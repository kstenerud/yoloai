// ABOUTME: Unit test for the SIGTERM→SIGKILL escalation ladder in stopVM. Uses a
// ABOUTME: fake tart binary and a SIGTERM-ignoring child to validate that stopVM
// ABOUTME: kills wedged processes without requiring a real Tart VM or daemon.
package tart

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStopVM_EscalatesToSIGKILL verifies that stopVM:
//   - issues SIGTERM to a process whose argv matches "tart run.*<vmName>",
//   - escalates to SIGKILL when the process ignores SIGTERM,
//   - logs a WARN with event "tart.stop.escalation" containing the PID,
//   - returns within the bounded timeout.
func TestStopVM_EscalatesToSIGKILL(t *testing.T) {
	const vmName = "yoloai-test-wedge-vm"

	// --- fake tart binary ---
	// Hangs on "stop" (will be killed by stopCtx timeout after tartGracefulStopTimeout).
	// Returns "[]" on "list" so vmState returns empty — not needed by stopVM but
	// avoids any stray calls producing parse errors.
	tmpDir := t.TempDir()
	fakeTart := filepath.Join(tmpDir, "tart")
	// "exec sleep 300" replaces the shell with sleep so no subprocess inherits the
	// stdout pipe. Without exec, sleep 300 inherits the pipe and cmd.Output() blocks
	// until sleep exits (300s), far outlasting the stopCtx cancellation.
	script := `#!/bin/sh
case "$1" in
  stop) exec sleep 300 ;;
  list) echo '[]' ;;
esac
`
	require.NoError(t, os.WriteFile(fakeTart, []byte(script), 0700)) //nolint:gosec // G306: test binary needs execute bit

	rt := &Runtime{tartBin: fakeTart}

	// --- SIGTERM-ignoring child process ---
	// argv[0] matches "tart run.*yoloai-test-wedge-vm" so pgrepTartRun finds it.
	child := &exec.Cmd{
		Path:        "/bin/bash",
		Args:        []string{"tart run " + vmName, "-c", "trap '' TERM; while true; do sleep 0.1; done"},
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	require.NoError(t, child.Start())
	childPID := child.Process.Pid
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})

	// Give the kernel time to add the child to the process table.
	time.Sleep(100 * time.Millisecond)

	// --- capture slog output ---
	var logBuf bytes.Buffer
	origLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	defer slog.SetDefault(origLogger)

	// Shrink the escalation timeouts to milliseconds — this test validates the
	// SIGTERM→SIGKILL escalation logic, not the production 10s/5s durations, so a
	// real 15s wall-clock wait would needlessly dominate the unit suite.
	origGraceful, origSigterm := tartGracefulStopTimeout, tartSigtermWait
	tartGracefulStopTimeout, tartSigtermWait = 200*time.Millisecond, 200*time.Millisecond
	defer func() { tartGracefulStopTimeout, tartSigtermWait = origGraceful, origSigterm }()

	// --- invoke stopVM and assert it returns within the bounded timeout ---
	// shrunk timeouts (200ms + 200ms) well under the 20s budget.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		rt.stopVM(ctx, vmName)
		close(done)
	}()

	select {
	case <-done:
		// expected
	case <-ctx.Done():
		t.Fatal("stopVM did not return within 20 seconds")
	}

	// Reap the child so it transitions from zombie to fully gone; kill(0) returns
	// ESRCH only after the parent calls wait (SIGKILL leaves a zombie until then).
	_ = child.Wait()

	// --- child process must be dead ---
	err := syscall.Kill(childPID, 0)
	assert.ErrorIs(t, err, syscall.ESRCH, "child PID %d should be dead after stopVM", childPID)

	// --- slog must contain a WARN with event "tart.stop.escalation" and the PID ---
	var escalationFound bool
	for _, line := range strings.Split(strings.TrimSpace(logBuf.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["event"] != "tart.stop.escalation" {
			continue
		}
		// survivors is logged as a []int; JSON decodes numbers as float64.
		survivors, _ := entry["survivors"].([]any)
		pidFound := false
		for _, v := range survivors {
			if f, ok := v.(float64); ok && int(f) == childPID {
				pidFound = true
				break
			}
		}
		assert.True(t, pidFound, "survivors list should contain child PID %d; got: %v", childPID, survivors)
		escalationFound = true
		break
	}
	assert.True(t, escalationFound,
		"expected WARN log entry with event=tart.stop.escalation; got logs:\n%s", logBuf.String())
}
