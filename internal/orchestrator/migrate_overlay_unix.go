//go:build !windows

// ABOUTME: Unix implementation of fileOwnerUID for the overlay-flatten migrator's
// ABOUTME: host-ownership preflight (reads the stat uid of a sandbox entry).

package orchestrator

import (
	"io/fs"
	"syscall"
)

// fileOwnerUID returns the owning uid of info and whether it could be
// determined. On unix it reads the stat uid; overlay sandboxes only exist on a
// Linux kernel, so this is the real path.
func fileOwnerUID(info fs.FileInfo) (int, bool) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), true
	}
	return 0, false
}
