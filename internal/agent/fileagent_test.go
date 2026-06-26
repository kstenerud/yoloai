// ABOUTME: Tests for file-defined agent loading (LoadFileAgents, RegisterFileAgents)
// ABOUTME: and the FileAgentSpec→Definition conversion path.
package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile writes content to a file in dir, creating the dir if needed.
func writeAgentFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0755))                                        //nolint:gosec // G301: test directory
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)) //nolint:gosec // G306: test file
}

func TestLoadFileAgents_MissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agents-does-not-exist")
	defs, err := LoadFileAgents(dir)
	require.NoError(t, err)
	assert.Nil(t, defs)
}

func TestLoadFileAgents_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	defs, err := LoadFileAgents(dir)
	require.NoError(t, err)
	assert.Nil(t, defs)
}

func TestLoadFileAgents_ValidMinimal(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "mytool.yaml", `
type: mytool
interactive_cmd: mytool --yes
`)
	defs, err := LoadFileAgents(dir)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	def := defs[0]
	assert.Equal(t, AgentType("mytool"), def.Type)
	assert.Equal(t, "mytool --yes", def.InteractiveCmd)
	assert.Equal(t, PromptModeInteractive, def.PromptMode, "default prompt mode should be interactive")
}

func TestLoadFileAgents_ValidFull(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "mytool.yaml", `
type: my-tool2
description: My custom AI tool
interactive_cmd: mytool2 --interactive
headless_cmd: mytool2 --prompt "PROMPT"
prompt_mode: headless
api_key_env_vars: [MY_TOOL_API_KEY, MY_TOOL_ALT_KEY]
auth_hint_env_vars: [LOCAL_ENDPOINT]
auth_optional: true
seed_files:
  - host_path: ~/.mytool/config.json
    target_path: config.json
    auth_only: false
    home_dir: false
  - host_path: ~/.mytool/auth.json
    target_path: auth.json
    auth_only: true
    home_dir: false
state_dir: /home/yoloai/.mytool2/
submit_sequence: Enter
startup_delay_ms: 2500
idle:
  ready_pattern: "$ "
  context_signal: true
  wchan_applicable: true
model_flag: --model
model_aliases:
  fast: mytool2-fast
  slow: mytool2-slow
model_prefixes:
  OLLAMA_API_BASE: "ollama/"
network_allowlist:
  - api.mytool2.com
  - auth.mytool2.com
context_file: AGENTS.md
agent_files_exclude: ["auth.json", "sessions/"]
`)
	defs, err := LoadFileAgents(dir)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	def := defs[0]

	assert.Equal(t, AgentType("my-tool2"), def.Type)
	assert.Equal(t, "My custom AI tool", def.Description)
	assert.Equal(t, "mytool2 --interactive", def.InteractiveCmd)
	assert.Equal(t, `mytool2 --prompt "PROMPT"`, def.HeadlessCmd)
	assert.Equal(t, PromptModeHeadless, def.PromptMode)
	assert.Equal(t, []string{"MY_TOOL_API_KEY", "MY_TOOL_ALT_KEY"}, def.APIKeyEnvVars)
	assert.Equal(t, []string{"LOCAL_ENDPOINT"}, def.AuthHintEnvVars)
	assert.True(t, def.AuthOptional)
	require.Len(t, def.SeedFiles, 2)
	assert.Equal(t, "~/.mytool/config.json", def.SeedFiles[0].HostPath)
	assert.Equal(t, "config.json", def.SeedFiles[0].TargetPath)
	assert.False(t, def.SeedFiles[0].AuthOnly)
	assert.False(t, def.SeedFiles[0].HomeDir)
	assert.Equal(t, "~/.mytool/auth.json", def.SeedFiles[1].HostPath)
	assert.Equal(t, "auth.json", def.SeedFiles[1].TargetPath)
	assert.True(t, def.SeedFiles[1].AuthOnly)
	assert.Equal(t, "/home/yoloai/.mytool2/", def.StateDir)
	assert.Equal(t, "Enter", def.SubmitSequence)
	assert.Equal(t, 2500*time.Millisecond, def.StartupDelay)
	assert.Equal(t, "$ ", def.Idle.ReadyPattern)
	assert.True(t, def.Idle.ContextSignal)
	assert.True(t, def.Idle.WchanApplicable)
	assert.False(t, def.Idle.Hook, "Hook cannot be set via YAML; must stay false")
	assert.Equal(t, "--model", def.ModelFlag)
	assert.Equal(t, map[string]string{"fast": "mytool2-fast", "slow": "mytool2-slow"}, def.ModelAliases)
	assert.Equal(t, map[string]string{"OLLAMA_API_BASE": "ollama/"}, def.ModelPrefixes)
	assert.Equal(t, []string{"api.mytool2.com", "auth.mytool2.com"}, def.NetworkAllowlist)
	assert.Equal(t, "AGENTS.md", def.ContextFile)
	assert.Equal(t, []string{"auth.json", "sessions/"}, def.AgentFilesExclude)

	// Code-only fields must stay zero/nil.
	assert.Nil(t, def.ApplySettings, "ApplySettings must be nil for file-defined agents")
	assert.False(t, def.ShortLivedOAuthWarning)
	assert.False(t, def.SeedsAllAgents)
}

func TestLoadFileAgents_YmlExtension(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "mytool.yml", `
type: mytool-yml
interactive_cmd: mytool-yml --run
`)
	defs, err := LoadFileAgents(dir)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	assert.Equal(t, AgentType("mytool-yml"), defs[0].Type)
}

func TestLoadFileAgents_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "alpha.yaml", `
type: alpha
interactive_cmd: alpha
`)
	writeAgentFile(t, dir, "beta.yaml", `
type: beta
headless_cmd: beta --prompt "PROMPT"
`)
	defs, err := LoadFileAgents(dir)
	require.NoError(t, err)
	require.Len(t, defs, 2)
	types := map[string]bool{}
	for _, d := range defs {
		types[string(d.Type)] = true
	}
	assert.True(t, types["alpha"])
	assert.True(t, types["beta"])
}

func TestLoadFileAgents_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "bad.yaml", `type: [unclosed`)
	_, err := LoadFileAgents(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad.yaml")
}

func TestLoadFileAgents_MissingType(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "notype.yaml", `
interactive_cmd: sometool
`)
	_, err := LoadFileAgents(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "notype.yaml")
	assert.Contains(t, err.Error(), "type is required")
}

func TestLoadFileAgents_InvalidTypeName(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "badname.yaml", `
type: "My Agent"
interactive_cmd: sometool
`)
	_, err := LoadFileAgents(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "My Agent")
	assert.Contains(t, err.Error(), "kebab-case")
}

func TestLoadFileAgents_MissingBothCmds(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "nocmd.yaml", `
type: nocmd
description: no commands
`)
	_, err := LoadFileAgents(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nocmd.yaml")
	assert.Contains(t, err.Error(), "interactive_cmd or headless_cmd")
}

func TestLoadFileAgents_InvalidPromptMode(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "badmode.yaml", `
type: badmode
interactive_cmd: sometool
prompt_mode: invalid
`)
	_, err := LoadFileAgents(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompt_mode")
}

func TestLoadFileAgents_BuiltInNameCollision(t *testing.T) {
	dir := t.TempDir()
	// "claude" is a reserved built-in name.
	writeAgentFile(t, dir, "claude.yaml", `
type: claude
interactive_cmd: fake-claude
`)
	_, err := LoadFileAgents(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude")
	assert.Contains(t, err.Error(), "reserved built-in")
}

func TestLoadFileAgents_BuiltInNameCollision_Shell(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "shell.yaml", `
type: shell
interactive_cmd: bash
`)
	_, err := LoadFileAgents(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved built-in")
}

func TestRegisterFileAgents_RegistersAndRetrieves(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "myagent.yaml", `
type: myagent
description: My test agent
interactive_cmd: myagent --run
`)
	// Clean up: remove the registered agent after the test so it doesn't bleed
	// into sibling tests (package-global registry).
	t.Cleanup(func() {
		agentsMu.Lock()
		delete(agents, "myagent")
		agentsMu.Unlock()
	})

	err := RegisterFileAgents(dir)
	require.NoError(t, err)

	def := GetAgent("myagent")
	require.NotNil(t, def)
	assert.Equal(t, AgentType("myagent"), def.Type)
	assert.Equal(t, "My test agent", def.Description)
	assert.Equal(t, "myagent --run", def.InteractiveCmd)
}

func TestRegisterFileAgents_MissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	err := RegisterFileAgents(dir)
	require.NoError(t, err)
}

func TestRegisterFileAgents_BuiltInCollision(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "aider.yaml", `
type: aider
interactive_cmd: fake-aider
`)
	err := RegisterFileAgents(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aider")
	assert.Contains(t, err.Error(), "reserved built-in")
	// Original built-in must be intact.
	def := GetAgent("aider")
	require.NotNil(t, def)
	assert.Contains(t, def.InteractiveCmd, "aider --yes-always")
}

func TestRegisterFileAgents_IdempotentFileAgent(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "idempotent.yaml", `
type: idempotent
interactive_cmd: version1
`)
	t.Cleanup(func() {
		agentsMu.Lock()
		delete(agents, "idempotent")
		agentsMu.Unlock()
	})

	require.NoError(t, RegisterFileAgents(dir))
	assert.Equal(t, "version1", GetAgent("idempotent").InteractiveCmd)

	// Overwrite the file with a new command and re-register.
	writeAgentFile(t, dir, "idempotent.yaml", `
type: idempotent
interactive_cmd: version2
`)
	require.NoError(t, RegisterFileAgents(dir))
	assert.Equal(t, "version2", GetAgent("idempotent").InteractiveCmd, "re-registration should update the entry")
}

func TestBuiltInAgentNames_FrozenAfterInit(t *testing.T) {
	// builtInAgentNames must include all known built-ins.
	for _, name := range []string{"aider", "claude", "gemini", "codex", "opencode", "test", "idle", "shell"} {
		assert.True(t, builtInAgentNames[name], "built-in name %q should be in frozen set", name)
	}
}
