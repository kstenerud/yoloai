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

	// Count real agents that have StateDir + ApplySettings
	realNames := agent.RealAgents()
	expectedCount := 0
	for _, name := range realNames {
		d := agent.GetAgent(name)
		if d.StateDir != "" && d.ApplySettings != nil {
			expectedCount++
		}
	}

	assert.Len(t, spec.SettingsPatches, expectedCount)

	// Each patch should be under home-seed/
	for _, p := range spec.SettingsPatches {
		assert.Equal(t, store.HomeSeedPath(sbDir), filepath.Dir(p.Dir(sbDir)),
			"shell agent patches must target home-seed subdirs")
		assert.NotNil(t, p.Apply)
	}
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
