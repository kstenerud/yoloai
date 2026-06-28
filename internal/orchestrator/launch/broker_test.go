// ABOUTME: Unit tests for applyBrokerEnv — the pure secretEnv rewrite that swaps
// ABOUTME: the real API key for a base_url + placeholder pointing at the injector.
package launch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
)

func claudeBrokerConfig() *agent.BrokerConfig {
	return &agent.BrokerConfig{ //nolint:gosec // G101 false positive: env-var NAMES + a placeholder, not real credentials
		UpstreamURL:     "https://api.anthropic.com",
		Destination:     "api.anthropic.com",
		Header:          "x-api-key",
		APIKeyEnvVar:    "ANTHROPIC_API_KEY",
		BaseURLEnvVar:   "ANTHROPIC_BASE_URL",
		AuthTokenEnvVar: "ANTHROPIC_AUTH_TOKEN",
		DummyToken:      "yoloai-broker-dummy",
	}
}

func TestApplyBrokerEnv_LinuxGatewayForBoth(t *testing.T) {
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "the-real-key", "OTHER": "keep"}
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	require.NoError(t, applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "172.17.0.1:44115"))

	_, hasKey := secretEnv["ANTHROPIC_API_KEY"]
	assert.False(t, hasKey, "real key removed from the container env")
	assert.Equal(t, "http://172.17.0.1:44115", secretEnv["ANTHROPIC_BASE_URL"])
	assert.Equal(t, "yoloai-broker-dummy", secretEnv["ANTHROPIC_AUTH_TOKEN"])
	assert.Equal(t, "keep", secretEnv["OTHER"], "unrelated secrets are untouched")
}

func TestApplyBrokerEnv_DialHostDiffersFromBindHost(t *testing.T) {
	// Docker Desktop: the injector binds the mac host loopback but the agent
	// reaches it via host.docker.internal. base_url must use DialHost + the bound port.
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "the-real-key"}
	reach := runtime.InjectorReach{BindHost: "127.0.0.1", DialHost: "host.docker.internal"}

	require.NoError(t, applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "127.0.0.1:51000"))

	assert.Equal(t, "http://host.docker.internal:51000", secretEnv["ANTHROPIC_BASE_URL"])
	_, hasKey := secretEnv["ANTHROPIC_API_KEY"]
	assert.False(t, hasKey)
}

func TestApplyBrokerEnv_BadInjectorAddr(t *testing.T) {
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "the-real-key"}
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	err := applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "not-an-addr")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse injector address")
	// On error the real key must remain (we did not partially rewrite into a leak).
	assert.Equal(t, "the-real-key", secretEnv["ANTHROPIC_API_KEY"])
}

// brokerCredentials gate (the default-on flip)

// descriptorBackend satisfies runtime.Backend (methods panic) but supplies a
// real Descriptor — enough to exercise brokerCredentials' gate, which only needs
// the type name for the forced-on error. It does NOT implement InjectorReachable,
// so it stands in for a backend that can't host an injector.
type descriptorBackend struct{ runtime.Backend }

func (descriptorBackend) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{Type: "fakebackend"}
}

func claudeState(t *testing.T, st *state.State) *state.State {
	t.Helper()
	st.Name = "box"
	st.SandboxDir = t.TempDir()
	st.Agent = &agent.Definition{Broker: claudeBrokerConfig()}
	return st
}

func TestBrokerCredentials_ForcedOffSkips(t *testing.T) {
	st := claudeState(t, &state.State{BrokerDisabled: true})
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "real"}

	// --no-broker: returns immediately, never touches the (panic) backend, key intact.
	require.NoError(t, brokerCredentials(context.Background(), panicBackend{}, st, "cname", secretEnv))
	assert.Equal(t, "real", secretEnv["ANTHROPIC_API_KEY"], "forced-off leaves direct delivery")
}

func TestBrokerCredentials_AutoOnUnreachableBackendIsSilentDirect(t *testing.T) {
	st := claudeState(t, &state.State{}) // auto: neither forced-on nor forced-off
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "real"}

	// Backend can't host an injector; auto mode falls back to direct delivery
	// (no error — the default must not break non-supporting backends).
	require.NoError(t, brokerCredentials(context.Background(), descriptorBackend{}, st, "cname", secretEnv))
	assert.Equal(t, "real", secretEnv["ANTHROPIC_API_KEY"])
}

func TestBrokerCredentials_ForcedOnUnreachableBackendErrors(t *testing.T) {
	st := claudeState(t, &state.State{BrokerCredentials: true}) // --broker
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "real"}

	// --broker against a backend that can't host an injector is a hard error,
	// not a silent leak of the key via direct delivery.
	err := brokerCredentials(context.Background(), descriptorBackend{}, st, "cname", secretEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot host a sandbox-reachable injector")
}

func TestBrokerCredentials_NoBrokerableKeySkips(t *testing.T) {
	st := claudeState(t, &state.State{}) // auto
	secretEnv := map[string]string{}     // subscription/OAuth: no API key

	// No brokerable key -> direct delivery, no backend touched, no error.
	require.NoError(t, brokerCredentials(context.Background(), panicBackend{}, st, "cname", secretEnv))
}

func TestBrokerCredentials_RestrictedNetworkAutoSkips(t *testing.T) {
	for _, mode := range []string{"isolated", "none"} {
		st := claudeState(t, &state.State{NetworkMode: mode}) // auto
		secretEnv := map[string]string{"ANTHROPIC_API_KEY": "real"}
		// Auto under restricted networking falls back to direct, no backend touched.
		require.NoError(t, brokerCredentials(context.Background(), panicBackend{}, st, "cname", secretEnv), mode)
		assert.Equal(t, "real", secretEnv["ANTHROPIC_API_KEY"], mode)
	}
}

func TestBrokerCredentials_RestrictedNetworkForcedErrors(t *testing.T) {
	st := claudeState(t, &state.State{NetworkMode: "isolated", BrokerCredentials: true})
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "real"}
	err := brokerCredentials(context.Background(), panicBackend{}, st, "cname", secretEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet supported with --network-isolated")
}
