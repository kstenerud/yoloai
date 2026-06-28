//go:build integration

// ABOUTME: End-to-end credential-broker test on real Docker: a brokered launch
// ABOUTME: keeps the credential host-side, points the agent at the injector, and the
// ABOUTME: container reaches the injector (gateway) which swaps in the real credential.
package orchestrator_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/broker"
	"github.com/kstenerud/yoloai/internal/orchestrator"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// brokerCannedSSE is a minimal Anthropic-style stream carrying a marker the test
// asserts the container received back through the injector.
const brokerCannedSSE = "event: message_start\n" +
	`data: {"type":"message_start"}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"delta":{"type":"text_delta","text":"BROKER_INTEGRATION_OK"}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

// brokerMockUpstream records the credential it was sent and returns canned SSE.
type brokerMockUpstream struct {
	mu      sync.Mutex
	gotKey  string
	gotAuth string
	hits    int
}

// TestIntegration_CredentialBroker exercises the full brokered launch path on
// real Docker for each brokerable credential form: the real credential stays
// host-side, the container env is rewritten to point at the injector with a
// placeholder, and a request from inside the container flows container -> bridge
// gateway -> injector -> mock upstream with the real credential swapped in
// host-side. The injector is reaped on destroy.
//
// Two cases cover Phase 1 and Phase 2: an API key injected raw into x-api-key,
// and a subscription OAuth token injected as Authorization: Bearer. The
// agent-facing redirect is identical in both; only the injector's upstream
// injection differs.
//
// docker exec starts a fresh process that does NOT inherit the agent's env, so
// the brokered env is observed by having the agent's prompt dump its env to a
// file (the same technique as TestIntegration_CredentialInjection); the
// container->injector request is then driven via exec with an explicit argv
// (literal base_url) to avoid any shell quoting.
func TestIntegration_CredentialBroker(t *testing.T) {
	mgr, ctx := integrationSetup(t)
	runBrokerScenarios(t, mgr, ctx)
}

// TestIntegration_CredentialBroker_LegacyPath runs the same brokered-launch
// scenarios with the runtime forced onto the LEGACY (non-agent-free) bring-up,
// proving that brokering is now decoupled from the agent-free launch path: the
// real credential stays host-side and the agent (run inline via the entrypoint,
// secrets delivered as /run/secrets files) reaches the injector all the same.
func TestIntegration_CredentialBroker_LegacyPath(t *testing.T) {
	mgr, ctx := legacyDockerIntegrationSetup(t)
	runBrokerScenarios(t, mgr, ctx)
}

// TestIntegration_CredentialBroker_Podman runs the same brokered-launch scenarios
// on rootless Podman: the legacy launch path + the decoupled broker + the slirp
// InjectorReach (injector binds 127.0.0.1, the slirp sandbox reaches it via
// 10.0.2.2). Proves brokering works on a backend with neither a ProcessLauncher
// nor a host-bindable bridge gateway. Skips when Podman is unavailable.
func TestIntegration_CredentialBroker_Podman(t *testing.T) {
	// This test's brokering target is LINUX rootless podman (slirp4netns host
	// loopback), the validated podman path. On macOS podman runs in a podman-machine
	// VM whose host hops don't carry the real agent's traffic reliably, so podman
	// reports ErrInjectorUnsupported on darwin and brokering degrades to direct
	// delivery (see runtime/podman/reach.go + the real-agent smoke evidence). With
	// direct delivery the real credential intentionally enters the container, which
	// is the opposite of what this broker test asserts — so it cannot run on macOS.
	if goruntime.GOOS == "darwin" {
		t.Skip("podman doesn't broker on macOS (podman-machine host hops fail the real agent → direct delivery); brokering is validated on Linux rootless podman")
	}
	mgr, ctx := podmanIntegrationSetup(t)
	runBrokerScenarios(t, mgr, ctx)
}

// TestIntegration_Podman_DirectDeliveryOnMacOS is the counterpart that locks the
// macOS posture: podman reports ErrInjectorUnsupported on darwin, so a brokerable
// agent with a credential present must DEGRADE TO DIRECT DELIVERY rather than
// broker — no host-side injector is started, and the real credential is delivered
// into the container as-is. This is the regression guard for the smoke-test failure
// where making podman-macOS broker via a podman-machine host hop hung the real
// agent: brokering must stay off on podman-macOS until a streaming-safe host hop
// exists. Linux-only behavior (Linux rootless DOES broker) is covered above.
func TestIntegration_Podman_DirectDeliveryOnMacOS(t *testing.T) {
	if goruntime.GOOS != "darwin" {
		t.Skip("guards the macOS podman direct-delivery posture; only meaningful on darwin")
	}
	mgr, ctx := podmanIntegrationSetup(t)

	const realCred = "sk-test-REAL-should-be-delivered-direct"
	def := agent.GetAgent("test")
	require.NotNil(t, def, "the test agent must be registered")
	origBroker := def.Broker
	def.Broker = &agent.BrokerConfig{ //nolint:gosec // G101 false positive: env-var names + a placeholder, not real credentials
		UpstreamURL: "http://127.0.0.1:1", // never contacted — brokering must not engage
		Destination: "127.0.0.1",
		Credentials: []agent.BrokerCredential{
			{EnvVar: "TEST_BROKER_KEY", Header: "x-api-key", Prefix: ""},
		},
		BaseURLEnvVar:   "TEST_BROKER_BASE_URL",
		AuthTokenEnvVar: "TEST_BROKER_AUTH",
		DummyToken:      "dummy-broker-placeholder",
	}
	t.Cleanup(func() { def.Broker = origBroker })

	name := "podman-direct-macos"
	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    name,
		Workdir: orchestrator.DirSpec{Path: createProjectDir(t)},
		Agent:   "test",
		Prompt:  "env > /tmp/broker-env.txt; sleep 30",
		Version: "test",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = destroySandbox(ctx, mgr, name) })

	_, err = startSandbox(ctx, mgr, name, orchestrator.StartOptions{
		Env: map[string]string{"TEST_BROKER_KEY": realCred},
	})
	require.NoError(t, err)

	instance := store.InstanceName("", name)
	testutil.WaitForActive(ctx, t, mgr.Runtime(), instance, 15*time.Second)

	// No injector is started for a podman-macOS sandbox — brokering degraded.
	assert.False(t, broker.InjectorAlive(mgr.Layout().SandboxDir(name)),
		"podman on macOS must NOT start an injector (direct delivery)")

	// The agent env carries the real credential directly, with no base_url redirect.
	var envDump string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r, execErr := mgr.Runtime().Exec(ctx, instance, []string{"cat", "/tmp/broker-env.txt"}, "yoloai")
		if execErr == nil && r.ExitCode == 0 && strings.Contains(r.Stdout, "TEST_BROKER_KEY=") {
			envDump = r.Stdout
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NotEmpty(t, envDump, "agent never dumped its env within 15s")
	env := parseEnvDump(envDump)
	assert.Equal(t, realCred, env["TEST_BROKER_KEY"], "the real credential is delivered directly")
	assert.Empty(t, env["TEST_BROKER_BASE_URL"], "no injector base_url redirect under direct delivery")
}

func runBrokerScenarios(t *testing.T, mgr *orchestrator.Engine, ctx context.Context) {
	t.Helper()
	cases := []struct {
		name     string // subtest name + sandbox suffix
		envVar   string // env var the real credential is delivered in
		header   string // header the injector injects it into
		prefix   string // value prefix ("" for x-api-key, "Bearer " for OAuth)
		realCred string // the real credential value (host-side only)
		wantKey  string // expected x-api-key the mock sees ("" if none)
		wantAuth string // expected Authorization the mock sees ("" if none)
	}{
		{
			name:     "api-key",
			envVar:   "TEST_BROKER_KEY",
			header:   "x-api-key",
			prefix:   "",
			realCred: "sk-test-REAL-broker-key-xyz",
			wantKey:  "sk-test-REAL-broker-key-xyz",
			wantAuth: "",
		},
		{
			name:     "oauth-bearer",
			envVar:   "TEST_BROKER_OAUTH",
			header:   "Authorization",
			prefix:   "Bearer ",
			realCred: "oauth-test-REAL-token-abc",
			wantKey:  "",
			wantAuth: "Bearer oauth-test-REAL-token-abc",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Host-side mock "upstream": the injector forwards here (loopback,
			// host-side) instead of real api.anthropic.com. The container never
			// talks to it directly.
			mock := &brokerMockUpstream{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				mock.mu.Lock()
				mock.hits++
				mock.gotKey = r.Header.Get("x-api-key")
				mock.gotAuth = r.Header.Get("Authorization")
				mock.mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, brokerCannedSSE)
			}))
			defer srv.Close()

			// Make the keepalive "test" agent brokerable, pointing its upstream at
			// the mock with a single credential for this case. The bash keepalive
			// keeps the container up to exec into, decoupling the test from driving
			// a real agent's wire protocol. Restored after the subtest.
			mockHost, _, _ := strings.Cut(strings.TrimPrefix(srv.URL, "http://"), ":")
			def := agent.GetAgent("test")
			require.NotNil(t, def, "the test agent must be registered")
			origBroker := def.Broker
			def.Broker = &agent.BrokerConfig{ //nolint:gosec // G101 false positive: env-var names + a placeholder, not real credentials
				UpstreamURL: srv.URL,
				Destination: mockHost,
				Credentials: []agent.BrokerCredential{
					{EnvVar: tc.envVar, Header: tc.header, Prefix: tc.prefix},
				},
				BaseURLEnvVar:   "TEST_BROKER_BASE_URL",
				AuthTokenEnvVar: "TEST_BROKER_AUTH",
				DummyToken:      "dummy-broker-placeholder",
			}
			t.Cleanup(func() { def.Broker = origBroker })

			name := "broker-integ-" + tc.name
			_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
				Name:    name,
				Workdir: orchestrator.DirSpec{Path: createProjectDir(t)},
				Agent:   "test",
				// The agent process has the brokered env; dump it to a file so exec
				// (a fresh process) can read it. Keepalive sleep keeps the agent
				// alive across the assertions.
				Prompt:  "env > /tmp/broker-env.txt; sleep 30",
				Version: "test",
			})
			require.NoError(t, err)
			t.Cleanup(func() { _, _ = destroySandbox(ctx, mgr, name) }) // safety net if an assert fails early

			// Brokering is auto-on (default): docker is InjectorReachable, the agent
			// is brokerable, the credential is present, and networking is open.
			_, err = startSandbox(ctx, mgr, name, orchestrator.StartOptions{
				Env: map[string]string{tc.envVar: tc.realCred},
			})
			require.NoError(t, err)

			instance := store.InstanceName("", name)
			testutil.WaitForActive(ctx, t, mgr.Runtime(), instance, 15*time.Second)

			// Poll until the agent has dumped its env.
			var envDump string
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				r, execErr := mgr.Runtime().Exec(ctx, instance, []string{"cat", "/tmp/broker-env.txt"}, "yoloai")
				if execErr == nil && r.ExitCode == 0 && strings.Contains(r.Stdout, "TEST_BROKER_BASE_URL=") {
					envDump = r.Stdout
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			require.NotEmpty(t, envDump, "agent never dumped its env within 15s")
			env := parseEnvDump(envDump)

			// 1. The real credential never enters the container.
			_, hasCred := env[tc.envVar]
			assert.False(t, hasCred, "real credential must not enter the container env")

			// 2. The agent is pointed at the injector with the placeholder.
			assert.Equal(t, "dummy-broker-placeholder", env["TEST_BROKER_AUTH"])
			baseURL := env["TEST_BROKER_BASE_URL"]
			assert.True(t, strings.HasPrefix(baseURL, "http://"), "base_url should point at the injector, got %q", baseURL)

			// 3. The host-side injector is running for this sandbox.
			assert.True(t, broker.InjectorAlive(mgr.Layout().SandboxDir(name)), "the per-sandbox injector should be alive")

			// 4. A request from inside the container reaches the injector through the
			//    bridge gateway; the injector swaps the placeholder for the real
			//    credential and forwards to the mock, streaming the response back.
			//    Explicit argv avoids any shell quoting of the header/url.
			resp, err := mgr.Runtime().Exec(ctx, instance, []string{
				"curl", "-s", "-m", "8", "-X", "POST", baseURL + "/v1/messages",
				"-H", "Authorization: Bearer dummy-broker-placeholder", "-d", "{}",
			}, "yoloai")
			require.NoError(t, err)
			assert.Contains(t, resp.Stdout, "BROKER_INTEGRATION_OK", "the injector should forward to the mock and stream the response back")

			mock.mu.Lock()
			gotKey, gotAuth, hits := mock.gotKey, mock.gotAuth, mock.hits
			mock.mu.Unlock()
			assert.GreaterOrEqual(t, hits, 1, "the mock upstream should have been reached")
			assert.Equal(t, tc.wantKey, gotKey, "x-api-key seen by the upstream (real key injected host-side, or empty)")
			assert.Equal(t, tc.wantAuth, gotAuth, "Authorization seen by the upstream (real bearer injected host-side, or placeholder stripped)")

			// 5. Destroy reaps the injector.
			_, err = destroySandbox(ctx, mgr, name)
			require.NoError(t, err)
			assert.False(t, broker.InjectorAlive(mgr.Layout().SandboxDir(name)), "the injector should be reaped on destroy")
		})
	}
}

// parseEnvDump parses `env` output (KEY=VALUE lines) into a map.
func parseEnvDump(dump string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(dump, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = v
		}
	}
	return out
}
