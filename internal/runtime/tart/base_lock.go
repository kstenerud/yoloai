//go:build !windows

// ABOUTME: Advisory flock-based mutex for Tart base VM creation, preventing
// ABOUTME: concurrent builds of the same base image from different processes.
package tart

import (
	"fmt"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/locking"
)

// AcquireBaseLock acquires an exclusive advisory lock on Tart base
// VM creation. Blocks until the lock is available — base VM builds
// legitimately take minutes (pulling 5+ GB images), so "wait for the
// other build to finish" is the right semantic; the second caller's
// IsReady check will then return true and skip rebuilding.
//
// Delegates to the internal/locking primitive for the flock dance.
// Mirrors the Docker backend's AcquireBaseLock and the per-sandbox
// AcquireLock — all three sit on the same primitive (Q-W.4a).
func AcquireBaseLock(layout config.Layout, baseName string) (func(), error) {
	path := layout.TartBaseLockPath(baseName)
	release, err := locking.AcquireBlocking(path)
	if err != nil {
		return nil, fmt.Errorf("acquire tart base lock for %q: %w", baseName, err)
	}
	return release, nil
}
