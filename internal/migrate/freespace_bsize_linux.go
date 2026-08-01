//go:build linux

// ABOUTME: Linux Statfs_t.Bsize accessor — it is int64 there, so the widen to
// ABOUTME: uint64 is a real conversion and gosec is right to want it justified.
package migrate

import "syscall"

// statfsBlockSize returns the filesystem block size as uint64. On linux
// Statfs_t.Bsize is int64, so this is a genuine widen; a block size is never
// negative, so the sign extension G115 warns about cannot happen.
func statfsBlockSize(st *syscall.Statfs_t) uint64 { return uint64(st.Bsize) } //nolint:gosec // G115: Bsize is non-negative; widen is safe
