// ABOUTME: Tests for the orphan-injector sweep: the keep-set/self exclusion and
// ABOUTME: the real SIGTERM→SIGKILL reap, driven with a fake process enumerator.
package broker

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/sysexec"
)

// spawnBlocker starts a long-lived child (`sleep 300`) and returns its PID. A
// background goroutine reaps it when it dies, so a SIGTERM'd child doesn't linger
// as a zombie that processAlive would misread — mirroring production, where the
// detached injector is reaped by init, not left defunct. Killed on cleanup.
func spawnBlocker(t *testing.T) int {
	t.Helper()
	cmd := sysexec.Command([]string{}, "sleep", "300")
	require.NoError(t, cmd.Start())
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd.Process.Pid
}

func withEnumerator(t *testing.T, pids []int) {
	t.Helper()
	orig := enumerateInjectorPIDs
	enumerateInjectorPIDs = func() ([]int, error) { return pids, nil }
	t.Cleanup(func() { enumerateInjectorPIDs = orig })
}

func TestReapOrphanInjectors_KillsOrphanKeepsLiveAndSelf(t *testing.T) {
	orphan := spawnBlocker(t)
	live := spawnBlocker(t)
	withEnumerator(t, []int{orphan, live, os.Getpid()})

	reaped, err := ReapOrphanInjectors(map[int]bool{live: true}, false)
	require.NoError(t, err)

	assert.Equal(t, []int{orphan}, reaped, "only the unkept, non-self process is reaped")
	waitUntilDead(t, orphan)
	assert.True(t, processAlive(live), "a kept injector is left running")
	assert.True(t, processAlive(os.Getpid()), "the sweeping process never reaps itself")
}

func TestReapOrphanInjectors_DryRunReportsButDoesNotKill(t *testing.T) {
	orphan := spawnBlocker(t)
	withEnumerator(t, []int{orphan})

	reaped, err := ReapOrphanInjectors(nil, true)
	require.NoError(t, err)

	assert.Equal(t, []int{orphan}, reaped, "dry-run still reports what it would reap")
	assert.True(t, processAlive(orphan), "dry-run kills nothing")
	_ = syscall.Kill(orphan, syscall.SIGKILL)
}
