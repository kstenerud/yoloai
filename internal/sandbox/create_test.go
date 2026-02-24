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
	data, err := buildContainerConfig(agentDef, "claude --dangerously-skip-permissions")
	require.NoError(t, err)

	var cfg containerConfig
	err = json.Unmarshal(data, &cfg)
	require.NoError(t, err)

	assert.Equal(t, "claude --dangerously-skip-permissions", cfg.AgentCommand)
	assert.Equal(t, 3000, cfg.StartupDelay)
	assert.Equal(t, "Enter Enter", cfg.SubmitSequence)
	assert.Equal(t, os.Getuid(), cfg.HostUID)
	assert.Equal(t, os.Getgid(), cfg.HostGID)
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

func TestGitBaseline_ExistingRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha, err := gitBaseline(dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)
}

func TestGitBaseline_WorktreeLink(t *testing.T) {
	// Create a main repo
	mainDir := t.TempDir()
	initGitRepo(t, mainDir)
	writeTestFile(t, mainDir, "file.txt", "hello")
	gitAdd(t, mainDir, ".")
	gitCommit(t, mainDir, "initial")

	// Create a worktree
	worktreeDir := filepath.Join(t.TempDir(), "wt")
	runGit(t, mainDir, "worktree", "add", worktreeDir, "-b", "test-branch")

	// Simulate cp -rp of the worktree (copy all files including .git file)
	copyDir := t.TempDir()
	for _, name := range []string{"file.txt", ".git"} {
		src := filepath.Join(worktreeDir, name)
		dst := filepath.Join(copyDir, name)
		data, err := os.ReadFile(src) //nolint:gosec // test code
		require.NoError(t, err)
		info, err := os.Stat(src)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(dst, data, info.Mode()))
	}

	// Verify .git is a file (worktree link), not a directory
	info, err := os.Lstat(filepath.Join(copyDir, ".git"))
	require.NoError(t, err)
	assert.False(t, info.IsDir(), ".git should be a file (worktree link)")

	// gitBaseline should disconnect from original and create fresh baseline
	sha, err := gitBaseline(copyDir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)

	// Verify .git is now a directory (standalone repo)
	info, err = os.Lstat(filepath.Join(copyDir, ".git"))
	require.NoError(t, err)
	assert.True(t, info.IsDir(), ".git should now be a directory (standalone repo)")
}

func TestGitBaseline_NonGitDir(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")

	sha, err := gitBaseline(dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)

	// Verify git repo was created
	_, err = os.Stat(filepath.Join(dir, ".git"))
	assert.NoError(t, err)
}
