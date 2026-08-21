// ABOUTME: Tests for resolveAgentArgs, resolvedAgentFiles, and resolveEnvForRestart —
// ABOUTME: DF208: the restart/relaunch path's profile-chain merge base must be
// ABOUTME: baked-in defaults, never the user's personal defaults/config.yaml.
package lifecycle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newProfileTestLayout returns a Layout rooted at a fresh tmp dir, for tests
// that exercise resolveAgentArgs/resolvedAgentFiles/resolveEnvForRestart directly
// (these take a Layout, not a full state.Deps).
func newProfileTestLayout(t *testing.T) config.Layout {
	t.Helper()
	return config.NewLayout(filepath.Join(t.TempDir(), ".yoloai")).WithPrincipal(config.CLIPrincipal)
}

// writeProfileConfig writes a profile's config.yaml under layout's profiles dir.
func writeProfileConfig(t *testing.T, layout config.Layout, name, content string) {
	t.Helper()
	dir := layout.ProfileDir(name)
	require.NoError(t, os.MkdirAll(dir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))
}

// writePersonalDefaults writes the user's personal defaults/config.yaml.
func writePersonalDefaults(t *testing.T, layout config.Layout, content string) {
	t.Helper()
	path := layout.DefaultsConfigPath()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}

// TestResolveAgentArgs_PersonalDefaultsDoNotLeakIntoProfile pins
// lifecycle.go:106 (resolveAgentArgs): a profile that doesn't set its own
// agent_args must not resolve to the personal defaults/config.yaml's
// agent_args (DF207/DF208).
func TestResolveAgentArgs_PersonalDefaultsDoNotLeakIntoProfile(t *testing.T) {
	layout := newProfileTestLayout(t)
	writePersonalDefaults(t, layout, "agent_args:\n  claude: --personal-flag\n")
	writeProfileConfig(t, layout, "leaktest", "agent: test\n") // profile sets nothing else

	got := resolveAgentArgs(layout, "claude", "leaktest")
	assert.Empty(t, got, "personal agent_args must not carry into a profile")
}

// TestResolvedAgentFiles_PersonalDefaultsDoNotLeakIntoProfile pins
// restart.go's resolvedAgentFiles. This is the site where a base-argument
// swap alone is not enough: agent_files is commented out of the baked-in
// defaults, so a profile that sets no agent_files merges to a nil
// MergedConfig.AgentFiles, and the pre-fix code specifically fell back to the
// personal cfg.AgentFiles whenever merged.AgentFiles was nil. A revert of
// either half of the fix — the base-argument swap, or dropping that
// nil-fallback — makes this test fail.
func TestResolvedAgentFiles_PersonalDefaultsDoNotLeakIntoProfile(t *testing.T) {
	layout := newProfileTestLayout(t)
	writeProfileConfig(t, layout, "filesprofile", "agent: test\n") // profile sets no agent_files

	cfg := &config.YoloaiConfig{AgentFiles: &config.AgentFilesConfig{BaseDir: "/personal/dir"}}
	meta := &store.Environment{Profile: "filesprofile"}

	got := resolvedAgentFiles(layout, cfg, meta)
	assert.Nil(t, got, "personal agent_files must not carry into a profile that doesn't set its own")
}

// TestResolvedAgentFiles_ProfileOwnValueStillApplies guards against fixing
// the leak by breaking profiles outright: a profile that does set agent_files
// must still resolve to its own value, not nil and not the personal value.
func TestResolvedAgentFiles_ProfileOwnValueStillApplies(t *testing.T) {
	layout := newProfileTestLayout(t)
	writeProfileConfig(t, layout, "filesprofile2", "agent_files: /profile/dir\n")

	cfg := &config.YoloaiConfig{AgentFiles: &config.AgentFilesConfig{BaseDir: "/personal/dir"}}
	meta := &store.Environment{Profile: "filesprofile2"}

	got := resolvedAgentFiles(layout, cfg, meta)
	require.NotNil(t, got)
	assert.Equal(t, "/profile/dir", got.BaseDir)
}

// TestResolvedAgentFiles_ErrorPathReturnsNilNotPersonalValue pins the second,
// independent half of the DF208 fix at this site: on a profile-chain
// resolution error, the function must return nil, not the personal
// cfg.AgentFiles. agent_files' list form applies no credential-exclusion
// filter at all (DF201), so degrading a profile-active error to "copy the
// user's personal agent state, unfiltered, into a profile sandbox" is the
// unsafe direction — the pre-fix code did exactly that.
func TestResolvedAgentFiles_ErrorPathReturnsNilNotPersonalValue(t *testing.T) {
	layout := newProfileTestLayout(t)
	// No profile directory created for "missing" — ResolveProfileChain errors.

	cfg := &config.YoloaiConfig{AgentFiles: &config.AgentFilesConfig{BaseDir: "/personal/dir"}}
	meta := &store.Environment{Profile: "missing"}

	got := resolvedAgentFiles(layout, cfg, meta)
	assert.Nil(t, got, "a profile-active error must not fall back to the personal agent_files")
}

// TestResolveEnvForRestart_PersonalDefaultsDoNotLeakIntoProfile pins
// restart.go:88 (resolveEnvForRestart): a profile's own env must resolve, and
// the personal defaults/config.yaml's env must not carry into it (DF207/DF208).
func TestResolveEnvForRestart_PersonalDefaultsDoNotLeakIntoProfile(t *testing.T) {
	layout := newProfileTestLayout(t)
	writePersonalDefaults(t, layout, "env:\n  PERSONAL_SECRET: leak-me-not\n")
	writeProfileConfig(t, layout, "envprofile", "env:\n  PROFILE_VAR: profile-value\n")

	meta := &store.Environment{Profile: "envprofile"}
	envVars, err := resolveEnvForRestart(layout, meta)
	require.NoError(t, err)

	assert.NotContains(t, envVars, "PERSONAL_SECRET", "personal env must not carry into a profile")
	assert.Equal(t, "profile-value", envVars["PROFILE_VAR"])
}

// TestRestartPath_NoProfile_PersonalDefaultsStillApply is the no-profile
// regression guard (AGENTS.md rule 10, requirement 3): without --profile, the
// restart path must still resolve from the user's personal
// defaults/config.yaml, for all three call sites. This behavior is
// unaffected by DF208 and must not change.
func TestRestartPath_NoProfile_PersonalDefaultsStillApply(t *testing.T) {
	layout := newProfileTestLayout(t)
	writePersonalDefaults(t, layout, `
agent_args:
  claude: --personal-flag
agent_files: /personal/dir
env:
  PERSONAL_VAR: personal-value
`)

	t.Run("resolveAgentArgs", func(t *testing.T) {
		got := resolveAgentArgs(layout, "claude", "")
		assert.Equal(t, "--personal-flag", got)
	})

	t.Run("resolvedAgentFiles", func(t *testing.T) {
		cfg, err := config.LoadConfig(layout)
		require.NoError(t, err)
		meta := &store.Environment{} // no Profile
		got := resolvedAgentFiles(layout, cfg, meta)
		require.NotNil(t, got)
		assert.Equal(t, "/personal/dir", got.BaseDir)
	})

	t.Run("resolveEnvForRestart", func(t *testing.T) {
		meta := &store.Environment{} // no Profile
		envVars, err := resolveEnvForRestart(layout, meta)
		require.NoError(t, err)
		assert.Equal(t, "personal-value", envVars["PERSONAL_VAR"])
	})
}

// TestRestartPath_ProfileResolution_MatchesCreatePathGuarantee is the
// create/restart consistency test that is the point of this fix: a profile
// sandbox whose user config sets env, agent_args, and agent_files resolves
// clean values on the restart path — the same "no exceptions" guarantee
// config.md:165,167 states and internal/orchestrator/create/prepare_profile_test.go's
// TestResolveProfileConfig_PersonalDefaultsDoNotLeakIntoProfile pins for the
// create path.
//
// This asserts the restart-path resolution directly against the same
// expectations as that create-path test, rather than a true create-then-restart
// round trip: create and lifecycle are different packages with unexported entry
// points, and a full round trip would require container-launch mocking that has
// nothing to do with what these three merge-base functions resolve. The fixture
// (personal config, profile name and content) mirrors that test's "leaktest"
// case so the two are directly comparable.
func TestRestartPath_ProfileResolution_MatchesCreatePathGuarantee(t *testing.T) {
	layout := newProfileTestLayout(t)
	writePersonalDefaults(t, layout, `
agent_args:
  claude: --personal-flag
agent_files: /personal/dir
env:
  PERSONAL_SECRET: leak-me-not
`)
	writeProfileConfig(t, layout, "leaktest", "agent: test\n") // profile sets nothing else

	meta := &store.Environment{Profile: "leaktest"}

	agentArgs := resolveAgentArgs(layout, "claude", "leaktest")
	assert.Empty(t, agentArgs, "personal agent_args must not carry into a profile")

	agentFilesCfg := &config.YoloaiConfig{AgentFiles: &config.AgentFilesConfig{BaseDir: "/personal/dir"}}
	agentFiles := resolvedAgentFiles(layout, agentFilesCfg, meta)
	assert.Nil(t, agentFiles, "personal agent_files must not carry into a profile")

	envVars, err := resolveEnvForRestart(layout, meta)
	require.NoError(t, err)
	assert.NotContains(t, envVars, "PERSONAL_SECRET", "personal env must not carry into a profile")
}
