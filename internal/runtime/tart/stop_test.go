// ABOUTME: Unit tests for waitForExit and pgrepTartRun — the two helpers that
// ABOUTME: underpin the SIGTERM→SIGKILL escalation ladder in stopVM.
package tart

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWaitForExit_Empty verifies that an empty PID slice is a no-op.
func TestWaitForExit_Empty(t *testing.T) {
	survivors := waitForExit(nil, 100*time.Millisecond)
	assert.Nil(t, survivors)
}

// TestWaitForExit_AlreadyDead verifies that a reaped PID returns nil immediately.
func TestWaitForExit_AlreadyDead(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait() // reap so PID transitions to ESRCH

	// PID is dead — waitForExit should return nil well before the timeout.
	start := time.Now()
	survivors := waitForExit([]int{pid}, 2*time.Second)
	assert.Nil(t, survivors)
	assert.Less(t, time.Since(start), 500*time.Millisecond, "should return quickly for dead PID")
}

// TestWaitForExit_TimesOutWithAlive verifies that a live process is returned
// as a survivor once the timeout expires.
func TestWaitForExit_TimesOutWithAlive(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	const waitTimeout = 200 * time.Millisecond
	start := time.Now()
	survivors := waitForExit([]int{pid}, waitTimeout)
	elapsed := time.Since(start)

	assert.Equal(t, []int{pid}, survivors, "alive PID should be returned as survivor")
	// Should not return substantially sooner than the timeout.
	assert.GreaterOrEqual(t, elapsed, waitTimeout-50*time.Millisecond, "should respect timeout")
	// Should not linger far past the timeout.
	assert.Less(t, elapsed, waitTimeout+500*time.Millisecond, "should not overshoot timeout")
}

// TestPgrepTartRun_FindsMatchingProcess verifies that a process whose argv[0]
// contains "tart run <vmName>" is found by pgrepTartRun.
func TestPgrepTartRun_FindsMatchingProcess(t *testing.T) {
	// Use t.Name() in the VM name to avoid cross-test collisions when tests
	// run in parallel.
	const vmName = "yoloai-pgrep-unit-target"

	// Spawn bash with argv[0] = "tart run <vmName>" so pgrep -f finds it.
	// A loop command is required: "sleep 60" causes bash to exec and replace
	// itself, discarding the custom argv[0].
	child := &exec.Cmd{
		Path:        "/bin/bash",
		Args:        []string{"tart run " + vmName, "-c", "while true; do sleep 0.1; done"},
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	require.NoError(t, child.Start())
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })

	// Brief pause so the process shows up in the kernel's process table.
	time.Sleep(100 * time.Millisecond)

	pids := pgrepTartRun(context.Background(), vmName)
	assert.Contains(t, pids, child.Process.Pid, "pgrepTartRun should find the spawned process")
}

// TestPgrepTartRun_NoMatch verifies that a non-existent VM name produces nil.
func TestPgrepTartRun_NoMatch(t *testing.T) {
	pids := pgrepTartRun(context.Background(), "yoloai-this-vm-does-not-exist-xyz987654")
	assert.Nil(t, pids)
}
