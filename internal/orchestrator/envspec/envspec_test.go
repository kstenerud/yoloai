// ABOUTME: Tests for BuildEnvSpec — verifies that agent.Definition fields are
// ABOUTME: correctly compiled into an agent-agnostic envsetup.EnvSpec.
package envspec_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/orchestrator/envspec"
	"github.com/kstenerud/yoloai/store"
)

// sbDir is an arbitrary sandbox root: these tests assert where a patch resolves
// relative to it, not that any real directory exists.
const sbDir = "/sandboxes/demo"

func TestBuildEnvSpec_NormalAgent(t *testing.T) {
	def := agent.GetAgent("claude")
	require.NotNil(t, def)

	spec := envspec.BuildEnvSpec(def)

	assert.Equal(t, ".claude", spec.StateRelPath)
	assert.True(t, spec.HasStateDir)
	assert.NotEmpty(t, spec.SeedFiles)
	assert.True(t, spec.ShortLivedOAuthWarning)
	assert.Equal(t, "claude", spec.AgentName)
	assert.False(t, spec.UserDefined, "a shipped agent.go Definition must not carry the file-defined marker")

	// Field mapping check on first seed file
	var found bool
	for _, sf := range spec.SeedFiles {
		if sf.TargetPath == ".credentials.json" {
			assert.True(t, sf.AuthOnly)
			assert.Equal(t, "Claude Code-credentials", sf.KeychainService)
			found = true
			break
		}
	}
	assert.True(t, found, "credentials SeedFile should be present")

	require.Len(t, spec.SettingsPatches, 1)
	assert.Equal(t, store.AgentRuntimePath(sbDir), spec.SettingsPatches[0].Dir(sbDir))
	assert.NotNil(t, spec.SettingsPatches[0].Apply)
}

func TestBuildEnvSpec_ShellAgent(t *testing.T) {
	def := agent.GetAgent("shell")
	require.NotNil(t, def)

	spec := envspec.BuildEnvSpec(def)

	// Each patch must resolve to the agent's FULL StateRelPath below home-seed.
	// Set equality against a hardcoded table, so the count is implied and a
	// mis-nested or spurious dir fails by diff. Truncating to the basename would
	// mis-nest a StateDir like ".local/share/opencode" as "opencode" — exactly
	// the seatbelt bug. Forward fence: the day an agent gains both a StateDir
	// and ApplySettings (opencode has the StateDir already), its StateRelPath
	// must be added to the table.
	observedStateRelPaths := map[string]bool{}
	for _, p := range spec.SettingsPatches {
		rel, err := filepath.Rel(store.HomeSeedPath(sbDir), p.Dir(sbDir))
		require.NoError(t, err)
		observedStateRelPaths[rel] = true
		assert.NotNil(t, p.Apply)
	}

	expectedStateRelPaths := map[string]bool{
		".claude": true,
		".codex":  true,
		".gemini": true,
	}
	assert.Equal(t, expectedStateRelPaths, observedStateRelPaths,
		"shell settings patches must resolve to exactly the seeded agents' StateRelPaths")
}

func TestBuildEnvSpec_NoStateDirAgent(t *testing.T) {
	def := agent.GetAgent("aider")
	require.NotNil(t, def)

	spec := envspec.BuildEnvSpec(def)

	assert.Equal(t, "", spec.StateRelPath)
	assert.False(t, spec.HasStateDir)
	assert.Nil(t, spec.SettingsPatches)
}

func TestBuildEnvSpec_UserDefinedAgentIsMarked(t *testing.T) {
	def := &agent.Definition{Type: "diamond", UserDefined: true, APIKeyEnvVars: []string{"DIAMOND_KEY"}}

	spec := envspec.BuildEnvSpec(def)

	assert.Equal(t, "diamond", spec.AgentName)
	assert.True(t, spec.UserDefined, "a file-defined agent's marker must carry through to the EnvSpec that reaches DescribeInjectedCredentials")
}

func TestBuildEnvSpec_SeedFileMapping(t *testing.T) {
	def := agent.GetAgent("claude")
	spec := envspec.BuildEnvSpec(def)

	// Verify all seed files are mapped (count must match)
	assert.Equal(t, len(def.SeedFiles), len(spec.SeedFiles))

	// SeedFiles field is []envsetup.SeedFile — verified by the field type in EnvSpec.
}
