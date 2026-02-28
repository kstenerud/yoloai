package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAgent_Claude(t *testing.T) {
	def := GetAgent("claude")
	require.NotNil(t, def)

	assert.Equal(t, "claude", def.Name)
	assert.NotEmpty(t, def.Description)
	assert.Equal(t, "claude --dangerously-skip-permissions", def.InteractiveCmd)
	assert.Contains(t, def.HeadlessCmd, "claude -p")
	assert.Equal(t, PromptModeInteractive, def.PromptMode)
	assert.Equal(t, []string{"ANTHROPIC_API_KEY"}, def.APIKeyEnvVars)
	require.Len(t, def.SeedFiles, 3)
	assert.Equal(t, "~/.claude/.credentials.json", def.SeedFiles[0].HostPath)
	assert.Equal(t, ".credentials.json", def.SeedFiles[0].TargetPath)
	assert.True(t, def.SeedFiles[0].AuthOnly)
	assert.Equal(t, "Claude Code-credentials", def.SeedFiles[0].KeychainService)
	assert.Equal(t, "~/.claude/settings.json", def.SeedFiles[1].HostPath)
	assert.Equal(t, "settings.json", def.SeedFiles[1].TargetPath)
	assert.False(t, def.SeedFiles[1].AuthOnly)
	assert.Equal(t, "~/.claude.json", def.SeedFiles[2].HostPath)
	assert.Equal(t, ".claude.json", def.SeedFiles[2].TargetPath)
	assert.True(t, def.SeedFiles[2].HomeDir)
	assert.Equal(t, "/home/yoloai/.claude/", def.StateDir)
	assert.Equal(t, "Enter Enter", def.SubmitSequence)
	assert.Equal(t, 3*time.Second, def.StartupDelay)
	assert.Equal(t, "--model", def.ModelFlag)
	assert.Equal(t, "claude-sonnet-4-latest", def.ModelAliases["sonnet"])
	assert.Equal(t, "claude-opus-4-latest", def.ModelAliases["opus"])
	assert.Equal(t, "claude-haiku-4-latest", def.ModelAliases["haiku"])
}

func TestGetAgent_Gemini(t *testing.T) {
	def := GetAgent("gemini")
	require.NotNil(t, def)

	assert.Equal(t, "gemini", def.Name)
	assert.NotEmpty(t, def.Description)
	assert.Equal(t, "gemini --yolo", def.InteractiveCmd)
	assert.Contains(t, def.HeadlessCmd, "gemini -p")
	assert.Equal(t, PromptModeInteractive, def.PromptMode)
	assert.Equal(t, []string{"GEMINI_API_KEY"}, def.APIKeyEnvVars)
	require.Len(t, def.SeedFiles, 1)
	assert.Equal(t, "~/.gemini/settings.json", def.SeedFiles[0].HostPath)
	assert.Equal(t, "settings.json", def.SeedFiles[0].TargetPath)
	assert.False(t, def.SeedFiles[0].AuthOnly)
	assert.Equal(t, "/home/yoloai/.gemini/", def.StateDir)
	assert.Equal(t, "Enter", def.SubmitSequence)
	assert.Equal(t, 3*time.Second, def.StartupDelay)
	assert.Equal(t, "", def.ReadyPattern)
	assert.Equal(t, "--model", def.ModelFlag)
	assert.Equal(t, "gemini-2.5-pro", def.ModelAliases["pro"])
	assert.Equal(t, "gemini-2.5-flash", def.ModelAliases["flash"])
}

func TestAllAgentNames(t *testing.T) {
	names := AllAgentNames()
	assert.Equal(t, []string{"claude", "gemini", "test"}, names)
}

func TestGetAgent_Test(t *testing.T) {
	def := GetAgent("test")
	require.NotNil(t, def)

	assert.Equal(t, "test", def.Name)
	assert.NotEmpty(t, def.Description)
	assert.Equal(t, "bash", def.InteractiveCmd)
	assert.Contains(t, def.HeadlessCmd, "sh -c")
	assert.Equal(t, PromptModeHeadless, def.PromptMode)
	assert.Empty(t, def.APIKeyEnvVars)
	assert.NotNil(t, def.APIKeyEnvVars, "should be empty slice, not nil")
	assert.Empty(t, def.SeedFiles)
	assert.Equal(t, "", def.StateDir)
	assert.Equal(t, "Enter", def.SubmitSequence)
	assert.Equal(t, time.Duration(0), def.StartupDelay)
	assert.Equal(t, "", def.ModelFlag)
	assert.Nil(t, def.ModelAliases)
}

func TestGetAgent_Unknown(t *testing.T) {
	assert.Nil(t, GetAgent("unknown"))
	assert.Nil(t, GetAgent(""))
	assert.Nil(t, GetAgent("codex"))
}
