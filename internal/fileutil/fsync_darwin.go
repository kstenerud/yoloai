//go:build darwin

// ABOUTME: darwin fullSync uses F_FULLFSYNC so buffered data reaches permanent
// ABOUTME: storage; a plain fsync only flushes to the APFS device cache.
package fileutil

import (
	"os"

	"golang.org/x/sys/unix"
)

// fullSync flushes f all the way to stable storage. On darwin a plain fsync(2)
// only pushes data to the drive, whose own cache can still lose it on power
// loss; F_FULLFSYNC forces a full flush to permanent storage. Verified against
// the on-device man page (reflink-vs-hardlink research, macOS §A.5).
func fullSync(f *os.File) error {
	_, err := unix.FcntlInt(f.Fd(), unix.F_FULLFSYNC, 0)
	return err
}
