//go:build windows

// ABOUTME: Windows stubs — flock(2) is Unix-only. Concurrent operations
// ABOUTME: may race on Windows; this matches sandbox/lock_windows.go's stance.

package locking

import (
	"errors"
	"os"
)

// ErrWouldBlock parity with the Unix build. Not actually returnable
// from the Windows stubs since they never fail, but the symbol must
// exist so callers that errors.Is against it compile.
var ErrWouldBlock = errors.New("locking: file is locked by another process")

// AcquireBlocking is a no-op on Windows. Returns a release function
// that does nothing.
func AcquireBlocking(_ string) (release func(), err error) {
	return func() {}, nil
}

// AcquireNonBlocking is a no-op on Windows. Returns a release
// function that does nothing.
func AcquireNonBlocking(_ string) (release func(), err error) {
	return func() {}, nil
}

// AcquireWithFile is a no-op on Windows. Returns nil for the file
// (callers that need the file handle for PID writing must guard
// against nil on Windows).
func AcquireWithFile(_ string) (f *os.File, release func(), err error) {
	return nil, func() {}, nil
}
