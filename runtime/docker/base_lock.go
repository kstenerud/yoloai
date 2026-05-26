//go:build !windows

// ABOUTME: Advisory flock-based mutex for Docker base image build/tag,
// ABOUTME: preventing two `yoloai new` invocations from racing on yoloai-base.
package docker

import (
	"fmt"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/internal/locking"
)

// AcquireBaseLock acquires an exclusive advisory lock on Docker base
// image building. Blocks until the lock is available — base image
// builds legitimately take minutes; the right semantic is "wait for
// the other build to finish", at which point the second caller's
// existence check will see the image and skip rebuilding.
//
// Delegates to the internal/locking primitive for the flock dance.
// Mirrors the Tart backend's AcquireBaseLock and the per-sandbox
// AcquireLock — all three sit on the same primitive (Q-W.4a).
func AcquireBaseLock(layout config.Layout, baseName string) (func(), error) {
	path := layout.DockerBaseLockPath(baseName)
	release, err := locking.AcquireBlocking(path)
	if err != nil {
		return nil, fmt.Errorf("acquire docker base lock for %q: %w", baseName, err)
	}
	return release, nil
}
