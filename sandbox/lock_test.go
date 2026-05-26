// ABOUTME: Tests for per-sandbox advisory file locking.

//go:build !windows

package sandbox

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/yoerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withFastRetry temporarily lowers the lock retry budget so contention-
// exhaustion tests don't sleep for the full real-world budget. Returns
// a restorer to call via defer.
func withFastRetry(t *testing.T) {
	t.Helper()
	oldAttempts := lockRetryAttempts
	oldInterval := lockRetryInterval
	lockRetryAttempts = 3
	lockRetryInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		lockRetryAttempts = oldAttempts
		lockRetryInterval = oldInterval
	})
}

// TestAcquireLock_CreatesDir verifies acquireLock succeeds when the sandboxes
// directory does not yet exist (e.g. first run with an empty HOME).
func TestAcquireLock_CreatesDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock, err := AcquireLock("mybox")
	require.NoError(t, err)
	unlock()
}

// TestAcquireLock_MutualExclusion verifies that a second goroutine blocks on
// acquireLock until the first releases it.
func TestAcquireLock_MutualExclusion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock1, err := AcquireLock("mybox")
	require.NoError(t, err)

	// Goroutine 2 tries to acquire the same lock — should block.
	acquired := make(chan struct{})
	go func() {
		unlock2, err2 := AcquireLock("mybox")
		if err2 == nil {
			close(acquired)
			unlock2()
		}
	}()

	// Confirm goroutine 2 is blocked.
	select {
	case <-acquired:
		t.Fatal("second lock acquired while first still held")
	case <-time.After(50 * time.Millisecond):
	}

	// Release — goroutine 2 should unblock.
	unlock1()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second lock not acquired after first released")
	}
}

// TestAcquireLock_IndependentSandboxes verifies locks on different names
// do not block each other.
func TestAcquireLock_IndependentSandboxes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock1, err := AcquireLock("box-a")
	require.NoError(t, err)
	defer unlock1()

	unlock2, err := AcquireLock("box-b")
	require.NoError(t, err)
	unlock2()
}

// TestAcquireLock_Reacquirable verifies the lock can be re-acquired after
// the release function is called.
func TestAcquireLock_Reacquirable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	for range 3 {
		unlock, err := AcquireLock("mybox")
		require.NoError(t, err)
		unlock()
	}
}

// TestAcquireLock_LockfileLeftOnDisk verifies the lockfile persists after
// release — it is a harmless advisory file that the next caller reuses.
func TestAcquireLock_LockfileLeftOnDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock, err := AcquireLock("mybox")
	require.NoError(t, err)
	unlock()

	_, statErr := os.Stat(lockPath("mybox"))
	assert.NoError(t, statErr, "lockfile should remain on disk after release")
}

// TestAcquireMultiLock_DeadlockPrevention verifies that two goroutines locking
// the same pair of names in opposite order do not deadlock. Both complete
// within the timeout because acquireMultiLock sorts names before locking.
func TestAcquireMultiLock_DeadlockPrevention(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			unlock, err := acquireMultiLock("sandbox-x", "sandbox-y")
			require.NoError(t, err)
			time.Sleep(time.Millisecond)
			unlock()
		}()
		go func() {
			defer wg.Done()
			// Reverse order — internally sorted so no deadlock.
			unlock, err := acquireMultiLock("sandbox-y", "sandbox-x")
			require.NoError(t, err)
			time.Sleep(time.Millisecond)
			unlock()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: goroutines did not complete within timeout")
	}
}

// TestAcquireMultiLock_MutualExclusion verifies only one holder of a
// multi-lock can proceed at a time for the same set of names.
func TestAcquireMultiLock_MutualExclusion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock1, err := acquireMultiLock("alpha", "beta")
	require.NoError(t, err)

	acquired := make(chan struct{})
	go func() {
		unlock2, err2 := acquireMultiLock("alpha", "beta")
		if err2 == nil {
			close(acquired)
			unlock2()
		}
	}()

	select {
	case <-acquired:
		t.Fatal("second multi-lock acquired while first still held")
	case <-time.After(50 * time.Millisecond):
	}

	unlock1()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second multi-lock not acquired after first released")
	}
}

// TestAcquireLock_WritesHolderPID verifies the lock file content is the
// acquiring process's PID while the lock is held.
func TestAcquireLock_WritesHolderPID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock, err := AcquireLock("mybox")
	require.NoError(t, err)
	defer unlock()

	data, err := os.ReadFile(lockPath("mybox"))
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), pid, "lock file should record acquiring process's PID")
}

// TestAcquireLock_ClearsHolderPIDOnRelease verifies the lock file is
// truncated when the lock is released, so a future reader of a stale
// file doesn't see a misleading PID.
func TestAcquireLock_ClearsHolderPIDOnRelease(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock, err := AcquireLock("mybox")
	require.NoError(t, err)
	unlock()

	data, err := os.ReadFile(lockPath("mybox"))
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(data)), "lock file content should be cleared on release")
}

// TestAcquireLock_ContentionReturnsTypedError verifies that exhausting
// the retry budget while a holder is alive surfaces *SandboxLockedError
// with HolderAlive=true and the holder's PID.
func TestAcquireLock_ContentionReturnsTypedError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	withFastRetry(t)

	unlock, err := AcquireLock("mybox")
	require.NoError(t, err)
	defer unlock()

	_, err = AcquireLock("mybox")
	require.Error(t, err)

	var lockedErr *yoerrors.SandboxLockedError
	require.True(t, errors.As(err, &lockedErr), "expected *SandboxLockedError, got %T: %v", err, err)
	assert.Equal(t, "mybox", lockedErr.Name)
	assert.Equal(t, os.Getpid(), lockedErr.HolderPID)
	assert.True(t, lockedErr.HolderAlive, "expected HolderAlive=true (the test process is alive)")
	assert.Equal(t, lockPath("mybox"), lockedErr.LockPath)
}

// TestForceUnlock_ClearsStaleLockfile verifies ForceUnlock removes a
// lock file whose recorded holder PID is not alive and reports
// cleared=true.
func TestForceUnlock_ClearsStaleLockfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Simulate a stale lock: lock file exists with a definitely-dead
	// PID. We use a value near PID_MAX that is very unlikely to be
	// alive on the test host.
	require.NoError(t, os.MkdirAll(lockPath("mybox")[:strings.LastIndex(lockPath("mybox"), "/")], 0750))
	stalePID := 2147483646
	require.NoError(t, os.WriteFile(lockPath("mybox"), []byte(strconv.Itoa(stalePID)+"\n"), 0600))

	cleared, err := ForceUnlock("mybox")
	require.NoError(t, err)
	assert.True(t, cleared, "expected cleared=true when a stale lock existed")

	_, statErr := os.Stat(lockPath("mybox"))
	assert.True(t, os.IsNotExist(statErr), "lock file should be removed after ForceUnlock; got err=%v", statErr)
}

// TestForceUnlock_RefusesAliveHolder verifies ForceUnlock refuses with
// *UsageError when the recorded holder PID names a live process, and
// reports cleared=false.
func TestForceUnlock_RefusesAliveHolder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Acquire a real lock so the file has our own (alive) PID.
	unlock, err := AcquireLock("mybox")
	require.NoError(t, err)
	defer unlock()

	cleared, err := ForceUnlock("mybox")
	require.Error(t, err)
	assert.False(t, cleared, "expected cleared=false when ForceUnlock refused")
	var usageErr *yoerrors.UsageError
	assert.True(t, errors.As(err, &usageErr), "expected *UsageError when holder is alive, got %T: %v", err, err)

	// Lock file should still be present.
	_, statErr := os.Stat(lockPath("mybox"))
	assert.NoError(t, statErr, "lock file should remain when ForceUnlock refused")
}

// TestForceUnlock_NoLockFileIsNoOp verifies ForceUnlock on a sandbox
// with no lock file at all returns (cleared=false, err=nil) so the
// caller can distinguish "removed a real stale lock" from "nothing
// was there to remove."
func TestForceUnlock_NoLockFileIsNoOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cleared, err := ForceUnlock("never-existed")
	assert.NoError(t, err)
	assert.False(t, cleared, "expected cleared=false when no lock file existed")
}

// TestIsProcessAlive verifies the PID-aliveness check returns true for
// the test process itself and false for sentinel non-PIDs.
func TestIsProcessAlive(t *testing.T) {
	assert.True(t, isProcessAlive(os.Getpid()), "current process should be alive")
	assert.False(t, isProcessAlive(0), "PID 0 should not count as alive")
	assert.False(t, isProcessAlive(-1), "negative PID should not count as alive")
}
