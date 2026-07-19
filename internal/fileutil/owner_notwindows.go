//go:build !windows

// ABOUTME: OwnerUID reads a path's owning uid from an already-stat'd FileInfo on
// every yoloai target (linux, darwin — st.Uid is uint32 on both), so an
// ownership audit can flag files the invoking user cannot delete without
// touching the filesystem twice.

package fileutil

import (
	"io/fs"
	"syscall"
)

// OwnerUID returns the uid that owns the file described by info. ok is true on
// platforms where file ownership is available (every yoloai target — linux and
// darwin both expose *syscall.Stat_t). Callers pass a FileInfo they already
// hold (WalkDir's DirEntry.Info(), or os.Lstat) so no extra stat is made.
func OwnerUID(info fs.FileInfo) (uid int, ok bool) {
	if st, isStat := info.Sys().(*syscall.Stat_t); isStat {
		return int(st.Uid), true
	}
	return 0, false
}
