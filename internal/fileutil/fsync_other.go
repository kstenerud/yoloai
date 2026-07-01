//go:build !darwin

// ABOUTME: non-darwin fullSync is a plain fsync; F_FULLFSYNC is macOS-only.
package fileutil

import "os"

// fullSync flushes f to stable storage. On Linux fsync(2) already requests a
// device-cache flush, so plain Sync is sufficient; F_FULLFSYNC is darwin-only.
func fullSync(f *os.File) error {
	return f.Sync()
}
