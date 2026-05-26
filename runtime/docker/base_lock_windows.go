//go:build windows

// ABOUTME: Windows stub for AcquireBaseLock — no-op since flock(2) is
// ABOUTME: unavailable; the Docker backend's Setup proceeds without the
// ABOUTME: cross-process race protection on Windows (preexisting limitation).
package docker

// AcquireBaseLock is a no-op on Windows. Mirrors sandbox/lock_windows.go's
// general stance: file-based advisory locking via flock is unavailable,
// so concurrent base-image builds may race. Returns a noop release
// function so the call site in docker.go's Setup compiles uniformly.
func AcquireBaseLock(_ string) (func(), error) {
	return func() {}, nil
}
