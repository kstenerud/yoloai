//go:build !windows

package tart

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

// AcquireBaseLock acquires an exclusive advisory lock on base VM creation.
// Blocks until the lock is available. Returns a release function.
func AcquireBaseLock(baseName string) (func(), error) {
	lockDir := config.TartBaseLocksDir()
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
