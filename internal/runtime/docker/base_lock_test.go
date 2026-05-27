//go:build !windows

// ABOUTME: Tests for Docker base-image build lock.

package docker

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/stretchr/testify/require"
)

// testLayout returns a Layout rooted at the current test HOME (set via
// t.Setenv("HOME", ...) by the caller). Lock files land in an isolated dir.
func testLayout(t *testing.T) config.Layout {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal("os.UserHomeDir():", err)
	}
	return config.NewLayout(home + "/.yoloai")
}

// TestAcquireBaseLock_MutualExclusion verifies that a second goroutine
// blocks on AcquireBaseLock until the first releases it. This is the
// invariant that prevents two Setup() callers from racing on the
// docker base image build/tag.
func TestAcquireBaseLock_MutualExclusion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock1, err := AcquireBaseLock(testLayout(t), "yoloai-base")
	require.NoError(t, err)

	acquired := make(chan struct{})
	go func() {
		unlock2, err2 := AcquireBaseLock(testLayout(t), "yoloai-base")
		if err2 == nil {
			close(acquired)
			unlock2()
		}
	}()

	// Confirm goroutine 2 is blocked while goroutine 1 holds the lock.
	select {
	case <-acquired:
		t.Fatal("second base lock acquired while first still held")
	case <-time.After(50 * time.Millisecond):
	}

	unlock1()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second base lock not acquired after first released")
	}
}

// TestAcquireBaseLock_IndependentBaseNames verifies that locks on
// different base names (a hypothetical future extension to per-profile
// base images) do not block each other.
func TestAcquireBaseLock_IndependentBaseNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	unlock1, err := AcquireBaseLock(testLayout(t), "yoloai-base")
	require.NoError(t, err)
	defer unlock1()

	unlock2, err := AcquireBaseLock(testLayout(t), "yoloai-base-alt")
	require.NoError(t, err)
	unlock2()
}

// TestAcquireBaseLock_Reacquirable verifies the lock can be acquired
// repeatedly after release.
func TestAcquireBaseLock_Reacquirable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	for range 3 {
		unlock, err := AcquireBaseLock(testLayout(t), "yoloai-base")
		require.NoError(t, err)
		unlock()
	}
}

// TestAcquireBaseLock_ConcurrentCallers verifies that under heavy
// concurrent acquisition, exactly one holder at a time makes progress.
// This is the integration-style test that mirrors the production race
// (multiple `yoloai new` processes hitting Setup() at once).
func TestAcquireBaseLock_ConcurrentCallers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const N = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		inCritSec int
		maxConcur int
	)

	wg.Add(N)
	for range N {
		go func() {
			defer wg.Done()
			unlock, err := AcquireBaseLock(testLayout(t), "yoloai-base")
			if err != nil {
				t.Errorf("acquire failed: %v", err)
				return
			}
			mu.Lock()
			inCritSec++
			if inCritSec > maxConcur {
				maxConcur = inCritSec
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond) // simulate brief "build" work
			mu.Lock()
			inCritSec--
			mu.Unlock()
			unlock()
		}()
	}

	wg.Wait()
	require.Equal(t, 1, maxConcur, "lock failed mutual exclusion: %d holders observed concurrently", maxConcur)
}
