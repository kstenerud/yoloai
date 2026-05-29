// ABOUTME: Tests for buildContainerConfig, setupWorkdir, and git baseline helpers.
package create

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/sandbox/runtimeconfig"
	"github.com/kstenerud/yoloai/internal/workspace"
)

func TestBuildContainerConfig_LaunchPrefixStored(t *testing.T) {
	// W1a: the wrap prefix passed in is stored verbatim, gate is set true.
	// Single source of truth: at runtime, Python and Go restart both read
	// agent_launch_prefix instead of re-invoking PrepareAgentCommand.
	agentDef := agent.GetAgent("claude")
	prefix := `PATH="/opt/homebrew/opt/node/bin:$PATH" `
	data, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, "claude", prefix, "default", "/tmp", false, false, nil, nil, nil, nil, 0, nil, "test", "", "", false, "", nil)
	require.NoError(t, err)
	var cfg runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &cfg))
	assert.Equal(t, prefix, cfg.AgentLaunchPrefix, "launch prefix must be stored verbatim")
	assert.True(t, cfg.UseLaunchPrefix, "use_launch_prefix gate must be true for new sandboxes")
}

func TestBuildContainerConfig_ValidJSON(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	data, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, "claude --dangerously-skip-permissions", "", "default+host", "/Users/test/project", false, false, nil, nil, nil, nil, 0, nil, "test", "", "", false, "", nil)
	require.NoError(t, err)

	var cfg runtimeconfig.ContainerConfig
	err = json.Unmarshal(data, &cfg)
	require.NoError(t, err)

	assert.Equal(t, "claude --dangerously-skip-permissions", cfg.AgentCommand)
	assert.Equal(t, 3000, cfg.StartupDelay)
	assert.Equal(t, "Enter Enter", cfg.SubmitSequence)
	assert.Equal(t, os.Getuid(), cfg.HostUID)
	assert.Equal(t, os.Getgid(), cfg.HostGID)
	assert.Equal(t, "default+host", cfg.TmuxConf)
	assert.Equal(t, ".claude", cfg.StateDirName)
	assert.False(t, cfg.NetworkIsolated)
	assert.Empty(t, cfg.AllowedDomains)
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
			data, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, "cmd", "", "default", "/tmp", false, false, nil, nil, nil, nil, 0, nil, "test", "", "", false, "", nil)
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
	data, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, "claude", "", "default", "/tmp", false, true, domains, nil, nil, nil, 0, nil, "test", "", "", false, "", nil)
	require.NoError(t, err)

	var cfg runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	assert.True(t, cfg.NetworkIsolated)
	assert.Equal(t, domains, cfg.AllowedDomains)
}

func TestBuildContainerConfig_AutoCommitInterval(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	copyDirs := []string{"/home/user/project", "/home/user/lib"}
	data, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, "claude", "", "default", "/tmp", false, false, nil, nil, nil, nil, 60, copyDirs, "test", "", "", false, "", nil)
	require.NoError(t, err)

	var cfg runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	assert.Equal(t, 60, cfg.AutoCommitInterval)
	assert.Equal(t, copyDirs, cfg.CopyDirs)
}

func TestBuildContainerConfig_AutoCommitIntervalZero(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	data, err := buildContainerConfig(config.NewLayout(t.TempDir()), agentDef, "claude", "", "default", "/tmp", false, false, nil, nil, nil, nil, 0, nil, "test", "", "", false, "", nil)
	require.NoError(t, err)

	var cfg runtimeconfig.ContainerConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	assert.Equal(t, 0, cfg.AutoCommitInterval)
	assert.Nil(t, cfg.CopyDirs)
}

func TestGitBaseline_FreshInit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")

	sha, err := workspace.Baseline(dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)

	// Verify git repo was created with the file tracked
	_, err = os.Stat(filepath.Join(dir, ".git"))
	assert.NoError(t, err)
}

func TestGitBaseline_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	sha, err := workspace.Baseline(dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40, "allow-empty should produce a valid commit")
}

func TestGitBaseline_EmptyGitRepo(t *testing.T) {
	// Regression test: git init with no commits should be handled gracefully
	dir := t.TempDir()
	require.NoError(t, workspace.RunGitCmd(dir, "init"))
	writeTestFile(t, dir, "file.txt", "hello")

	// setupWorkdir should remove the empty .git and create a fresh baseline.
	sandboxDir := filepath.Join(t.TempDir(), "test-sandbox")
	workdir := &DirSpec{Path: dir, Mode: DirMode("copy")}
	rt := &mockDockerRuntime{} // Docker-like backend: creates baseline on host
	_, sha, err := setupWorkdir(sandboxDir, workdir, rt)
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
