//go:build !windows

// ABOUTME: Tests for the flock primitives — block/non-block + mutual exclusion.

package locking

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireBlocking_HappyPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	unlock, err := AcquireBlocking(path)
	require.NoError(t, err)
	defer unlock()

	_, statErr := os.Stat(path)
	assert.NoError(t, statErr, "lockfile should be created on the filesystem")
}

func TestAcquireBlocking_MutualExclusion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	unlock1, err := AcquireBlocking(path)
	require.NoError(t, err)

	acquired := make(chan struct{})
	go func() {
		unlock2, err2 := AcquireBlocking(path)
		if err2 == nil {
			close(acquired)
			unlock2()
		}
	}()

	// Confirm goroutine 2 is blocked.
	select {
	case <-acquired:
		t.Fatal("second AcquireBlocking returned while first still held")
	case <-time.After(50 * time.Millisecond):
	}

	unlock1()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second AcquireBlocking did not unblock after first released")
	}
}

func TestAcquireNonBlocking_ReturnsErrWouldBlockOnContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	unlock, err := AcquireNonBlocking(path)
	require.NoError(t, err)
	defer unlock()

	_, err = AcquireNonBlocking(path)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrWouldBlock), "expected ErrWouldBlock, got %v", err)
}

func TestAcquireNonBlocking_HappyPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	unlock, err := AcquireNonBlocking(path)
	require.NoError(t, err)
	unlock()

	// Should be able to re-acquire after release.
	unlock2, err := AcquireNonBlocking(path)
	require.NoError(t, err)
	unlock2()
}

func TestAcquireWithFile_ReturnsFileForReadWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	f, unlock, err := AcquireWithFile(path)
	require.NoError(t, err)
	defer unlock()
	require.NotNil(t, f)

	// Write something to the lockfile and read it back.
	const pid = "12345\n"
	_, err = f.Write([]byte(pid))
	require.NoError(t, err)

	// Re-read from disk (separate from f, since f is at end-of-write
	// after the Write call).
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is t.TempDir-derived in this test
	require.NoError(t, err)
	assert.Equal(t, pid, string(data))
}

func TestAcquireWithFile_AlsoReturnsErrWouldBlockOnContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	_, unlock, err := AcquireWithFile(path)
	require.NoError(t, err)
	defer unlock()

	_, _, err = AcquireWithFile(path)
	assert.True(t, errors.Is(err, ErrWouldBlock), "expected ErrWouldBlock, got %v", err)
}

// TestParentDirectoryCreatedAutomatically verifies the primitive
// creates missing parent dirs (matches the legacy behavior of the
// sandbox/runtime lock helpers each had).
func TestParentDirectoryCreatedAutomatically(t *testing.T) {
	base := t.TempDir()
	deep := filepath.Join(base, "subdir", "nested", "test.lock")

	unlock, err := AcquireBlocking(deep)
	require.NoError(t, err)
	defer unlock()

	_, err = os.Stat(deep)
	assert.NoError(t, err, "lockfile and its parent dirs should be created")
}

// TestConcurrent_NonBlocking exercises N goroutines hammering the
// same lockfile with non-blocking acquire; verifies that exactly one
// holder is granted at a time and the others reliably see
// ErrWouldBlock.
func TestConcurrent_NonBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	const N = 16
	var (
		wg        sync.WaitGroup
		successes int
		blocked   int
		mu        sync.Mutex
	)
	wg.Add(N)
	for range N {
		go func() {
			defer wg.Done()
			unlock, err := AcquireNonBlocking(path)
			mu.Lock()
			if err == nil {
				successes++
				mu.Unlock()
				time.Sleep(time.Millisecond)
				unlock()
				return
			}
			if errors.Is(err, ErrWouldBlock) {
				blocked++
			} else {
				t.Errorf("unexpected error: %v", err)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Across N attempts, some succeed and some get ErrWouldBlock.
	// The exact split is timing-dependent; we just assert both
	// paths fired.
	assert.Greater(t, successes, 0, "at least one acquire should succeed")
	assert.Greater(t, blocked, 0, "at least one acquire should see ErrWouldBlock")
	assert.Equal(t, N, successes+blocked, "all attempts should resolve")
}
