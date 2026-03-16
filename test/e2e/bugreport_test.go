//go:build e2e

package e2e_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runYoloaiInDir runs the compiled yoloai binary from a specific working directory.
// Returns stdout, stderr, and the exit code.
func runYoloaiInDir(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(yoloaiBin, args...) //nolint:gosec // G204: test helper, path set in TestMain
	cmd.Dir = dir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// sandboxLogsDir returns the logs directory for a sandbox using the current HOME env var.
func sandboxLogsDir(t *testing.T, name string) string {
	t.Helper()
	home := os.Getenv("HOME")
	return filepath.Join(home, ".yoloai", "sandboxes", name, "logs")
}

// sandboxDir returns the sandbox state directory for a sandbox using the current HOME env var.
func sandboxStateDir(t *testing.T, name string) string {
	t.Helper()
	home := os.Getenv("HOME")
	return filepath.Join(home, ".yoloai", "sandboxes", name)
}

// TestE2E_Debug_WritesCLIJSONL verifies that --debug causes debug-level entries in cli.jsonl.
func TestE2E_Debug_WritesCLIJSONL(t *testing.T) {
	projectDir := e2eSetup(t)

	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "e2e-debug", projectDir)
	require.Equal(t, 0, code)
	t.Cleanup(func() { destroySandbox(t, "e2e-debug") })

	// Run a command with --debug; cli.jsonl should get debug-level entries
	runYoloai(t, "--debug", "sandbox", "e2e-debug", "info") //nolint:errcheck // return value not needed

	cliJSONL := filepath.Join(sandboxLogsDir(t, "e2e-debug"), "cli.jsonl")
	data, err := os.ReadFile(cliJSONL) //nolint:gosec // test path
	require.NoError(t, err, "cli.jsonl should exist after --debug run")
	assert.Contains(t, string(data), `"level":"debug"`, "cli.jsonl should contain debug entries")
}

// TestE2E_BugreportFlag_Unsafe verifies --bugreport unsafe writes a complete report.
func TestE2E_BugreportFlag_Unsafe(t *testing.T) {
	projectDir := e2eSetup(t)

	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "--prompt", "test task", "e2e-br-unsafe", projectDir)
	require.Equal(t, 0, code)
	t.Cleanup(func() { destroySandbox(t, "e2e-br-unsafe") })

	// Write fake JSONL entries to the sandbox log files
	logsDir := sandboxLogsDir(t, "e2e-br-unsafe")
	require.NoError(t, os.MkdirAll(logsDir, 0700))
	entry := `{"ts":"2026-03-16T10:00:00.000Z","level":"info","event":"test.event","msg":"fake log entry"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "cli.jsonl"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "sandbox.jsonl"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "monitor.jsonl"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "agent-hooks.jsonl"), []byte(entry), 0600))

	// Run with --bugreport unsafe from a temp dir so we can find the report
	reportDir := t.TempDir()
	_, _, code = runYoloaiInDir(t, reportDir, "--bugreport", "unsafe", "sandbox", "e2e-br-unsafe", "info")
	// Exit code may be non-zero if sandbox is not running; that's fine — report is still written
	_ = code

	matches, err := filepath.Glob(filepath.Join(reportDir, "yoloai-bugreport-*.md"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "expected exactly one bug report file")

	content, err := os.ReadFile(matches[0]) //nolint:gosec // test path
	require.NoError(t, err)
	out := string(content)

	assert.Contains(t, out, "Sandbox detail")
	assert.Contains(t, out, "logs/cli.jsonl")
	assert.Contains(t, out, "logs/sandbox.jsonl")
	assert.Contains(t, out, "logs/monitor.jsonl")
	assert.Contains(t, out, "logs/agent-hooks.jsonl")
	assert.Contains(t, out, "Agent output")
	assert.Contains(t, out, "Live log")
	assert.Contains(t, out, "Exit code:") // **Exit code:** section from flag path
	assert.Contains(t, out, "test task")  // prompt.txt included in unsafe
}

// TestE2E_BugreportFlag_Safe verifies --bugreport safe writes a sanitized report.
func TestE2E_BugreportFlag_Safe(t *testing.T) {
	projectDir := e2eSetup(t)

	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "--prompt", "test task", "e2e-br-safe", projectDir)
	require.Equal(t, 0, code)
	t.Cleanup(func() { destroySandbox(t, "e2e-br-safe") })

	// Write fake JSONL entries to the sandbox log files
	logsDir := sandboxLogsDir(t, "e2e-br-safe")
	require.NoError(t, os.MkdirAll(logsDir, 0700))
	entry := `{"ts":"2026-03-16T10:00:00.000Z","level":"info","event":"test.event","msg":"fake log entry"}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "cli.jsonl"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "sandbox.jsonl"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "monitor.jsonl"), []byte(entry), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "agent-hooks.jsonl"), []byte(entry), 0600))

	// Run with --bugreport safe from a temp dir
	reportDir := t.TempDir()
	_, _, code = runYoloaiInDir(t, reportDir, "--bugreport", "safe", "sandbox", "e2e-br-safe", "info")
	_ = code

	matches, err := filepath.Glob(filepath.Join(reportDir, "yoloai-bugreport-*.md"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "expected exactly one bug report file")

	content, err := os.ReadFile(matches[0]) //nolint:gosec // test path
	require.NoError(t, err)
	out := string(content)

	// Safe mode includes log sections
	assert.Contains(t, out, "Sandbox detail")
	assert.Contains(t, out, "logs/cli.jsonl")
	assert.Contains(t, out, "logs/sandbox.jsonl")
	assert.Contains(t, out, "logs/monitor.jsonl")
	assert.Contains(t, out, "logs/agent-hooks.jsonl")

	// Safe mode omits sensitive sections
	assert.NotContains(t, out, "Agent output")
	assert.NotContains(t, out, "prompt.txt") // prompt.txt section omitted in safe mode
	assert.NotContains(t, out, "tmux screen capture")
}
