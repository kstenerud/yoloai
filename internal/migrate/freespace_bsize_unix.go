//go:build !windows && !linux

// ABOUTME: Non-linux unix Statfs_t.Bsize accessor — it is uint32 on darwin, so
// ABOUTME: the widen is between unsigned types and needs no suppression.
package migrate

import "syscall"

// statfsBlockSize returns the filesystem block size as uint64. On darwin (and
// other non-linux unixes) Statfs_t.Bsize is an unsigned narrower type, so the
// widen is unconditionally safe and carries no linter directive — which is the
// reason this is split from the linux file rather than sharing one suppression
// that would be unused on half the platforms it compiled for.
func statfsBlockSize(st *syscall.Statfs_t) uint64 { return uint64(st.Bsize) }
