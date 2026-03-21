// ABOUTME: Per-sandbox advisory file locking using flock(2) on Unix/Linux/macOS.

//go:build !windows

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kstenerud/yoloai/config"
	"golang.org/x/sys/unix"
)

// lockPath returns the path to the per-sandbox advisory lockfile.
// Lives next to the sandbox dir (not inside it) so it works before
// the sandbox directory is created (e.g. during "yoloai new").
func lockPath(name string) string {
	return filepath.Join(config.SandboxesDir(), name+".lock") //nolint:gosec // name is validated by ValidateName
}

// acquireLock creates (or opens) the per-sandbox lockfile and acquires an
// exclusive flock. It blocks until the lock is available. The returned
// release function must be called when the protected operation completes.
//
// The lockfile is left on disk after release; this is harmless — it is a
// 0-byte advisory file and the next call for the same sandbox reuses it.
// Locks are released automatically if the process exits or crashes.
func acquireLock(name string) (func(), error) {
	if err := os.MkdirAll(config.SandboxesDir(), 0750); err != nil {
		return nil, fmt.Errorf("create sandboxes dir: %w", err)
	}
	path := lockPath(name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec // path constructed from validated name
	if err != nil {
		return nil, fmt.Errorf("open sandbox lockfile: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil { //nolint:gosec // file descriptor fits in int on all supported platforms
		_ = f.Close()
		return nil, fmt.Errorf("acquire sandbox lock for %q: %w", name, err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN) //nolint:gosec // same fd
		_ = f.Close()
	}, nil
}

// acquireMultiLock acquires exclusive locks on multiple sandbox names atomically
// (in sorted order to prevent deadlocks). The returned release function
// releases all locks. Use this when an operation touches two sandboxes
// (e.g. clone: source + destination).
func acquireMultiLock(names ...string) (func(), error) {
	sorted := make([]string, len(names))
	copy(sorted, names)
	sort.Strings(sorted)

	var files []*os.File
	release := func() {
		for _, f := range files {
			_ = unix.Flock(int(f.Fd()), unix.LOCK_UN) //nolint:gosec // same fd
			_ = f.Close()
		}
	}

	if err := os.MkdirAll(config.SandboxesDir(), 0750); err != nil {
		return nil, fmt.Errorf("create sandboxes dir: %w", err)
	}
	for _, name := range sorted {
		path := lockPath(name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec // path constructed from validated name
		if err != nil {
			release()
			return nil, fmt.Errorf("open sandbox lockfile for %q: %w", name, err)
		}
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil { //nolint:gosec // file descriptor fits in int
			_ = f.Close()
			release()
			return nil, fmt.Errorf("acquire sandbox lock for %q: %w", name, err)
		}
		files = append(files, f)
	}

	return release, nil
}
