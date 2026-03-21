// ABOUTME: Tests for per-sandbox advisory file locking.

//go:build !windows

package sandbox

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAcquireLock_CreatesDir verifies acquireLock succeeds when the sandboxes
// directory does not yet exist (e.g. first run with an empty HOME).
func TestAcquireLock_CreatesDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock, err := acquireLock("mybox")
	require.NoError(t, err)
	unlock()
}

// TestAcquireLock_MutualExclusion verifies that a second goroutine blocks on
// acquireLock until the first releases it.
func TestAcquireLock_MutualExclusion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock1, err := acquireLock("mybox")
	require.NoError(t, err)

	// Goroutine 2 tries to acquire the same lock — should block.
	acquired := make(chan struct{})
	go func() {
		unlock2, err2 := acquireLock("mybox")
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

	unlock1, err := acquireLock("box-a")
	require.NoError(t, err)
	defer unlock1()

	unlock2, err := acquireLock("box-b")
	require.NoError(t, err)
	unlock2()
}

// TestAcquireLock_Reacquirable verifies the lock can be re-acquired after
// the release function is called.
func TestAcquireLock_Reacquirable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	for range 3 {
		unlock, err := acquireLock("mybox")
		require.NoError(t, err)
		unlock()
	}
}

// TestAcquireLock_LockfileLeftOnDisk verifies the lockfile persists after
// release — it is a harmless advisory file that the next caller reuses.
func TestAcquireLock_LockfileLeftOnDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock, err := acquireLock("mybox")
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
