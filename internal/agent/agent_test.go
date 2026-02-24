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
	assert.Equal(t, "claude --dangerously-skip-permissions", def.InteractiveCmd)
	assert.Contains(t, def.HeadlessCmd, "claude -p")
	assert.Equal(t, PromptModeInteractive, def.PromptMode)
	assert.Equal(t, []string{"ANTHROPIC_API_KEY"}, def.APIKeyEnvVars)
	assert.Equal(t, "/home/yoloai/.claude/", def.StateDir)
	assert.Equal(t, "Enter Enter", def.SubmitSequence)
	assert.Equal(t, 3*time.Second, def.StartupDelay)
	assert.Equal(t, "--model", def.ModelFlag)
	assert.Equal(t, "claude-sonnet-4-latest", def.ModelAliases["sonnet"])
	assert.Equal(t, "claude-opus-4-latest", def.ModelAliases["opus"])
	assert.Equal(t, "claude-haiku-4-latest", def.ModelAliases["haiku"])
}

func TestGetAgent_Test(t *testing.T) {
	def := GetAgent("test")
	require.NotNil(t, def)

	assert.Equal(t, "test", def.Name)
	assert.Equal(t, "bash", def.InteractiveCmd)
	assert.Contains(t, def.HeadlessCmd, "sh -c")
	assert.Equal(t, PromptModeHeadless, def.PromptMode)
	assert.Empty(t, def.APIKeyEnvVars)
	assert.NotNil(t, def.APIKeyEnvVars, "should be empty slice, not nil")
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
