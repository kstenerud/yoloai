// ABOUTME: No-op sandbox locking for Windows. Windows/WSL is not yet fully
// supported; concurrent operations on the same sandbox name may produce
// errors but will not corrupt state.

//go:build windows

package sandbox

import "github.com/kstenerud/yoloai/config"

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
