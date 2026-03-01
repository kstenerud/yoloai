package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAgent_Aider(t *testing.T) {
	def := GetAgent("aider")
	require.NotNil(t, def)

	assert.Equal(t, "aider", def.Name)
	assert.NotEmpty(t, def.Description)
	assert.Equal(t, "aider --yes-always", def.InteractiveCmd)
	assert.Contains(t, def.HeadlessCmd, "aider --message")
	assert.Equal(t, PromptModeInteractive, def.PromptMode)
	assert.Equal(t, []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "DEEPSEEK_API_KEY", "OPENROUTER_API_KEY"}, def.APIKeyEnvVars)
	require.Len(t, def.SeedFiles, 1)
	assert.Equal(t, "~/.aider.conf.yml", def.SeedFiles[0].HostPath)
	assert.Equal(t, ".aider.conf.yml", def.SeedFiles[0].TargetPath)
	assert.True(t, def.SeedFiles[0].HomeDir)
	assert.False(t, def.SeedFiles[0].AuthOnly)
	assert.Equal(t, "", def.StateDir)
	assert.Equal(t, "Enter", def.SubmitSequence)
	assert.Equal(t, 3*time.Second, def.StartupDelay)
	assert.Equal(t, "> $", def.ReadyPattern)
	assert.Equal(t, "--model", def.ModelFlag)
	assert.Equal(t, "sonnet", def.ModelAliases["sonnet"])
	assert.Equal(t, "opus", def.ModelAliases["opus"])
	assert.Equal(t, "haiku", def.ModelAliases["haiku"])
	assert.Equal(t, "deepseek", def.ModelAliases["deepseek"])
	assert.Equal(t, "flash", def.ModelAliases["flash"])
	assert.Nil(t, def.NetworkAllowlist)
}

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
	assert.Equal(t, []string{"api.anthropic.com", "statsig.anthropic.com", "sentry.io"}, def.NetworkAllowlist)
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
	require.Len(t, def.SeedFiles, 3)
	assert.Equal(t, "~/.gemini/oauth_creds.json", def.SeedFiles[0].HostPath)
	assert.Equal(t, "oauth_creds.json", def.SeedFiles[0].TargetPath)
	assert.True(t, def.SeedFiles[0].AuthOnly)
	assert.Equal(t, "~/.gemini/google_accounts.json", def.SeedFiles[1].HostPath)
	assert.Equal(t, "google_accounts.json", def.SeedFiles[1].TargetPath)
	assert.True(t, def.SeedFiles[1].AuthOnly)
	assert.Equal(t, "~/.gemini/settings.json", def.SeedFiles[2].HostPath)
	assert.Equal(t, "settings.json", def.SeedFiles[2].TargetPath)
	assert.False(t, def.SeedFiles[2].AuthOnly)
	assert.Equal(t, "/home/yoloai/.gemini/", def.StateDir)
	assert.Equal(t, "Enter", def.SubmitSequence)
	assert.Equal(t, 3*time.Second, def.StartupDelay)
	assert.Equal(t, "", def.ReadyPattern)
	assert.Equal(t, "--model", def.ModelFlag)
	assert.Equal(t, "gemini-2.5-pro", def.ModelAliases["pro"])
	assert.Equal(t, "gemini-2.5-flash", def.ModelAliases["flash"])
	assert.Equal(t, []string{"generativelanguage.googleapis.com", "cloudcode-pa.googleapis.com"}, def.NetworkAllowlist)
}

func TestGetAgent_OpenCode(t *testing.T) {
	def := GetAgent("opencode")
	require.NotNil(t, def)

	assert.Equal(t, "opencode", def.Name)
	assert.NotEmpty(t, def.Description)
	assert.Equal(t, "opencode", def.InteractiveCmd)
	assert.Contains(t, def.HeadlessCmd, "opencode run")
	assert.Equal(t, PromptModeHeadless, def.PromptMode)
	assert.Equal(t, []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY"}, def.APIKeyEnvVars)
	require.Len(t, def.SeedFiles, 2)
	assert.Equal(t, "~/.local/share/opencode/auth.json", def.SeedFiles[0].HostPath)
	assert.Equal(t, "auth.json", def.SeedFiles[0].TargetPath)
	assert.True(t, def.SeedFiles[0].AuthOnly)
	assert.Equal(t, "~/.config/opencode/config.json", def.SeedFiles[1].HostPath)
	assert.Equal(t, "config.json", def.SeedFiles[1].TargetPath)
	assert.True(t, def.SeedFiles[1].HomeDir)
	assert.False(t, def.SeedFiles[1].AuthOnly)
	assert.Equal(t, "/home/yoloai/.local/share/opencode/", def.StateDir)
	assert.Equal(t, "Enter", def.SubmitSequence)
	assert.Equal(t, 3*time.Second, def.StartupDelay)
	assert.Equal(t, "", def.ReadyPattern)
	assert.Equal(t, "--model", def.ModelFlag)
	assert.Equal(t, "anthropic/claude-sonnet-4-5-latest", def.ModelAliases["sonnet"])
	assert.Equal(t, "anthropic/claude-opus-4-latest", def.ModelAliases["opus"])
	assert.Equal(t, "anthropic/claude-haiku-4-5-latest", def.ModelAliases["haiku"])
}

func TestGetAgent_Codex(t *testing.T) {
	def := GetAgent("codex")
	require.NotNil(t, def)

	assert.Equal(t, "codex", def.Name)
	assert.NotEmpty(t, def.Description)
	assert.Equal(t, "codex --dangerously-bypass-approvals-and-sandbox", def.InteractiveCmd)
	assert.Contains(t, def.HeadlessCmd, "codex exec")
	assert.Equal(t, PromptModeInteractive, def.PromptMode)
	assert.Equal(t, []string{"CODEX_API_KEY", "OPENAI_API_KEY"}, def.APIKeyEnvVars)
	require.Len(t, def.SeedFiles, 2)
	assert.Equal(t, "~/.codex/auth.json", def.SeedFiles[0].HostPath)
	assert.Equal(t, "auth.json", def.SeedFiles[0].TargetPath)
	assert.True(t, def.SeedFiles[0].AuthOnly)
	assert.Equal(t, "~/.codex/config.toml", def.SeedFiles[1].HostPath)
	assert.Equal(t, "config.toml", def.SeedFiles[1].TargetPath)
	assert.False(t, def.SeedFiles[1].AuthOnly)
	assert.Equal(t, "/home/yoloai/.codex/", def.StateDir)
	assert.Equal(t, "Enter", def.SubmitSequence)
	assert.Equal(t, 3*time.Second, def.StartupDelay)
	assert.Equal(t, "›", def.ReadyPattern)
	assert.Equal(t, "--model", def.ModelFlag)
	assert.Nil(t, def.ModelAliases)
	assert.Equal(t, []string{"api.openai.com"}, def.NetworkAllowlist)
}

func TestAllAgentNames(t *testing.T) {
	names := AllAgentNames()
	assert.Equal(t, []string{"aider", "claude", "codex", "gemini", "opencode", "shell", "test"}, names)
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
	assert.Nil(t, def.NetworkAllowlist)
}

func TestRealAgents(t *testing.T) {
	names := RealAgents()
	assert.Equal(t, []string{"aider", "claude", "codex", "gemini", "opencode"}, names)
}

func TestGetAgent_Shell(t *testing.T) {
	def := GetAgent("shell")
	require.NotNil(t, def)

	assert.Equal(t, "shell", def.Name)
	assert.NotEmpty(t, def.Description)
	assert.Contains(t, def.InteractiveCmd, "yolo-claude")
	assert.Contains(t, def.InteractiveCmd, "yolo-codex")
	assert.Contains(t, def.InteractiveCmd, "yolo-gemini")
	assert.Contains(t, def.InteractiveCmd, "yolo-aider")
	assert.Contains(t, def.InteractiveCmd, "yolo-opencode")
	assert.Contains(t, def.HeadlessCmd, "sh -c")
	assert.Equal(t, PromptModeHeadless, def.PromptMode)
	assert.Equal(t, "", def.StateDir)
	assert.Equal(t, "Enter", def.SubmitSequence)
	assert.Equal(t, time.Duration(0), def.StartupDelay)
	assert.Equal(t, "", def.ModelFlag)
	assert.Nil(t, def.ModelAliases)

	// Should have API keys from all real agents (deduplicated)
	assert.Contains(t, def.APIKeyEnvVars, "ANTHROPIC_API_KEY")
	assert.Contains(t, def.APIKeyEnvVars, "GEMINI_API_KEY")
	assert.Contains(t, def.APIKeyEnvVars, "CODEX_API_KEY")
	assert.Contains(t, def.APIKeyEnvVars, "OPENAI_API_KEY")
	assert.Contains(t, def.APIKeyEnvVars, "DEEPSEEK_API_KEY")
	assert.Contains(t, def.APIKeyEnvVars, "OPENROUTER_API_KEY")

	// Should have seed files from all real agents
	assert.NotEmpty(t, def.SeedFiles)

	// All seed files should be HomeDir=true
	for _, sf := range def.SeedFiles {
		assert.True(t, sf.HomeDir, "shell agent seed file %s should have HomeDir=true", sf.TargetPath)
	}

	// Check that non-HomeDir files from real agents got remapped with dir prefix
	var targetPaths []string
	for _, sf := range def.SeedFiles {
		targetPaths = append(targetPaths, sf.TargetPath)
	}
	assert.Contains(t, targetPaths, ".claude/.credentials.json")
	assert.Contains(t, targetPaths, ".claude/settings.json")
	assert.Contains(t, targetPaths, ".claude.json") // was already HomeDir, unchanged
	assert.Contains(t, targetPaths, ".codex/auth.json")
	assert.Contains(t, targetPaths, ".codex/config.toml")
	assert.Contains(t, targetPaths, ".gemini/oauth_creds.json")
	assert.Contains(t, targetPaths, ".gemini/settings.json")
	assert.Contains(t, targetPaths, ".aider.conf.yml") // was already HomeDir, unchanged
	assert.Contains(t, targetPaths, "opencode/auth.json")
	assert.Contains(t, targetPaths, "config.json") // was already HomeDir, unchanged

	// Each seed file should have OwnerAPIKeys set
	for _, sf := range def.SeedFiles {
		assert.NotNil(t, sf.OwnerAPIKeys, "shell agent seed file %s should have OwnerAPIKeys set", sf.TargetPath)
	}

	// Should have network allowlist from all real agents
	assert.Contains(t, def.NetworkAllowlist, "api.anthropic.com")
	assert.Contains(t, def.NetworkAllowlist, "generativelanguage.googleapis.com")
	assert.Contains(t, def.NetworkAllowlist, "api.openai.com")
}

func TestGetAgent_Unknown(t *testing.T) {
	assert.Nil(t, GetAgent("unknown"))
	assert.Nil(t, GetAgent(""))
}
