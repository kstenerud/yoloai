//go:build integration

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cliSetup creates an isolated HOME, a project dir, and ensures EnsureSetup
// has run (base image built). Returns a cleanup-enabled *testing.T context.
func cliSetup(t *testing.T) (projectDir string) {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	projectDir = filepath.Join(tmpHome, "project")
	require.NoError(t, os.MkdirAll(projectDir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))

	// Run EnsureSetup via a quick `new --no-start` then destroy, or just
	// invoke setup by creating a throwaway sandbox. We use the root command
	// to trigger EnsureSetup via the Manager.
	root := newRootCmd("test", "test", "test")
	root.SetArgs([]string{"new", "--agent", "test", "--no-start", "cli-setup", projectDir})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	require.NoError(t, root.ExecuteContext(context.Background()))

	// Clean up the setup sandbox
	root = newRootCmd("test", "test", "test")
	root.SetArgs([]string{"destroy", "--yes", "cli-setup"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	_ = root.ExecuteContext(context.Background())

	return projectDir
}

// runCLI executes a command through the root Cobra command and returns
// stdout, stderr, and any error.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd("test", "test", "test")
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return outBuf.String(), errBuf.String(), err
}

// destroySandbox is a cleanup helper that destroys a sandbox, ignoring errors.
func destroySandbox(t *testing.T, name string) {
	t.Helper()
	runCLI(t, "destroy", "--yes", name) //nolint:errcheck // best-effort cleanup
}

func TestCLI_NewAndDestroy(t *testing.T) {
	projectDir := cliSetup(t)

	_, stderr, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-new", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-new") })

	assert.DirExists(t, sandbox.Dir("cli-new"))
	assert.Contains(t, stderr, "cli-new") // Manager output goes to stderr

	_, stderr, err = runCLI(t, "destroy", "--yes", "cli-new")
	require.NoError(t, err)
	assert.Contains(t, stderr, "Destroyed")
	assert.NoDirExists(t, sandbox.Dir("cli-new"))
}

func TestCLI_NewWithPrompt(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "--prompt", "echo hi", "cli-prompt", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-prompt") })

	sandboxDir := sandbox.Dir("cli-prompt")
	prompt, err := os.ReadFile(filepath.Join(sandboxDir, "prompt.txt")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, "echo hi", string(prompt))
}

func TestCLI_Ls(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-ls", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-ls") })

	stdout, _, err := runCLI(t, "ls")
	require.NoError(t, err)
	assert.Contains(t, stdout, "cli-ls")
}

func TestCLI_LsJSON(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-lsjson", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-lsjson") })

	stdout, _, err := runCLI(t, "--json", "ls")
	require.NoError(t, err)

	var result []json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.GreaterOrEqual(t, len(result), 1)
}

func TestCLI_Diff(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-diff", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-diff") })

	// Modify work copy
	meta, err := sandbox.LoadMeta(sandbox.Dir("cli-diff"))
	require.NoError(t, err)
	workDir := sandbox.WorkDir("cli-diff", meta.Workdir.HostPath)
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"diff-test\") }\n"),
		0600,
	))

	stdout, _, err := runCLI(t, "diff", "cli-diff")
	require.NoError(t, err)
	assert.Contains(t, stdout, "fmt")
}

func TestCLI_StartStop(t *testing.T) {
	projectDir := cliSetup(t)

	// Create and start in one step (avoids separate start which recreates container)
	_, _, err := runCLI(t, "new", "--agent", "test", "cli-startstop", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-startstop") })

	// Wait for container to stabilize
	time.Sleep(2 * time.Second)

	_, _, err = runCLI(t, "stop", "cli-startstop")
	require.NoError(t, err)
}

func TestCLI_Log(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-log", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-log") })

	// Write a fake log for testing
	sandboxDir := sandbox.Dir("cli-log")
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "log.txt"), []byte("test log output\n"), 0600))

	stdout, _, err := runCLI(t, "log", "cli-log")
	require.NoError(t, err)
	assert.Contains(t, stdout, "test log output")
}

func TestCLI_DestroyNonExistent(t *testing.T) {
	_ = cliSetup(t)

	_, _, err := runCLI(t, "destroy", "--yes", "nonexistent-sandbox-xyz")
	assert.Error(t, err)
}

func TestCLI_NewDuplicate(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-dup", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-dup") })

	_, _, err = runCLI(t, "new", "--agent", "test", "--no-start", "cli-dup", projectDir)
	assert.Error(t, err, "creating duplicate sandbox should fail")
}

func TestCLI_NewReplace(t *testing.T) {
	projectDir := cliSetup(t)

	_, _, err := runCLI(t, "new", "--agent", "test", "--no-start", "cli-replace", projectDir)
	require.NoError(t, err)
	t.Cleanup(func() { destroySandbox(t, "cli-replace") })

	_, _, err = runCLI(t, "new", "--agent", "test", "--no-start", "--replace", "cli-replace", projectDir)
	require.NoError(t, err)

	assert.DirExists(t, sandbox.Dir("cli-replace"))
}
