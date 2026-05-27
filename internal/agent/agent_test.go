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
	assert.Equal(t, "> $", def.Idle.ReadyPattern)
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
	assert.Equal(t, []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}, def.APIKeyEnvVars)
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
	assert.Equal(t, "claude-sonnet-4-6", def.ModelAliases["sonnet"])
	assert.Equal(t, "claude-opus-4-6", def.ModelAliases["opus"])
	assert.Equal(t, "claude-haiku-4-5-20251001", def.ModelAliases["haiku"])
	assert.Equal(t, []string{"api.anthropic.com", "claude.ai", "platform.claude.com", "statsig.anthropic.com", "sentry.io"}, def.NetworkAllowlist)
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
	assert.Equal(t, "Type your message", def.Idle.ReadyPattern)
	assert.Equal(t, "--model", def.ModelFlag)
	assert.Equal(t, "gemini-2.5-pro", def.ModelAliases["pro"])
	assert.Equal(t, "gemini-2.5-flash", def.ModelAliases["flash"])
	assert.Equal(t, "gemini-3.1-pro-preview", def.ModelAliases["preview-pro"])
	assert.Equal(t, "gemini-3-flash-preview", def.ModelAliases["preview-flash"])
	assert.Equal(t, []string{"generativelanguage.googleapis.com", "cloudcode-pa.googleapis.com", "oauth2.googleapis.com"}, def.NetworkAllowlist)
}

func TestGetAgent_OpenCode(t *testing.T) {
	def := GetAgent("opencode")
	require.NotNil(t, def)

	assert.Equal(t, "opencode", def.Name)
	assert.NotEmpty(t, def.Description)
	assert.Equal(t, "opencode", def.InteractiveCmd)
	assert.Contains(t, def.HeadlessCmd, "opencode run")
	assert.Equal(t, PromptModeHeadless, def.PromptMode)
	assert.Equal(t, []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GROQ_API_KEY", "OPENROUTER_API_KEY", "XAI_API_KEY"}, def.APIKeyEnvVars)
	assert.Equal(t, []string{"GITHUB_TOKEN", "LOCAL_ENDPOINT", "AZURE_OPENAI_ENDPOINT", "AWS_ACCESS_KEY_ID", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "VERTEXAI_PROJECT"}, def.AuthHintEnvVars)
	assert.True(t, def.AuthOptional)
	require.Len(t, def.SeedFiles, 5)
	assert.Equal(t, "~/.local/share/opencode/auth.json", def.SeedFiles[0].HostPath)
	assert.Equal(t, "auth.json", def.SeedFiles[0].TargetPath)
	assert.True(t, def.SeedFiles[0].AuthOnly)
	assert.Equal(t, "~/.opencode.json", def.SeedFiles[1].HostPath)
	assert.Equal(t, ".opencode.json", def.SeedFiles[1].TargetPath)
	assert.True(t, def.SeedFiles[1].AuthOnly)
	assert.True(t, def.SeedFiles[1].HomeDir)
	assert.Equal(t, "~/.config/github-copilot/hosts.json", def.SeedFiles[2].HostPath)
	assert.Equal(t, ".config/github-copilot/hosts.json", def.SeedFiles[2].TargetPath)
	assert.True(t, def.SeedFiles[2].AuthOnly)
	assert.True(t, def.SeedFiles[2].HomeDir)
	assert.Equal(t, "~/.config/github-copilot/apps.json", def.SeedFiles[3].HostPath)
	assert.Equal(t, ".config/github-copilot/apps.json", def.SeedFiles[3].TargetPath)
	assert.True(t, def.SeedFiles[3].AuthOnly)
	assert.True(t, def.SeedFiles[3].HomeDir)
	assert.Equal(t, "~/.config/opencode/.opencode.json", def.SeedFiles[4].HostPath)
	assert.Equal(t, ".config/opencode/.opencode.json", def.SeedFiles[4].TargetPath)
	assert.True(t, def.SeedFiles[4].HomeDir)
	assert.False(t, def.SeedFiles[4].AuthOnly)
	assert.Equal(t, "/home/yoloai/.local/share/opencode/", def.StateDir)
	assert.Equal(t, "Enter", def.SubmitSequence)
	assert.Equal(t, 3*time.Second, def.StartupDelay)
	assert.Equal(t, "", def.Idle.ReadyPattern)
	assert.Equal(t, "--model", def.ModelFlag)
	assert.Equal(t, "anthropic/claude-sonnet-4-5-latest", def.ModelAliases["sonnet"])
	assert.Equal(t, "anthropic/claude-opus-4-latest", def.ModelAliases["opus"])
	assert.Equal(t, "anthropic/claude-haiku-4-5-latest", def.ModelAliases["haiku"])
	assert.Equal(t, []string{"api.anthropic.com", "api.openai.com", "generativelanguage.googleapis.com", "api.github.com", "api.githubcopilot.com"}, def.NetworkAllowlist)
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
	assert.Equal(t, "›", def.Idle.ReadyPattern)
	assert.Equal(t, "--model", def.ModelFlag)
	assert.Equal(t, "gpt-5.3-codex", def.ModelAliases["default"])
	assert.Equal(t, "gpt-5.3-codex-spark", def.ModelAliases["spark"])
	assert.Equal(t, "codex-mini-latest", def.ModelAliases["mini"])
	assert.Equal(t, []string{"api.openai.com"}, def.NetworkAllowlist)
}

func TestAllAgentNames(t *testing.T) {
	names := AllAgentNames()
	assert.Equal(t, []string{"aider", "claude", "codex", "gemini", "idle", "opencode", "shell", "test"}, names)
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
	assert.Contains(t, def.APIKeyEnvVars, "GROQ_API_KEY")
	assert.Contains(t, def.APIKeyEnvVars, "XAI_API_KEY")

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
	assert.Contains(t, targetPaths, ".opencode.json")                    // was already HomeDir, unchanged
	assert.Contains(t, targetPaths, ".config/github-copilot/hosts.json") // was already HomeDir, unchanged
	assert.Contains(t, targetPaths, ".config/github-copilot/apps.json")  // was already HomeDir, unchanged
	assert.Contains(t, targetPaths, ".config/opencode/.opencode.json")   // was already HomeDir, unchanged

	// Each seed file should have OwnerAPIKeys set
	for _, sf := range def.SeedFiles {
		assert.NotNil(t, sf.OwnerAPIKeys, "shell agent seed file %s should have OwnerAPIKeys set", sf.TargetPath)
	}

	// Should have network allowlist from all real agents
	assert.Contains(t, def.NetworkAllowlist, "api.anthropic.com")
	assert.Contains(t, def.NetworkAllowlist, "generativelanguage.googleapis.com")
	assert.Contains(t, def.NetworkAllowlist, "api.openai.com")
}

func TestStateRelPath(t *testing.T) {
	tests := []struct {
		agent string
		want  string
	}{
		{"claude", ".claude"},
		{"gemini", ".gemini"},
		{"codex", ".codex"},
		{"opencode", ".local/share/opencode"},
		{"aider", ""}, // no StateDir
		{"test", ""},  // no StateDir
		{"shell", ""}, // no StateDir
	}
	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			def := GetAgent(tt.agent)
			require.NotNil(t, def)
			assert.Equal(t, tt.want, def.StateRelPath())
		})
	}
}

func TestGetAgent_Unknown(t *testing.T) {
	assert.Nil(t, GetAgent("unknown"))
	assert.Nil(t, GetAgent(""))
}

func TestApplySettings_Claude(t *testing.T) {
	def := GetAgent("claude")
	require.NotNil(t, def)
	require.NotNil(t, def.ApplySettings, "claude should have ApplySettings set")

	settings := map[string]any{}
	def.ApplySettings(settings)

	assert.Equal(t, true, settings["skipDangerousModePermissionPrompt"])
	assert.Equal(t, map[string]any{"enabled": false}, settings["sandbox"])
	assert.Equal(t, "terminal_bell", settings["preferredNotifChannel"])

	// Verify idle hooks were injected
	hooks, ok := settings["hooks"].(map[string]any)
	require.True(t, ok, "hooks should be a map")
	assert.NotNil(t, hooks["Stop"], "Stop hook should be set")
	assert.NotNil(t, hooks["PreToolUse"], "PreToolUse hook should be set")
	assert.NotNil(t, hooks["UserPromptSubmit"], "UserPromptSubmit hook should be set")
}

func TestApplySettings_ClaudePreservesExistingHooks(t *testing.T) {
	def := GetAgent("claude")
	require.NotNil(t, def)

	existingHook := map[string]any{"type": "command", "command": "echo existing"}
	existingGroup := map[string]any{"hooks": []any{existingHook}}
	settings := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{existingGroup},
		},
	}
	def.ApplySettings(settings)

	hooks := settings["hooks"].(map[string]any)
	stopHooks := hooks["Stop"].([]any)
	assert.Len(t, stopHooks, 2, "should preserve existing hook and append idle hook")
}

func TestApplySettings_Gemini(t *testing.T) {
	def := GetAgent("gemini")
	require.NotNil(t, def)
	require.NotNil(t, def.ApplySettings, "gemini should have ApplySettings set")

	settings := map[string]any{}
	def.ApplySettings(settings)

	security, ok := settings["security"].(map[string]any)
	require.True(t, ok, "security should be a map")
	folderTrust, ok := security["folderTrust"].(map[string]any)
	require.True(t, ok, "folderTrust should be a map")
	assert.Equal(t, false, folderTrust["enabled"])
}

func TestApplySettings_GeminiPreservesExistingSecurityFields(t *testing.T) {
	def := GetAgent("gemini")
	require.NotNil(t, def)

	settings := map[string]any{
		"security": map[string]any{"auth": map[string]any{"selectedType": "oauth"}},
	}
	def.ApplySettings(settings)

	security := settings["security"].(map[string]any)
	// Existing field should be preserved
	assert.Equal(t, map[string]any{"selectedType": "oauth"}, security["auth"])
	// folderTrust should be added
	assert.Equal(t, map[string]any{"enabled": false}, security["folderTrust"])
}

func TestApplySettings_OtherAgentsNil(t *testing.T) {
	for _, name := range []string{"aider", "codex", "opencode", "test", "idle"} {
		def := GetAgent(name)
		require.NotNil(t, def, "agent %q should exist", name)
		assert.Nil(t, def.ApplySettings, "agent %q should have nil ApplySettings", name)
	}
}

func TestShortLivedOAuthWarning(t *testing.T) {
	assert.True(t, GetAgent("claude").ShortLivedOAuthWarning, "claude should have ShortLivedOAuthWarning=true")

	for _, name := range []string{"aider", "gemini", "codex", "opencode", "test", "idle", "shell"} {
		def := GetAgent(name)
		require.NotNil(t, def, "agent %q should exist", name)
		assert.False(t, def.ShortLivedOAuthWarning, "agent %q should have ShortLivedOAuthWarning=false", name)
	}
}

func TestSeedsAllAgents(t *testing.T) {
	assert.True(t, GetAgent("shell").SeedsAllAgents, "shell should have SeedsAllAgents=true")

	for _, name := range []string{"aider", "claude", "gemini", "codex", "opencode", "test", "idle"} {
		def := GetAgent(name)
		require.NotNil(t, def, "agent %q should exist", name)
		assert.False(t, def.SeedsAllAgents, "agent %q should have SeedsAllAgents=false", name)
	}
}
