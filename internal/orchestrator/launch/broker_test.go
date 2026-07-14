// ABOUTME: Unit tests for applyBrokerEnv — the pure secretEnv rewrite that swaps
// ABOUTME: the real API key for a base_url + placeholder pointing at the injector.
package launch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

func claudeBrokerConfig() *agent.BrokerConfig {
	return &agent.BrokerConfig{ //nolint:gosec // G101 false positive: env-var NAMES + a placeholder, not real credentials
		UpstreamURL: "https://api.anthropic.com",
		Destination: "api.anthropic.com",
		Credentials: []agent.BrokerCredential{
			{EnvVar: "ANTHROPIC_API_KEY", Header: "x-api-key", Prefix: ""},
			{EnvVar: "CLAUDE_CODE_OAUTH_TOKEN", Header: "Authorization", Prefix: "Bearer "},
		},
		PlaceholderHeader: "Authorization",
		BaseURLEnvVar:     "ANTHROPIC_BASE_URL",
		AuthTokenEnvVar:   "ANTHROPIC_AUTH_TOKEN",
		DummyToken:        "yoloai-broker-dummy",
	}
}

func TestApplyBrokerEnv_LinuxGatewayForBoth(t *testing.T) {
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "the-real-key", "OTHER": "keep"}
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	endpoint, err := applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "172.17.0.1:44115", "placeholder-tok")
	require.NoError(t, err)
	assert.Equal(t, "172.17.0.1:44115", endpoint, "agent-facing endpoint = DialHost:port")

	_, hasKey := secretEnv["ANTHROPIC_API_KEY"]
	assert.False(t, hasKey, "real key removed from the container env")
	assert.Equal(t, "http://172.17.0.1:44115", secretEnv["ANTHROPIC_BASE_URL"])
	assert.Equal(t, "placeholder-tok", secretEnv["ANTHROPIC_AUTH_TOKEN"], "the per-sandbox token is the agent's placeholder")
	assert.Equal(t, "keep", secretEnv["OTHER"], "unrelated secrets are untouched")
}

func TestApplyBrokerEnv_DialHostDiffersFromBindHost(t *testing.T) {
	// Docker Desktop: the injector binds the mac host loopback but the agent
	// reaches it via host.docker.internal. base_url must use DialHost + the bound port.
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "the-real-key"}
	reach := runtime.InjectorReach{BindHost: "127.0.0.1", DialHost: "host.docker.internal"}

	endpoint, err := applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "127.0.0.1:51000", "placeholder-tok")
	require.NoError(t, err)
	assert.Equal(t, "host.docker.internal:51000", endpoint, "endpoint uses DialHost not BindHost")

	assert.Equal(t, "http://host.docker.internal:51000", secretEnv["ANTHROPIC_BASE_URL"])
	_, hasKey := secretEnv["ANTHROPIC_API_KEY"]
	assert.False(t, hasKey)
}

func TestApplyBrokerEnv_BadInjectorAddr(t *testing.T) {
	secretEnv := map[string]string{"ANTHROPIC_API_KEY": "the-real-key"}
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	_, err := applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "not-an-addr", "placeholder-tok")
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

	_, err := applyBrokerEnv(secretEnv, claudeBrokerConfig(), reach, "172.17.0.1:44115", "placeholder-tok")
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
	apiKeySpec := buildInjectorSpec(t.TempDir(), bc, bc.Credentials[0], reach, "real-api-key", "placeholder-tok")
	require.Len(t, apiKeySpec.Bindings, 1)
	assert.Equal(t, "x-api-key", apiKeySpec.Bindings[0].Header)
	assert.Empty(t, apiKeySpec.Bindings[0].Prefix)
	assert.Equal(t, "real-api-key", apiKeySpec.Bindings[0].Secret)
	assert.Contains(t, apiKeySpec.StripHeaders, "Authorization", "the inbound dummy bearer is always stripped")
	assert.Equal(t, "placeholder-tok", apiKeySpec.ExpectedToken, "the injector verifies the per-sandbox token")

	// The subscription token is injected as Authorization: Bearer.
	oauthSpec := buildInjectorSpec(t.TempDir(), bc, bc.Credentials[1], reach, "real-oauth-token", "placeholder-tok")
	require.Len(t, oauthSpec.Bindings, 1)
	assert.Equal(t, "Authorization", oauthSpec.Bindings[0].Header)
	assert.Equal(t, "Bearer ", oauthSpec.Bindings[0].Prefix)
	assert.Equal(t, "real-oauth-token", oauthSpec.Bindings[0].Secret)
}

func TestApplyBrokerEnv_GeminiPlaceholderRidesInAPIKeyVar(t *testing.T) {
	// Gemini has no dedicated auth-token env var: the placeholder must ride in
	// GEMINI_API_KEY itself (AuthTokenEnvVar==GEMINI_API_KEY). applyBrokerEnv drops
	// the real key, then sets the same var to the placeholder.
	bc := agent.GetAgent("gemini").Broker
	require.NotNil(t, bc, "gemini must be wired for brokering")
	secretEnv := map[string]string{"GEMINI_API_KEY": "real-gemini-key", "OTHER": "keep"}
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	endpoint, err := applyBrokerEnv(secretEnv, bc, reach, "172.17.0.1:44115", "placeholder-tok")
	require.NoError(t, err)
	assert.Equal(t, "172.17.0.1:44115", endpoint)
	assert.Equal(t, "http://172.17.0.1:44115", secretEnv["GOOGLE_GEMINI_BASE_URL"])
	assert.Equal(t, "placeholder-tok", secretEnv["GEMINI_API_KEY"], "the real key is replaced by the placeholder in the same var")
	assert.Equal(t, "keep", secretEnv["OTHER"])
}

func TestApplyBrokerEnv_CodexDeliversNothingViaEnv(t *testing.T) {
	// Codex redirects via config.toml and delivers its placeholder via auth.json —
	// neither via env vars (BaseURLEnvVar=="" and AuthTokenEnvVar==""). So
	// applyBrokerEnv must NOT write any empty-keyed env var; it only drops the real
	// key(s) so they don't leak into the container. The files are handled separately
	// by patchBrokerConfigFiles.
	bc := agent.GetAgent("codex").Broker
	require.NotNil(t, bc, "codex must be wired for brokering")
	secretEnv := map[string]string{"OPENAI_API_KEY": "real-openai-key", "OTHER": "keep"}
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	_, err := applyBrokerEnv(secretEnv, bc, reach, "172.17.0.1:44115", "placeholder-tok")
	require.NoError(t, err)
	_, hasEmptyKey := secretEnv[""]
	assert.False(t, hasEmptyKey, "no env var is written for a file-delivered agent")
	_, hasKey := secretEnv["OPENAI_API_KEY"]
	assert.False(t, hasKey, "the real key is dropped from the container env")
	assert.NotContains(t, secretEnv, "CODEX_API_KEY", "no placeholder is delivered via env for Codex")
	assert.Equal(t, "keep", secretEnv["OTHER"])
}

func TestBuildInjectorSpec_StripsPlaceholderHeaderPerAgent(t *testing.T) {
	reach := runtime.InjectorReach{BindHost: "172.17.0.1", DialHost: "172.17.0.1"}

	// Gemini's placeholder rides in x-goog-api-key, so that is the header stripped
	// and searched for the token — not the hardcoded Authorization.
	gem := agent.GetAgent("gemini").Broker
	gemSpec := buildInjectorSpec(t.TempDir(), gem, gem.Credentials[0], reach, "real", "tok")
	assert.Equal(t, []string{"x-goog-api-key"}, gemSpec.StripHeaders)
	assert.Equal(t, "x-goog-api-key", gemSpec.Bindings[0].Header)

	// Codex carries its placeholder in Authorization (Bearer).
	cod := agent.GetAgent("codex").Broker
	codSpec := buildInjectorSpec(t.TempDir(), cod, cod.Credentials[0], reach, "real", "tok")
	assert.Equal(t, []string{"Authorization"}, codSpec.StripHeaders)
	assert.Equal(t, "Bearer ", codSpec.Bindings[0].Prefix)
}

func TestPatchBrokerConfigFiles(t *testing.T) {
	bc := agent.GetAgent("codex").Broker
	require.NotEmpty(t, bc.ConfigFiles, "codex delivers redirect + placeholder via config files")

	read := func(t *testing.T, sandboxDir, rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(sandboxDir, store.AgentRuntimeDir, rel)) //nolint:gosec // test path under t.TempDir()
		require.NoError(t, err)
		return string(data)
	}

	t.Run("writes both config.toml redirect and auth.json placeholder", func(t *testing.T) {
		sandboxDir := t.TempDir()
		require.NoError(t, patchBrokerConfigFiles(sandboxDir, bc, "172.17.0.1:44115", "per-sandbox-tok"))

		cfg := read(t, sandboxDir, "config.toml")
		assert.Contains(t, cfg, "openai_base_url")
		assert.Contains(t, cfg, "http://172.17.0.1:44115/v1", "Codex appends /responses to this, yielding the real /v1/responses path")

		auth := read(t, sandboxDir, "auth.json")
		assert.Contains(t, auth, "apikey")
		assert.Contains(t, auth, "per-sandbox-tok", "the placeholder — not the real key — is written to auth.json")
	})

	t.Run("preserves existing user config.toml", func(t *testing.T) {
		sandboxDir := t.TempDir()
		dir := filepath.Join(sandboxDir, store.AgentRuntimeDir)
		require.NoError(t, os.MkdirAll(dir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = \"gpt-5.3-codex\"\n"), 0o600))

		require.NoError(t, patchBrokerConfigFiles(sandboxDir, bc, "172.17.0.1:44115", "tok"))
		cfg := read(t, sandboxDir, "config.toml")
		assert.Contains(t, cfg, "gpt-5.3-codex", "unrelated user settings survive")
		assert.Contains(t, cfg, "openai_base_url")
	})

	t.Run("no-op for env-redirected agents", func(t *testing.T) {
		sandboxDir := t.TempDir()
		require.NoError(t, patchBrokerConfigFiles(sandboxDir, claudeBrokerConfig(), "172.17.0.1:44115", "tok"))
		_, err := os.Stat(filepath.Join(sandboxDir, store.AgentRuntimeDir))
		assert.True(t, os.IsNotExist(err), "nothing written when ConfigFiles is empty")
	})
}

func TestApplyWorkdirTrust(t *testing.T) {
	readConfig := func(t *testing.T, sandboxDir string) map[string]any {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(sandboxDir, store.AgentRuntimeDir, "config.toml")) //nolint:gosec // test path under t.TempDir()
		require.NoError(t, err)
		var cfg map[string]any
		require.NoError(t, toml.Unmarshal(data, &cfg))
		return cfg
	}

	t.Run("codex trusts the resolved container workdir", func(t *testing.T) {
		sandboxDir := t.TempDir()
		st := &state.State{
			SandboxDir: sandboxDir,
			Agent:      agent.GetAgent("codex"),
			Workdir:    &state.DirSpec{Path: "/home/karl/proj"},
		}
		require.NoError(t, applyWorkdirTrust(st))

		projects := readConfig(t, sandboxDir)["projects"].(map[string]any)
		entry, ok := projects["/home/karl/proj"].(map[string]any)
		require.True(t, ok, "the container workdir is recorded as a trusted project")
		assert.Equal(t, "trusted", entry["trust_level"])
	})

	t.Run("custom mount path is trusted, not the host path", func(t *testing.T) {
		sandboxDir := t.TempDir()
		st := &state.State{
			SandboxDir: sandboxDir,
			Agent:      agent.GetAgent("codex"),
			Workdir:    &state.DirSpec{Path: "/host/path", MountPath: "/container/path"},
		}
		require.NoError(t, applyWorkdirTrust(st))

		projects := readConfig(t, sandboxDir)["projects"].(map[string]any)
		assert.Contains(t, projects, "/container/path", "trusts the container path codex actually runs in")
		assert.NotContains(t, projects, "/host/path")
	})

	t.Run("no-op for an agent without WorkdirTrust", func(t *testing.T) {
		sandboxDir := t.TempDir()
		st := &state.State{
			SandboxDir: sandboxDir,
			Agent:      agent.GetAgent("gemini"),
			Workdir:    &state.DirSpec{Path: "/home/karl/proj"},
		}
		require.NoError(t, applyWorkdirTrust(st))
		_, err := os.Stat(filepath.Join(sandboxDir, store.AgentRuntimeDir))
		assert.True(t, os.IsNotExist(err), "nothing written when the agent declares no WorkdirTrust")
	})
}

func TestApplyDirectCredential(t *testing.T) {
	authPath := func(sandboxDir string) string {
		return filepath.Join(sandboxDir, store.AgentRuntimeDir, "auth.json")
	}

	t.Run("codex writes the real key to auth.json when not brokered", func(t *testing.T) {
		sandboxDir := t.TempDir()
		st := &state.State{SandboxDir: sandboxDir, Agent: agent.GetAgent("codex")}
		// Direct delivery: the real key is still in secretEnv (brokering would have removed it).
		secretEnv := map[string]string{"OPENAI_API_KEY": "sk-real-key"}

		require.NoError(t, applyDirectCredential(st, secretEnv))
		data, err := os.ReadFile(authPath(sandboxDir))
		require.NoError(t, err)
		var auth map[string]any
		require.NoError(t, json.Unmarshal(data, &auth))
		assert.Equal(t, "apikey", auth["auth_mode"])
		assert.Equal(t, "sk-real-key", auth["OPENAI_API_KEY"], "the real key authenticates Codex directly")
	})

	t.Run("no-op when the credential was brokered away", func(t *testing.T) {
		sandboxDir := t.TempDir()
		st := &state.State{SandboxDir: sandboxDir, Agent: agent.GetAgent("codex")}
		// Brokered: applyBrokerEnv removed the real key from secretEnv already.
		require.NoError(t, applyDirectCredential(st, map[string]string{}))
		_, err := os.Stat(authPath(sandboxDir))
		assert.True(t, os.IsNotExist(err), "brokered launch already wrote a placeholder auth.json; direct step must not overwrite it")
	})

	t.Run("no-op for an agent without DirectCredentialFile", func(t *testing.T) {
		sandboxDir := t.TempDir()
		st := &state.State{SandboxDir: sandboxDir, Agent: agent.GetAgent("gemini")}
		require.NoError(t, applyDirectCredential(st, map[string]string{"GEMINI_API_KEY": "real"}))
		_, err := os.Stat(filepath.Join(sandboxDir, store.AgentRuntimeDir))
		assert.True(t, os.IsNotExist(err), "gemini's env-var credential works directly; nothing written")
	})
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
