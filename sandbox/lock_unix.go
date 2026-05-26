// ABOUTME: Per-sandbox advisory file locking using flock(2) on Unix/Linux/macOS.
// ABOUTME: Non-blocking acquire with brief retry; SandboxLockedError on contention.

//go:build !windows

package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/yoerrors"
	"golang.org/x/sys/unix"
)

// lockPath returns the path to the per-sandbox advisory lockfile.
// Lives next to the sandbox dir (not inside it) so it works before
// the sandbox directory is created (e.g. during "yoloai new").
func lockPath(name string) string {
	return filepath.Join(config.SandboxesDir(), name+".lock") //nolint:gosec // name is validated by ValidateName
}

// Retry tuning. Total budget ≈ lockRetryAttempts × lockRetryInterval =
// 3 seconds. Long enough to absorb a brief overlap from two
// back-to-back invocations; short enough that a genuine hang surfaces
// quickly as a *SandboxLockedError instead of a silent block.
//
// Variables (not constants) so tests can temporarily lower the budget
// to exercise the contention-exhaustion path without sleeping for
// three real seconds per case.
var (
	lockRetryAttempts = 30
	lockRetryInterval = 100 * time.Millisecond
)

// AcquireLock creates (or opens) the per-sandbox lockfile and acquires
// an exclusive flock. Non-blocking: tries the flock up to
// lockRetryAttempts times with lockRetryInterval between attempts.
// Returns *SandboxLockedError (with holder PID + aliveness) when the
// retry budget is exhausted.
//
// On success, writes the acquiring process's PID to the file content
// (informational — the flock(2) advisory lock is still the source of
// truth for mutual exclusion). The release function clears the PID
// bytes and releases the flock.
//
// The lockfile is left on disk after release; this is harmless — it
// is an advisory file and the next call for the same sandbox reuses
// it. Locks are released automatically if the process exits or
// crashes (flock semantics).
//
// **Lock-acquisition invariant (Q-T).** Every public Manager method
// that mutates a sandbox's state calls AcquireLock (or
// acquireMultiLock) at the method entry, before any backend RPC or
// filesystem op, and releases via defer. The lock acquisition is
// part of the method's signature, not pushed down into internals.
// Read methods (List, Inspect, Status, NeedsConfirmation,
// SandboxFiles, SandboxCache) do NOT acquire the lock and run in
// parallel.
//
// Current writer set (audit point — keep in sync as methods are
// added):
//
//	Create, Stop, Start, Destroy, Reset      → sandbox/{create,lifecycle}.go
//	Clone                                    → sandbox/clone.go (multi-lock)
//	SendInput                                → sandbox/manager.go
//	ApplyAll                                 → sandbox/patch/apply.go
//
// The W-L10 layering linter (planned) will verify that new write
// methods on the future Client surface include AcquireLock at their
// public entry point.
func AcquireLock(name string) (func(), error) {
	if err := fileutil.MkdirAll(config.SandboxesDir(), 0750); err != nil {
		return nil, fmt.Errorf("create sandboxes dir: %w", err)
	}
	path := lockPath(name)
	f, err := fileutil.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // path constructed from validated name
	if err != nil {
		return nil, fmt.Errorf("open sandbox lockfile: %w", err)
	}

	if err := flockWithRetry(int(f.Fd())); err != nil { //nolint:gosec // file descriptor fits in int
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, newLockedError(name, path)
		}
		return nil, fmt.Errorf("acquire sandbox lock for %q: %w", name, err)
	}

	// Record our PID for contention readers. Best-effort — the flock
	// is the source of truth; a write failure here doesn't undo the
	// lock acquisition.
	_ = writeHolderPID(f, os.Getpid())

	return func() {
		_ = clearHolderPID(f)
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN) //nolint:gosec // same fd
		_ = f.Close()
	}, nil
}

// acquireMultiLock acquires exclusive locks on multiple sandbox names
// atomically (in sorted order to prevent deadlocks). The returned
// release function releases all locks. Use this when an operation
// touches two sandboxes (e.g. clone: source + destination).
//
// Returns *SandboxLockedError for the first name that fails the retry
// budget; previously-acquired locks in this call are released before
// returning.
func acquireMultiLock(names ...string) (func(), error) {
	sorted := make([]string, len(names))
	copy(sorted, names)
	sort.Strings(sorted)

	var files []*os.File
	release := func() {
		for _, f := range files {
			_ = clearHolderPID(f)
			_ = unix.Flock(int(f.Fd()), unix.LOCK_UN) //nolint:gosec // same fd
			_ = f.Close()
		}
	}

	if err := fileutil.MkdirAll(config.SandboxesDir(), 0750); err != nil {
		return nil, fmt.Errorf("create sandboxes dir: %w", err)
	}
	for _, name := range sorted {
		path := lockPath(name)
		f, err := fileutil.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // path constructed from validated name
		if err != nil {
			release()
			return nil, fmt.Errorf("open sandbox lockfile for %q: %w", name, err)
		}
		if err := flockWithRetry(int(f.Fd())); err != nil { //nolint:gosec // fd fits in int
			_ = f.Close()
			release()
			if errors.Is(err, unix.EWOULDBLOCK) {
				return nil, newLockedError(name, path)
			}
			return nil, fmt.Errorf("acquire sandbox lock for %q: %w", name, err)
		}
		_ = writeHolderPID(f, os.Getpid())
		files = append(files, f)
	}

	return release, nil
}

// ForceUnlock clears a stale per-sandbox lock file. Returns
// cleared=true when an existing lock file was removed; cleared=false
// when no lock file was present (idempotent no-op). Refuses (returns
// a *UsageError) when the recorded holder PID is alive — the right
// recovery for an alive holder is to wait or kill that process, not
// to silently break invariants of a running operation.
//
// On Unix, "clearing" means removing the lock file. flock state is
// released the moment the holder's file descriptor closes (process
// exit), so removing the file when the holder is dead has no race
// against an in-progress operation.
//
// CLI: `yoloai sandbox <name> unlock`. See api_surface.go's Q-Z
// resolution for the rationale.
func ForceUnlock(name string) (cleared bool, err error) {
	path := lockPath(name)

	if _, statErr := os.Stat(path); errors.Is(statErr, fs.ErrNotExist) {
		return false, nil
	}

	pid, _ := readHolderPID(path) // 0 if unreadable

	if pid != 0 && isProcessAlive(pid) {
		return false, yoerrors.NewUsageError(
			"sandbox %q is in use by another process (PID %d); kill that process before forcing unlock",
			name, pid,
		)
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("remove lock file %s: %w", path, err)
	}
	return true, nil
}

// flockWithRetry attempts LOCK_EX|LOCK_NB up to lockRetryAttempts
// times, sleeping lockRetryInterval between attempts. Returns the
// last unix.EWOULDBLOCK if the retry budget is exhausted, or any
// other error immediately.
func flockWithRetry(fd int) error {
	var lastErr error
	for i := 0; i < lockRetryAttempts; i++ {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return err
		}
		lastErr = err
		if i < lockRetryAttempts-1 {
			time.Sleep(lockRetryInterval)
		}
	}
	return lastErr
}

// newLockedError reads the lock file's recorded holder PID, classifies
// aliveness, and returns a *SandboxLockedError.
func newLockedError(name, path string) error {
	pid, _ := readHolderPID(path) // 0 if unreadable
	return &yoerrors.SandboxLockedError{
		Name:        name,
		HolderPID:   pid,
		HolderAlive: pid != 0 && isProcessAlive(pid),
		LockPath:    path,
	}
}

// writeHolderPID writes pid as a decimal string to f, truncating any
// previous content. The lock holder is identified by this PID for
// contention readers.
func writeHolderPID(f *os.File, pid int) error {
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err := fmt.Fprintf(f, "%d\n", pid)
	return err
}

// clearHolderPID truncates the lock file to zero bytes on release so a
// future reader of a stale lock file doesn't see a misleading PID.
// Best-effort; the flock release is the actual ownership transfer.
func clearHolderPID(f *os.File) error {
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	return f.Truncate(0)
}

// readHolderPID reads the lock file at path and parses the recorded
// holder PID. Returns 0 if the file is missing, empty, or contains
// anything other than a decimal integer.
func readHolderPID(path string) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is config-derived
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return 0, nil
	}
	pid, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return pid, nil
}

// isProcessAlive returns true if pid names a live process on this
// host. Implementation: syscall.Kill(pid, 0) — ESRCH means dead, EPERM
// means alive (but not owned by us; still counts as alive), no error
// means alive. PID reuse on long-uptime systems is a real but small
// risk; combined with "ForceUnlock refuses alive holders" the failure
// mode (refuse to clear a reused PID) is safe — the user can rm the
// file manually if needed.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}
