// ABOUTME: No-op sandbox locking for Windows. Windows/WSL is not yet fully
// supported; concurrent operations on the same sandbox name may produce
// errors but will not corrupt state.

//go:build windows

package store

import (
	"io"

	"github.com/kstenerud/yoloai/internal/config"
)

// AcquireLock is a no-op on Windows.
func AcquireLock(_ config.Layout, _ string) (func(), error) {
	return func() {}, nil
}

// acquireMultiLock is a no-op on Windows.
func acquireMultiLock(_ config.Layout, _ ...string) (func(), error) {
	return func() {}, nil
}

// ForceUnlock is a no-op on Windows — there is no lock file to clear
// (AcquireLock is itself a no-op on this platform). Always returns
// (cleared=false, err=nil).
func ForceUnlock(_ config.Layout, _ string) (cleared bool, err error) {
	return false, nil
}

// RemoveLockFile is a no-op on Windows — AcquireLock never creates a
// lock file on this platform, so there is nothing to remove.
func RemoveLockFile(_ config.Layout, _ string) error {
	return nil
}

// SweepStaleLocks is a no-op on Windows — AcquireLock never creates lock
// files on this platform, so there are none to sweep.
func SweepStaleLocks(_ config.Layout, _ bool, _ io.Writer) ([]string, error) {
	return nil, nil
}
