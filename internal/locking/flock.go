// ABOUTME: Generic flock(2)-based file lock primitive — the sole place in
// ABOUTME: yoloai that calls unix.Flock. Higher-level callers compose this.

//go:build !windows

// Package locking provides yoloai's file-locking primitives. All
// flock(2) usage in the project is required to go through this
// package — domain-specific lockers (per-sandbox locks, base-image
// build locks) wrap these primitives with their own semantics
// (retry budgets, PID files, recovery UX) but never call
// unix.Flock directly.
//
// This package centralises all flock logic so the dance isn't duplicated
// across the sandbox store and the per-backend base-image locks.
package locking

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// ErrWouldBlock is returned by AcquireNonBlocking when another
// process currently holds the lock. Distinguishable via errors.Is —
// callers that want retry / typed-error UX branch on this.
var ErrWouldBlock = errors.New("locking: file is locked by another process")

// AcquireBlocking opens (or creates) the lockfile at path and
// acquires an exclusive flock, BLOCKING until it can. Returns a
// release function the caller must defer.
//
// Use this for "wait for the other holder to finish" semantics —
// e.g. base-image build locks, where the right user experience is
// "the other build is in progress; we'll use its result when it's
// done."
//
// The lockfile is created if missing (mode 0600) and its parent
// directory is created if missing (mode 0750). The release function
// flock-unlocks and closes the file; the file itself stays on disk
// (harmless — empty 0-byte advisory file).
func AcquireBlocking(path string) (release func(), err error) {
	f, err := openLockFile(path)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil { //nolint:gosec // G115: fd conversion is safe
		_ = f.Close()
		return nil, fmt.Errorf("acquire lock %s: %w", path, err)
	}
	return makeRelease(f), nil
}

// AcquireNonBlocking opens (or creates) the lockfile at path and
// tries to acquire an exclusive flock WITHOUT blocking. Returns
// ErrWouldBlock (via errors.Is) when another process currently holds
// the lock; returns other errors verbatim for genuine failures
// (file open, etc.).
//
// Use this for "fast-fail and let the caller decide what to do"
// semantics — e.g. per-sandbox locks where the caller wraps with a
// retry budget and a typed *SandboxLockedError on exhaustion.
func AcquireNonBlocking(path string) (release func(), err error) {
	f, err := openLockFile(path)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil { //nolint:gosec // G115: fd conversion is safe
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrWouldBlock
		}
		return nil, fmt.Errorf("acquire lock %s: %w", path, err)
	}
	return makeRelease(f), nil
}

// AcquireWithFile is the same as AcquireNonBlocking but returns the
// underlying *os.File so the caller can read/write to the lockfile
// content (e.g. writing a holder PID for contention diagnostics).
// The returned release function unlocks and closes the file; the
// caller MUST NOT close the file itself.
func AcquireWithFile(path string) (f *os.File, release func(), err error) {
	f, err = openLockFile(path)
	if err != nil {
		return nil, nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil { //nolint:gosec // G115: fd conversion is safe
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, nil, ErrWouldBlock
		}
		return nil, nil, fmt.Errorf("acquire lock %s: %w", path, err)
	}
	return f, makeRelease(f), nil
}

// openLockFile opens (or creates) the lockfile, creating the parent
// directory if needed.
func openLockFile(path string) (*os.File, error) {
	if err := fileutil.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	f, err := fileutil.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // path is callsite-controlled and validated upstream
	if err != nil {
		return nil, fmt.Errorf("open lockfile %s: %w", path, err)
	}
	return f, nil
}

// makeRelease returns a release closure that flock-unlocks and
// closes the file. Best-effort; ignores errors on release.
func makeRelease(f *os.File) func() {
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN) //nolint:gosec // same fd
		_ = f.Close()
	}
}
