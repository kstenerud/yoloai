// ABOUTME: Unit tests for the Codex broker file patches — config.toml
// ABOUTME: openai_base_url and auth.json placeholder-key delivery.
package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	toml "github.com/pelletier/go-toml/v2"
)

func TestPatchCodexBaseURL_CreatesFromEmpty(t *testing.T) {
	out, err := patchCodexBaseURL(nil, "http://172.17.0.1:44115", "ignored-placeholder")
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, toml.Unmarshal(out, &cfg))
	// The "/v1" suffix makes Codex's appended "/responses" resolve to the real
	// "/v1/responses" once the injector forwards to api.openai.com.
	assert.Equal(t, "http://172.17.0.1:44115/v1", cfg["openai_base_url"])
}

func TestPatchCodexBaseURL_PreservesExistingConfig(t *testing.T) {
	existing := []byte("model = \"gpt-5.3-codex\"\napproval_policy = \"never\"\n")
	out, err := patchCodexBaseURL(existing, "http://10.0.0.1:5000", "")
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, toml.Unmarshal(out, &cfg))
	assert.Equal(t, "http://10.0.0.1:5000/v1", cfg["openai_base_url"])
	assert.Equal(t, "gpt-5.3-codex", cfg["model"], "unrelated user settings survive the patch")
	assert.Equal(t, "never", cfg["approval_policy"])
}

func TestPatchCodexBaseURL_Idempotent(t *testing.T) {
	first, err := patchCodexBaseURL([]byte("model = \"gpt-5.3-codex\"\n"), "http://10.0.0.1:5000", "")
	require.NoError(t, err)
	second, err := patchCodexBaseURL(first, "http://10.0.0.1:5000", "")
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second), "re-patching the same endpoint is stable")
}

func TestPatchCodexBaseURL_RejectsInvalid(t *testing.T) {
	_, err := patchCodexBaseURL([]byte("this is = = not valid toml ]["), "http://x", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse codex config.toml")
}

func TestPatchCodexAuth_WritesApiKeyLoginShape(t *testing.T) {
	// Codex 0.144 authenticates from auth.json, not a bare env var, so the
	// placeholder must be written in the shape `codex login --with-api-key` produces.
	out, err := patchCodexAuth(nil, "http://ignored", "per-sandbox-placeholder")
	require.NoError(t, err)

	var auth map[string]any
	require.NoError(t, json.Unmarshal(out, &auth))
	assert.Equal(t, "apikey", auth["auth_mode"])
	assert.Equal(t, "per-sandbox-placeholder", auth["OPENAI_API_KEY"], "the placeholder — not the real key — goes into the sandbox")
}

func TestRenderCodexAuth_DirectRealKey(t *testing.T) {
	// The non-brokered path writes the real key into auth.json (DF84), same shape
	// as the brokered placeholder — a bare env var doesn't authenticate Codex.
	out, err := renderCodexAuth("sk-real-openai-key")
	require.NoError(t, err)

	var auth map[string]any
	require.NoError(t, json.Unmarshal(out, &auth))
	assert.Equal(t, "apikey", auth["auth_mode"])
	assert.Equal(t, "sk-real-openai-key", auth["OPENAI_API_KEY"])
}

func TestPatchCodexWorkdirTrust_MarksWorkdirTrusted(t *testing.T) {
	out, err := patchCodexWorkdirTrust(nil, "/home/karl/tmp")
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, toml.Unmarshal(out, &cfg))
	projects, ok := cfg["projects"].(map[string]any)
	require.True(t, ok, "projects table written")
	entry, ok := projects["/home/karl/tmp"].(map[string]any)
	require.True(t, ok, "workdir marked as a project")
	assert.Equal(t, "trusted", entry["trust_level"])
}

func TestPatchCodexWorkdirTrust_PreservesBrokerPatchAndOtherProjects(t *testing.T) {
	// Composes with the broker's openai_base_url patch and existing trusted
	// projects on the same file (the launch path runs this after the broker patch).
	existing := []byte("openai_base_url = 'http://172.17.0.1:44115/v1'\n\n[projects.'/home/karl']\ntrust_level = 'trusted'\n")
	out, err := patchCodexWorkdirTrust(existing, "/work/proj")
	require.NoError(t, err)

	var cfg map[string]any
	require.NoError(t, toml.Unmarshal(out, &cfg))
	assert.Equal(t, "http://172.17.0.1:44115/v1", cfg["openai_base_url"], "broker redirect survives")
	projects := cfg["projects"].(map[string]any)
	assert.Contains(t, projects, "/home/karl", "existing trusted project survives")
	assert.Equal(t, "trusted", projects["/work/proj"].(map[string]any)["trust_level"], "workdir added")
}

func TestPatchCodexWorkdirTrust_Idempotent(t *testing.T) {
	first, err := patchCodexWorkdirTrust([]byte("model = 'gpt-5.3-codex'\n"), "/work/proj")
	require.NoError(t, err)
	second, err := patchCodexWorkdirTrust(first, "/work/proj")
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second), "re-trusting the same workdir is stable")
}

func TestPatchCodexWorkdirTrust_EmptyWorkdirIsNoOp(t *testing.T) {
	in := []byte("model = 'gpt-5.3-codex'\n")
	out, err := patchCodexWorkdirTrust(in, "")
	require.NoError(t, err)
	assert.Equal(t, string(in), string(out), "no workdir → config untouched")
}

func TestPatchCodexAuth_OverwritesSeededAuth(t *testing.T) {
	// A brokered launch always replaces auth.json with the placeholder, even if a
	// host auth.json leaked through — the real credential must never reach the box.
	seeded := []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-REAL-should-be-gone"}`)
	out, err := patchCodexAuth(seeded, "", "placeholder-only")
	require.NoError(t, err)
	assert.NotContains(t, string(out), "sk-REAL-should-be-gone")
	assert.Contains(t, string(out), "placeholder-only")
}
