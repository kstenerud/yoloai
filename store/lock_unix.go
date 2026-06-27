// ABOUTME: Per-sandbox advisory file locking via the locking primitive.
// ABOUTME: Non-blocking acquire with brief retry; SandboxLockedError on contention.

//go:build !windows

package store

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/locking"
	"github.com/kstenerud/yoloai/yoerrors"
)

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

// AcquireLock acquires the per-sandbox lock by name, rooted at the
// given Layout. Non-blocking under the hood: tries the flock up to
// lockRetryAttempts times with lockRetryInterval between attempts.
// Returns *SandboxLockedError (with holder PID + aliveness) when the
// retry budget is exhausted.
//
// On success, writes the acquiring process's PID to the lockfile
// content (informational — the flock(2) advisory lock is still the
// source of truth for mutual exclusion). The release function
// clears the PID bytes and releases the flock.
//
// **Lock-acquisition invariant (Q-T).** Every public Engine method
// that mutates a sandbox's state calls AcquireLock (or
// AcquireMultiLock) at the method entry, before any backend RPC or
// filesystem op, and releases via defer. Read methods do not
// acquire the lock.
//
// Current writer set (keep in sync as methods are added):
//
//	Create, Stop, Start, Destroy, Reset      → sandbox/{create,lifecycle}.go
//	Clone                                    → sandbox/clone.go (multi-lock)
//	SendInput                                → sandbox/engine.go
//	ApplyAll                                 → copyflow/apply.go
//
// New write methods on the Client surface must include AcquireLock at their
// public entry point.
func AcquireLock(layout config.Layout, name string) (func(), error) {
	path := layout.SandboxLockPath(name)
	return acquireWithRetry(layout, name, path)
}

// AcquireMultiLock acquires exclusive locks on multiple sandbox
// names atomically (in sorted order to prevent deadlocks). The
// returned release function releases all locks. Use this when an
// operation touches two sandboxes (e.g. clone: source + destination).
//
// Returns *SandboxLockedError for the first name that fails the
// retry budget; previously-acquired locks in this call are released
// before returning.
func AcquireMultiLock(layout config.Layout, names ...string) (func(), error) {
	sorted := make([]string, len(names))
	copy(sorted, names)
	sort.Strings(sorted)

	var releases []func()
	releaseAll := func() {
		for _, r := range releases {
			r()
		}
	}

	for _, name := range sorted {
		path := layout.SandboxLockPath(name)
		release, err := acquireWithRetry(layout, name, path)
		if err != nil {
			releaseAll()
			return nil, err
		}
		releases = append(releases, release)
	}

	return releaseAll, nil
}

// acquireWithRetry is the shared retry+PID-write helper used by
// AcquireLock and AcquireMultiLock. Returns *SandboxLockedError when
// the retry budget is exhausted.
func acquireWithRetry(layout config.Layout, name, path string) (func(), error) {
	var f *os.File
	var release func()
	var lastErr error
	for i := 0; i < lockRetryAttempts; i++ {
		var err error
		f, release, err = locking.AcquireWithFile(path)
		if err == nil {
			break
		}
		if !errors.Is(err, locking.ErrWouldBlock) {
			return nil, fmt.Errorf("acquire sandbox lock for %q: %w", name, err)
		}
		lastErr = err
		if i < lockRetryAttempts-1 {
			time.Sleep(lockRetryInterval)
		}
	}
	if f == nil {
		// Retry budget exhausted; classify the holder.
		_ = lastErr
		return nil, newLockedError(layout, name)
	}

	// Record our PID for contention readers. Best-effort — the flock
	// is the source of truth; a write failure here doesn't undo the
	// lock acquisition.
	_ = writeHolderPID(f, os.Getpid())

	// Wrap release so we clear the PID bytes before flock-unlocking.
	innerRelease := release
	return func() {
		_ = clearHolderPID(f)
		innerRelease()
	}, nil
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
// CLI: `yoloai sandbox <name> unlock`. See working-notes D45 (Q-Z)
// for the rationale.
func ForceUnlock(layout config.Layout, name string) (cleared bool, err error) {
	path := layout.SandboxLockPath(name)

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

// RemoveLockFile removes the per-sandbox lock file unconditionally.
// Unlike ForceUnlock, it does NOT check the holder PID — it is meant to
// be called by the lock holder itself (e.g. Destroy, which holds the
// flock while it tears the sandbox down) so the <name>.lock file does
// not accumulate after the sandbox directory is gone.
//
// Safe to call while holding the flock: on Unix the flock is bound to
// the open fd, not the path, so unlinking the path leaves the caller's
// lock valid until its fd closes. A concurrent acquirer in the window
// between unlink and fd-close simply creates a fresh file and flocks
// that new inode — it never collides with the inode being removed.
//
// Best-effort: a removal failure is returned but callers typically
// ignore it (the lock file is harmless leftover, not corruption).
func RemoveLockFile(layout config.Layout, name string) error {
	path := layout.SandboxLockPath(name)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove lock file %s: %w", path, err)
	}
	return nil
}

// SweepStaleLocks removes orphaned per-sandbox lock files: <name>.lock
// entries in SandboxesDir whose sandbox directory no longer exists AND
// that no live process currently holds. It returns the names whose lock
// files were removed (or, under dryRun, would be removed).
//
// Liveness is proven by a non-blocking try-acquire: if the flock succeeds
// the file has no live holder and is safe to remove; if it would block,
// an operation is mid-flight (e.g. a `Create` that acquired the lock
// before creating the directory) and the file is left alone. This is why
// the sweep tolerates the "dir gone but lock held" window without racing
// a concurrent create/destroy.
//
// dryRun reports without removing.
func SweepStaleLocks(layout config.Layout, dryRun bool, out io.Writer) ([]string, error) {
	dir := layout.SandboxesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sandboxes dir %s: %w", dir, err)
	}

	var removed []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".lock")
		// A lock file beside an existing sandbox dir is its legitimate
		// companion — leave it.
		if _, statErr := os.Stat(layout.SandboxDir(name)); statErr == nil {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		release, acqErr := locking.AcquireNonBlocking(path)
		if acqErr != nil {
			// ErrWouldBlock (live holder) or a genuine open error — skip.
			continue
		}
		if dryRun {
			release()
			removed = append(removed, name)
			continue
		}
		// Remove while holding the flock (safe — the flock is bound to our
		// open fd, not the path), then release.
		rmErr := os.Remove(path)
		release()
		if rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			// Surface, don't swallow: a lock we can't remove keeps blocking its
			// sandbox name, so the user needs to know why it persists. Matches
			// the backend prunes' warn-and-skip convention.
			fmt.Fprintf(out, "Warning: could not remove stale lock %s: %v\n", path, rmErr) //nolint:errcheck // best-effort progress
			continue
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// newLockedError reads the lock file's recorded holder PID,
// classifies aliveness, and returns a *SandboxLockedError.
func newLockedError(layout config.Layout, name string) error {
	path := layout.SandboxLockPath(name)
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

// clearHolderPID truncates the lock file to zero bytes on release so
// a future reader of a stale lock file doesn't see a misleading PID.
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
	data, err := os.ReadFile(path) //nolint:gosec // path is layout-derived
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
// host. Implementation: syscall.Kill(pid, 0) — ESRCH means dead,
// EPERM means alive (but not owned by us; still counts as alive),
// no error means alive.
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
