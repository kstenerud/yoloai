//go:build windows

// ABOUTME: OwnerUID stub for Windows — yoloai does not target Windows, but the
// package must compile there; POSIX uid ownership has no meaning, so ok is false.

package fileutil

import "io/fs"

// OwnerUID always reports ok=false on Windows: there is no POSIX uid to read, so
// an ownership audit finds nothing (which is correct — the sudo/root-owned-file
// hazard this guards against is a unix concept).
func OwnerUID(_ fs.FileInfo) (uid int, ok bool) {
	return 0, false
}
