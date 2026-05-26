//go:build !windows

// ABOUTME: Advisory flock-based mutex for Docker base image build/tag,
// ABOUTME: preventing two `yoloai new` invocations from racing on yoloai-base.
package docker

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

// AcquireBaseLock acquires an exclusive advisory lock on Docker base
// image building. Blocks until the lock is available. Returns a
// release function.
//
// Mirrors runtime/tart/base_lock.go — the Docker backend has the same
// race the Tart backend solved with that lock: two concurrent
// Setup() callers can both observe "image missing" and both call
// buildBaseImage; the second one fails at the tag step with
// "AlreadyExists: image yoloai-base:latest already exists" after the
// first one wins.
//
// Holding this lock around the whole Setup body (check + build) means
// the second caller re-checks after the first finishes and skips the
// rebuild instead of racing.
//
// Blocking (LOCK_EX, no retry budget) is deliberate here — base image
// builds legitimately take minutes; "wait for the other build to
// finish" is the right user behavior. This differs from the per-
// sandbox lock (sandbox/lock_unix.go) which uses non-blocking retry
// because sandbox operations are short and a hang there usually
// signals a wedged process, not a legitimate in-progress operation.
func AcquireBaseLock(baseName string) (func(), error) {
	lockDir := config.DockerBaseLocksDir()
	if err := fileutil.MkdirAll(lockDir, 0750); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}

	path := filepath.Join(lockDir, baseName+".lock")
	f, err := fileutil.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open base lockfile: %w", err)
	}

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil { //nolint:gosec // G115: fd conversion is safe
		_ = f.Close()
		return nil, fmt.Errorf("acquire base lock for %q: %w", baseName, err)
	}

	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN) //nolint:gosec // G115: fd conversion is safe
		_ = f.Close()
	}, nil
}
