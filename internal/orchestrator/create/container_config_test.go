// ABOUTME: Tests for buildContainerConfig, setupWorkdir, and git baseline helpers.
package create

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/orchestrator/runtimeconfig"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/internal/workspace"
)

func TestBuildContainerConfig_LaunchPrefixStored(t *testing.T) {
	// W1a/W1b: the wrap prefix passed in is stored verbatim as the single source
	// of truth. At runtime, Python and Go restart both read agent_launch_prefix
	// (unconditionally) from runtime-config.json; the create-time source of the
	// constant is launch.AgentLaunchPrefix (no longer the runtime descriptor).
	agentDef := agent.GetAgent("claude")
	prefix := `PATH="/opt/homebrew/opt/node/bin:$PATH" `
	data, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, "claude", prefix, "default", "/tmp", false, false, nil, nil, nil, 0, nil, "test", "", "", false, "", nil, false)
	require.NoError(t, err)
	var cfg runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &cfg))
	assert.Equal(t, prefix, cfg.AgentLaunchPrefix, "launch prefix must be stored verbatim")
}

func TestBuildContainerConfig_ValidJSON(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	layout := config.NewLayout(t.TempDir())
	data, err := buildContainerConfig(layout, agentDef, "claude --dangerously-skip-permissions", "", "default+host", "/Users/test/project", false, false, nil, nil, nil, 0, nil, "test", "", "", false, "", nil, false)
	require.NoError(t, err)

	var cfg runtimeconfig.ContainerConfig
	err = json.Unmarshal(data, &cfg)
	require.NoError(t, err)

	assert.Equal(t, "claude --dangerously-skip-permissions", cfg.AgentCommand)
	assert.Equal(t, 3000, cfg.StartupDelay)
	assert.Equal(t, "Enter Enter", cfg.SubmitSequence)
	// The config's host uid/gid come from the Layout (which honors SUDO_UID under
	// sudo), not the process euid — assert against that source so the test holds
	// under `sudo make check`, where os.Getuid() is 0 but the Layout carries the
	// invoking user's uid.
	assert.Equal(t, layout.HostUID, cfg.HostUID)
	assert.Equal(t, layout.HostGID, cfg.HostGID)
	assert.Equal(t, "default+host", cfg.TmuxConf)
	assert.Equal(t, ".claude", cfg.StateDirName)
	assert.False(t, cfg.NetworkIsolated)
	assert.Empty(t, cfg.AllowedDomains)
}

func TestBuildContainerConfig_Headless(t *testing.T) {
	// A headless run (D100) sets headless and turns fall-to-shell off so the pane
	// dies on agent exit → Tier-3 done detection. The interactive default keeps
	// fall-to-shell on.
	agentDef := agent.GetAgent("claude")

	headlessData, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, `claude -p "x"`, "", "default", "/tmp", false, false, nil, nil, nil, 0, nil, "test", "", "", false, "", nil, true)
	require.NoError(t, err)
	var headless runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(headlessData, &headless))
	assert.True(t, headless.Headless)
	assert.False(t, headless.FallToShell, "headless must not fall to shell")

	interactiveData, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, "claude", "", "default", "/tmp", false, false, nil, nil, nil, 0, nil, "test", "", "", false, "", nil, false)
	require.NoError(t, err)
	var interactive runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(interactiveData, &interactive))
	assert.False(t, interactive.Headless)
	assert.True(t, interactive.FallToShell, "interactive keeps fall-to-shell on")
}

func TestAgentHasUsableAuth(t *testing.T) {
	// D101 (failsafe): headless is gated on OBSERVED auth — an agent runs headless
	// only when it has a usable key/credential, so it can't stall on a login prompt
	// in a headless pane. No special-casing any agent's headless behavior.
	noAuth := config.Layout{}.WithEnv(map[string]string{})
	withAnthropicKey := config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "x"})
	withGeminiKey := config.Layout{}.WithEnv(map[string]string{"GEMINI_API_KEY": "x"})

	// No auth → not viable, for EVERY real agent including Claude (we never bet on
	// key-less headless working — the failsafe property).
	assert.False(t, agentHasUsableAuth(agent.GetAgent("claude"), nil, noAuth), "claude with no observable auth → not viable")
	assert.False(t, agentHasUsableAuth(agent.GetAgent("gemini"), nil, noAuth), "gemini with no auth → not viable")
	// Auth present → viable.
	assert.True(t, agentHasUsableAuth(agent.GetAgent("claude"), nil, withAnthropicKey), "claude with a key → viable")
	assert.True(t, agentHasUsableAuth(agent.GetAgent("gemini"), nil, withGeminiKey), "gemini with a key → viable")
	// Utility agents need no API key → always viable.
	assert.True(t, agentHasUsableAuth(agent.GetAgent("test"), nil, noAuth), "test needs no API key → viable")
}

func TestResolveAgentParams_HeadlessDowngrade(t *testing.T) {
	// D101 (failsafe-forward): opts.Headless is a PREFERENCE. resolveAgentParams
	// computes effective headless = opts.Headless && agentHasUsableAuth(...).
	// Without observable auth the preference is silently downgraded to interactive.
	claudeDef := agent.GetAgent("claude")
	noAuthLayout := config.NewLayout(t.TempDir()).WithEnv(map[string]string{})
	withKeyLayout := config.NewLayout(t.TempDir()).WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test"})
	pr := &profileResult{}
	gcfg := &config.GlobalConfig{}
	homeDir := t.TempDir()
	prompt := "do something"

	// Headless=true but no auth → effective headless must be false (downgraded).
	opts := Options{Agent: "claude", Prompt: prompt, Headless: true}
	_, _, _, _, _, headless, err := resolveAgentParams(claudeDef, opts, pr, gcfg, homeDir, noAuthLayout, nil)
	require.NoError(t, err)
	assert.False(t, headless, "headless without observable auth must be downgraded to interactive")

	// Headless=true with auth present → stays true.
	_, _, _, _, _, headless, err = resolveAgentParams(claudeDef, opts, pr, gcfg, homeDir, withKeyLayout, nil)
	require.NoError(t, err)
	assert.True(t, headless, "headless with observable auth must stay true")
}

func TestAgentHasUsableAuth_AuthHintEnvVar(t *testing.T) {
	// An agent with an AuthHintEnvVar set in configEnv is viable even without a
	// cloud API key (e.g. aider pointing at a local Ollama instance).
	withHint := config.Layout{}.WithEnv(map[string]string{})
	configEnv := map[string]string{"OLLAMA_API_BASE": "http://localhost:11434"}
	assert.True(t, agentHasUsableAuth(agent.GetAgent("aider"), configEnv, withHint), "aider with OLLAMA_API_BASE in configEnv → viable")
}

func TestAgentHasUsableAuth_AuthFile(t *testing.T) {
	// An agent whose auth-only credential file exists on disk is viable without
	// a cloud API key. gemini uses ~/.gemini/oauth_creds.json as an AuthOnly file.
	tmpDir := t.TempDir()
	credDir := tmpDir + "/.gemini"
	require.NoError(t, os.MkdirAll(credDir, 0750))
	require.NoError(t, os.WriteFile(credDir+"/oauth_creds.json", []byte("{}"), 0600))

	// NewLayoutFor so HomeDir points at tmpDir (where ~/.gemini/... resolves correctly).
	layout := config.NewLayoutFor(tmpDir+"/.yoloai", tmpDir).WithEnv(map[string]string{})
	assert.True(t, agentHasUsableAuth(agent.GetAgent("gemini"), nil, layout), "gemini with credentials file → viable")
}

func TestBuildContainerConfig_StateDirName(t *testing.T) {
	tests := []struct {
		agent    string
		expected string
	}{
		{"claude", ".claude"},
		{"gemini", ".gemini"},
		{"test", ""},
	}
	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			agentDef := agent.GetAgent(tt.agent)
			data, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, "cmd", "", "default", "/tmp", false, false, nil, nil, nil, 0, nil, "test", "", "", false, "", nil, false)
			require.NoError(t, err)
			var cfg runtimeconfig.ContainerConfig
			require.NoError(t, json.Unmarshal(data, &cfg))
			assert.Equal(t, tt.expected, cfg.StateDirName)
		})
	}
}

func TestBuildContainerConfig_NetworkIsolated(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	domains := []string{"api.anthropic.com", "sentry.io"}
	data, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, "claude", "", "default", "/tmp", false, true, domains, nil, nil, 0, nil, "test", "", "", false, "", nil, false)
	require.NoError(t, err)

	var cfg runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	assert.True(t, cfg.NetworkIsolated)
	assert.Equal(t, domains, cfg.AllowedDomains)
}

func TestBuildContainerConfig_AutoCommitInterval(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	copyDirs := []string{"/home/user/project", "/home/user/lib"}
	data, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, "claude", "", "default", "/tmp", false, false, nil, nil, nil, 60, copyDirs, "test", "", "", false, "", nil, false)
	require.NoError(t, err)

	var cfg runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	assert.Equal(t, 60, cfg.AutoCommitInterval)
	assert.Equal(t, copyDirs, cfg.CopyDirs)
}

func TestBuildContainerConfig_AutoCommitIntervalZero(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	data, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, "claude", "", "default", "/tmp", false, false, nil, nil, nil, 0, nil, "test", "", "", false, "", nil, false)
	require.NoError(t, err)

	var cfg runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	assert.Equal(t, 0, cfg.AutoCommitInterval)
	assert.Nil(t, cfg.CopyDirs)
}

func TestGitBaseline_FreshInit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")

	sha, err := git.NewTestHostWithEnv(testutil.GitEnv()).Baseline(context.Background(), dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)

	// Verify git repo was created with the file tracked
	_, err = os.Stat(filepath.Join(dir, ".git"))
	assert.NoError(t, err)
}

func TestGitBaseline_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	sha, err := git.NewTestHostWithEnv(testutil.GitEnv()).Baseline(context.Background(), dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40, "allow-empty should produce a valid commit")
}

func TestGitBaseline_EmptyGitRepo(t *testing.T) {
	// Regression test: git init with no commits should be handled gracefully
	dir := t.TempDir()
	require.NoError(t, git.NewTestHostWithEnv(testutil.GitEnv()).RunCmd(context.Background(), dir, "init"))
	writeTestFile(t, dir, "file.txt", "hello")

	// setupWorkdir should remove the empty .git and create a fresh baseline.
	sandboxDir := filepath.Join(t.TempDir(), "test-sandbox")
	workdir := &DirSpec{Path: dir, Mode: DirMode("copy")}
	rt := &mockDockerRuntime{} // Docker-like backend: creates baseline on host
	_, sha, err := setupWorkdir(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), sandboxDir, workdir, rt)
	require.NoError(t, err)
	assert.Len(t, sha, 40)
}

// removeGitDirs tests

func TestRemoveGitDirs_TopLevel(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	require.NoError(t, os.MkdirAll(filepath.Join(gitDir, "objects"), 0750))
	writeTestFile(t, dir, "file.txt", "hello")

	require.NoError(t, workspace.RemoveGitDirs(dir))

	assert.NoDirExists(t, gitDir)
	assert.FileExists(t, filepath.Join(dir, "file.txt"))
}

func TestRemoveGitDirs_Submodule(t *testing.T) {
	dir := t.TempDir()
	submod := filepath.Join(dir, "submod")
	require.NoError(t, os.MkdirAll(submod, 0750))
	writeTestFile(t, submod, "code.go", "package main")
	// Submodule .git is a file pointing to parent repo
	writeTestFile(t, submod, ".git", "gitdir: ../../.git/modules/submod")

	require.NoError(t, workspace.RemoveGitDirs(dir))

	assert.NoFileExists(t, filepath.Join(submod, ".git"))
	assert.FileExists(t, filepath.Join(submod, "code.go"))
}

func TestRemoveGitDirs_Nested(t *testing.T) {
	dir := t.TempDir()

	// Top-level .git dir
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0750))

	// Nested submodule with .git file
	nested := filepath.Join(dir, "a", "b")
	require.NoError(t, os.MkdirAll(nested, 0750))
	writeTestFile(t, nested, ".git", "gitdir: ../../../.git/modules/a/b")
	writeTestFile(t, nested, "main.go", "package main")

	require.NoError(t, workspace.RemoveGitDirs(dir))

	assert.NoDirExists(t, filepath.Join(dir, ".git"))
	assert.NoFileExists(t, filepath.Join(nested, ".git"))
	assert.FileExists(t, filepath.Join(nested, "main.go"))
}

func TestRemoveGitDirs_NoGit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")

	require.NoError(t, workspace.RemoveGitDirs(dir))
	assert.FileExists(t, filepath.Join(dir, "file.txt"))
}
