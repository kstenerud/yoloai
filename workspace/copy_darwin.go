//go:build darwin

package workspace

import "golang.org/x/sys/unix"

// cloneDir uses macOS clonefile(2) to create a copy-on-write clone of the
// entire directory tree. This is near-instant on APFS. Any error (unsupported
// filesystem, cross-device, destination exists, etc.) triggers fallback to the
// regular walk-based copy.
func cloneDir(src, dst string) error {
	return unix.Clonefile(src, dst, 0)
}
