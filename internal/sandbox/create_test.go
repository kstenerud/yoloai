package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
)

func TestResolveModel_Alias(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := resolveModel(agentDef, "sonnet")
	assert.Equal(t, "claude-sonnet-4-latest", result)
}

func TestResolveModel_FullName(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := resolveModel(agentDef, "claude-sonnet-4-5-20250929")
	assert.Equal(t, "claude-sonnet-4-5-20250929", result)
}

func TestResolveModel_Empty(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := resolveModel(agentDef, "")
	assert.Equal(t, "", result)
}

func TestBuildAgentCommand_InteractiveWithModel(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := buildAgentCommand(agentDef, "claude-opus-4-latest", "", nil)
	assert.Equal(t, "claude --dangerously-skip-permissions --model claude-opus-4-latest", result)
}

func TestBuildAgentCommand_InteractiveWithPassthrough(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	result := buildAgentCommand(agentDef, "claude-sonnet-4-latest", "", []string{"--max-turns", "5"})
	assert.Equal(t, "claude --dangerously-skip-permissions --model claude-sonnet-4-latest --max-turns 5", result)
}

func TestBuildAgentCommand_HeadlessWithPrompt(t *testing.T) {
	agentDef := agent.GetAgent("test")
	result := buildAgentCommand(agentDef, "", "echo hello", nil)
	assert.Equal(t, `sh -c "echo hello"`, result)
}

func TestBuildAgentCommand_InteractiveFallback(t *testing.T) {
	agentDef := agent.GetAgent("test")
	result := buildAgentCommand(agentDef, "", "", nil)
	assert.Equal(t, "bash", result)
}

func TestBuildContainerConfig_ValidJSON(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	data, err := buildContainerConfig(agentDef, "claude --dangerously-skip-permissions", "default+host")
	require.NoError(t, err)

	var cfg containerConfig
	err = json.Unmarshal(data, &cfg)
	require.NoError(t, err)

	assert.Equal(t, "claude --dangerously-skip-permissions", cfg.AgentCommand)
	assert.Equal(t, 3000, cfg.StartupDelay)
	assert.Equal(t, "Enter Enter", cfg.SubmitSequence)
	assert.Equal(t, os.Getuid(), cfg.HostUID)
	assert.Equal(t, os.Getgid(), cfg.HostGID)
	assert.Equal(t, "default+host", cfg.TmuxConf)
}

func TestReadPrompt_DirectText(t *testing.T) {
	result, err := readPrompt("hello", "")
	require.NoError(t, err)
	assert.Equal(t, "hello", result)
}

func TestReadPrompt_File(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "prompt.txt")
	require.NoError(t, os.WriteFile(path, []byte("prompt from file\n"), 0600))

	result, err := readPrompt("", path)
	require.NoError(t, err)
	assert.Equal(t, "prompt from file", result)
}

func TestReadPrompt_MutualExclusion(t *testing.T) {
	_, err := readPrompt("hello", "/some/file")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestReadPrompt_StdinDash(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	_, err = w.WriteString("hello from stdin\n")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	result, err := readPrompt("-", "")
	require.NoError(t, err)
	assert.Equal(t, "hello from stdin", result)
}

func TestParsePortBindings_Valid(t *testing.T) {
	portMap, portSet, err := parsePortBindings([]string{"3000:3000", "8080:80"})
	require.NoError(t, err)

	port3000, _ := nat.NewPort("tcp", "3000")
	port80, _ := nat.NewPort("tcp", "80")

	require.Contains(t, portMap, port3000)
	assert.Equal(t, "3000", portMap[port3000][0].HostPort)
	require.Contains(t, portMap, port80)
	assert.Equal(t, "8080", portMap[port80][0].HostPort)

	assert.Contains(t, portSet, port3000)
	assert.Contains(t, portSet, port80)
}

func TestParsePortBindings_Invalid(t *testing.T) {
	_, _, err := parsePortBindings([]string{"invalid"})
	require.Error(t, err)
}

func TestParsePortBindings_Empty(t *testing.T) {
	portMap, portSet, err := parsePortBindings(nil)
	require.NoError(t, err)
	assert.Nil(t, portMap)
	assert.Nil(t, portSet)
}

func TestGitBaseline_FreshInit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")

	sha, err := gitBaseline(dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)

	// Verify git repo was created with the file tracked
	_, err = os.Stat(filepath.Join(dir, ".git"))
	assert.NoError(t, err)
}

func TestGitBaseline_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	sha, err := gitBaseline(dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40, "allow-empty should produce a valid commit")
}

// removeGitDirs tests

func TestRemoveGitDirs_TopLevel(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	require.NoError(t, os.MkdirAll(filepath.Join(gitDir, "objects"), 0750))
	writeTestFile(t, dir, "file.txt", "hello")

	require.NoError(t, removeGitDirs(dir))

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

	require.NoError(t, removeGitDirs(dir))

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

	require.NoError(t, removeGitDirs(dir))

	assert.NoDirExists(t, filepath.Join(dir, ".git"))
	assert.NoFileExists(t, filepath.Join(nested, ".git"))
	assert.FileExists(t, filepath.Join(nested, "main.go"))
}

func TestRemoveGitDirs_NoGit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")

	require.NoError(t, removeGitDirs(dir))
	assert.FileExists(t, filepath.Join(dir, "file.txt"))
}
