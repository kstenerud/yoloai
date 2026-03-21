// ABOUTME: No-op sandbox locking for Windows. Windows/WSL is not yet fully
// supported; concurrent operations on the same sandbox name may produce
// errors but will not corrupt state.

//go:build windows

package sandbox

// acquireLock is a no-op on Windows.
func acquireLock(_ string) (func(), error) {
	return func() {}, nil
}

// acquireMultiLock is a no-op on Windows.
func acquireMultiLock(_ ...string) (func(), error) {
	return func() {}, nil
}
