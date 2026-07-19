//go:build e2e

// ABOUTME: Compiled-binary smoke for --debug's cli.jsonl debug entries and the
// ABOUTME: --bugreport flag's flag-only report sections (Live log, Exit code)
// ABOUTME: that only the real Execute() entrypoint can exercise.
package e2e_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runYoloaiInDir runs the compiled yoloai binary from a specific working directory.
// Returns stdout, stderr, and the exit code.
func runYoloaiInDir(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := sysexec.Command(sutEnv(), yoloaiBin, args...)
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
	return filepath.Join(home, ".yoloai", "library", "sandboxes", name, "logs")
}

// TestE2E_Debug_WritesCLIJSONL verifies that --debug causes debug-level entries in cli.jsonl.
func TestE2E_Debug_WritesCLIJSONL(t *testing.T) {
	projectDir := e2eSetup(t)

	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "e2e-debug", projectDir)
	require.Equal(t, 0, code)
	t.Cleanup(func() { destroySandbox(t, "e2e-debug") })

	// Run a command with --debug; cli.jsonl should get debug-level entries
	runYoloai(t, "--debug", "sandbox", "e2e-debug", "info")

	cliJSONL := filepath.Join(sandboxLogsDir(t, "e2e-debug"), "cli.jsonl")
	data, err := os.ReadFile(cliJSONL) //nolint:gosec // G304: path under the test's sandbox logs dir
	require.NoError(t, err, "cli.jsonl should exist after --debug run")
	assert.Contains(t, string(data), `"level":"debug"`, "cli.jsonl should contain debug entries")
}

// TestE2E_BugreportFlag is the flag-path smoke: it verifies that the
// --bugreport global flag, routed through the real Execute() entrypoint on the
// compiled binary, writes a report containing the flag-only sections (Live log
// + Exit code) that the in-process subcommand path cannot exercise. The
// safe/unsafe section-redaction matrix is owned by the bugreport unit tests
// (internal/cli/bugreport/writer_test.go) and the in-process subcommand pair
// (internal/cli/integration_test.go); re-asserting it through the slow
// compiled-binary tier would be ice-cream-cone duplication, so this only checks
// the wiring unique to the flag entrypoint.
func TestE2E_BugreportFlag(t *testing.T) {
	projectDir := e2eSetup(t)

	_, _, code := runYoloai(t, "new", "--agent", "test", "--no-start", "e2e-br", projectDir)
	require.Equal(t, 0, code)
	t.Cleanup(func() { destroySandbox(t, "e2e-br") })

	// Run with --bugreport from a temp dir so we can find the report.
	reportDir := t.TempDir()
	_, _, code = runYoloaiInDir(t, reportDir, "--bugreport", "unsafe", "sandbox", "e2e-br", "info")
	// Exit code may be non-zero if sandbox is not running; that's fine — report is still written.
	_ = code

	matches, err := filepath.Glob(filepath.Join(reportDir, "yoloai-bugreport-*.md"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "expected exactly one bug report file")

	content, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	out := string(content)

	assert.Contains(t, out, "Sandbox detail")
	// Flag-only sections written by finalizeBugReport in the Execute() wrapper.
	assert.Contains(t, out, "Live log")
	assert.Contains(t, out, "Exit code:")
}
