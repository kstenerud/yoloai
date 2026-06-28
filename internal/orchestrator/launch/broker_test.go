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
		UpstreamURL: "https://api.anthropic.com",
		Destination: "api.anthropic.com",
		Credentials: []agent.BrokerCredential{
			{EnvVar: "ANTHROPIC_API_KEY", Header: "x-api-key", Prefix: ""},
			{EnvVar: "CLAUDE_CODE_OAUTH_TOKEN", Header: "Authorization", Prefix: "Bearer "},
		},
		BaseURLEnvVar:   "ANTHROPIC_BASE_URL",
		AuthTokenEnvVar: "ANTHROPIC_AUTH_TOKEN",
		DummyToken:      "yoloai-broker-dummy",
	}
}

func TestApplyBrokerEnv_LinuxGatewayForBoth(t *testing.T) {
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "the-real-key", "OTHER": "keep"}
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	endpoint, err := applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "172.17.0.1:44115")
	require.NoError(t, err)
	assert.Equal(t, "172.17.0.1:44115", endpoint, "agent-facing endpoint = DialHost:port")

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

	endpoint, err := applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "127.0.0.1:51000")
	require.NoError(t, err)
	assert.Equal(t, "host.docker.internal:51000", endpoint, "endpoint uses DialHost not BindHost")

	assert.Equal(t, "http://host.docker.internal:51000", secretEnv["ANTHROPIC_BASE_URL"])
	_, hasKey := secretEnv["ANTHROPIC_API_KEY"]
	assert.False(t, hasKey)
}

func TestApplyBrokerEnv_BadInjectorAddr(t *testing.T) {
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "the-real-key"}
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	_, err := applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "not-an-addr")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse injector address")
	// On error the real key must remain (we did not partially rewrite into a leak).
	assert.Equal(t, "the-real-key", secretEnv["ANTHROPIC_API_KEY"])
}

func TestApplyBrokerEnv_DropsEveryBrokerableCredential(t *testing.T) {
	// A user with BOTH an API key and a subscription token: brokering the API key
	// must still strip the OAuth token from the container env — no brokerable
	// credential, selected or not, may leak in.
	secretEnv := map[string]string{ //nolint:gosec // G101 false positive: test fixture values, not real credentials
		"ANTHROPIC_API_KEY":       "the-real-key",
		"CLAUDE_CODE_OAUTH_TOKEN": "the-oauth-token",
	}
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	_, err := applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "172.17.0.1:44115")
	require.NoError(t, err)

	_, hasKey := secretEnv["ANTHROPIC_API_KEY"]
	assert.False(t, hasKey, "API key removed")
	_, hasToken := secretEnv["CLAUDE_CODE_OAUTH_TOKEN"]
	assert.False(t, hasToken, "the unselected OAuth token must not leak into the container")
}

func TestBuildInjectorSpec_PerCredentialInjection(t *testing.T) {
	bc := claudeBrokerConfig()
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	// The API key is injected raw into x-api-key.
	apiKeySpec := buildInjectorSpec(t.TempDir(), bc, bc.Credentials[0], reach, "real-api-key")
	require.Len(t, apiKeySpec.Bindings, 1)
	assert.Equal(t, "x-api-key", apiKeySpec.Bindings[0].Header)
	assert.Empty(t, apiKeySpec.Bindings[0].Prefix)
	assert.Equal(t, "real-api-key", apiKeySpec.Bindings[0].Secret)
	assert.Contains(t, apiKeySpec.StripHeaders, "Authorization", "the inbound dummy bearer is always stripped")

	// The subscription token is injected as Authorization: Bearer.
	oauthSpec := buildInjectorSpec(t.TempDir(), bc, bc.Credentials[1], reach, "real-oauth-token")
	require.Len(t, oauthSpec.Bindings, 1)
	assert.Equal(t, "Authorization", oauthSpec.Bindings[0].Header)
	assert.Equal(t, "Bearer ", oauthSpec.Bindings[0].Prefix)
	assert.Equal(t, "real-oauth-token", oauthSpec.Bindings[0].Secret)
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
	_, err := brokerCredentials(context.Background(), panicBackend{}, st, secretEnv)
	require.NoError(t, err)
	assert.Equal(t, "real", secretEnv["ANTHROPIC_API_KEY"], "forced-off leaves direct delivery")
}

func TestBrokerCredentials_AutoOnUnreachableBackendIsSilentDirect(t *testing.T) {
	st := claudeState(t, &state.State{}) // auto: neither forced-on nor forced-off
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "real"}

	// Backend can't host an injector; auto mode falls back to direct delivery
	// (no error — the default must not break non-supporting backends).
	_, err := brokerCredentials(context.Background(), descriptorBackend{}, st, secretEnv)
	require.NoError(t, err)
	assert.Equal(t, "real", secretEnv["ANTHROPIC_API_KEY"])
}

func TestBrokerCredentials_ForcedOnUnreachableBackendErrors(t *testing.T) {
	st := claudeState(t, &state.State{BrokerCredentials: true}) // --broker
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "real"}

	// --broker against a backend that can't host an injector is a hard error,
	// not a silent leak of the key via direct delivery.
	_, err := brokerCredentials(context.Background(), descriptorBackend{}, st, secretEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot host a sandbox-reachable injector")
}

func TestBrokerCredentials_NoBrokerableKeySkips(t *testing.T) {
	st := claudeState(t, &state.State{}) // auto
	secretEnv := map[string]string{}     // subscription/OAuth: no API key

	// No brokerable key -> direct delivery, no backend touched, no error.
	_, err := brokerCredentials(context.Background(), panicBackend{}, st, secretEnv)
	require.NoError(t, err)
}

func TestBrokerCredentials_NetworkNoneAutoSkips(t *testing.T) {
	// --network-none has no egress: auto falls back to direct delivery without
	// ever touching the backend (panicBackend would panic if reach were resolved).
	st := claudeState(t, &state.State{NetworkMode: "none"})
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "real"}
	_, err := brokerCredentials(context.Background(), panicBackend{}, st, secretEnv)
	require.NoError(t, err)
	assert.Equal(t, "real", secretEnv["ANTHROPIC_API_KEY"])
}

func TestBrokerCredentials_NetworkNoneForcedErrors(t *testing.T) {
	st := claudeState(t, &state.State{NetworkMode: "none", BrokerCredentials: true})
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "real"}
	_, err := brokerCredentials(context.Background(), panicBackend{}, st, secretEnv)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported with --network-none")
}

// slirpReachBackend is reachable but needs a dedicated network mode (like rootless
// podman's slirp), which can't compose with --network-isolated yet.
type slirpReachBackend struct{ runtime.Backend }

func (slirpReachBackend) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{Type: "slirpmock"}
}

func (slirpReachBackend) InjectorReach(context.Context) (runtime.InjectorReach, error) {
	return runtime.InjectorReach{BindHost: "127.0.0.1", DialHost: "10.0.2.2", RequiredNetworkMode: "slirp4netns:allow_host_loopback=true"}, nil
}

func TestBrokerCredentials_IsolatedWithSpecialNetModeRefused(t *testing.T) {
	secretEnv := func() map[string]string { return map[string]string{"ANTHROPIC_API_KEY": "real"} }

	// Auto: a backend needing slirp can't compose with isolation → direct delivery.
	stAuto := claudeState(t, &state.State{NetworkMode: "isolated"})
	env := secretEnv()
	_, err := brokerCredentials(context.Background(), slirpReachBackend{}, stAuto, env)
	require.NoError(t, err)
	assert.Equal(t, "real", env["ANTHROPIC_API_KEY"], "auto falls back to direct delivery")

	// Forced: explicit --broker errors rather than silently degrading.
	stForced := claudeState(t, &state.State{NetworkMode: "isolated", BrokerCredentials: true})
	_, err = brokerCredentials(context.Background(), slirpReachBackend{}, stForced, secretEnv())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet supported with --network-isolated")
}
