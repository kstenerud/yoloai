// ABOUTME: Single-filesystem preflight — migration hard-refuses unless every
// ABOUTME: live dir and its scratch share one local FS, so every rename is atomic.
package migrate

import (
	"fmt"
	"os"
	"syscall"
)

// SameFilesystem reports whether every given path resides on the same local
// filesystem (device id). Migration requires it (crash-safe-migration decision
// 4): every mv/rename must be atomic, so the scratch dir and the live dirs
// cannot straddle a mount boundary — an EXDEV rename silently degrades to a
// copy+delete that can leave a partial sentinel dir. This also subsumes the
// network-FS refusal (flock is unreliable on NFS/SMB and meaningless across a
// synced root like Dropbox/iCloud). Every path must exist. The error names the
// escape (relocate ~/.yoloai onto a single local FS).
func SameFilesystem(paths ...string) error {
	if len(paths) < 2 {
		return nil
	}
	var dev0 uint64
	for i, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("stat %s: %w", p, err)
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("cannot determine the filesystem of %s", p)
		}
		dev := uint64(st.Dev) //nolint:unconvert // Stat_t.Dev is int32 on darwin, uint64 on linux
		if i == 0 {
			dev0 = dev
			continue
		}
		if dev != dev0 {
			return fmt.Errorf("%s and %s are on different filesystems; migration requires a single local filesystem — relocate ~/.yoloai onto one and retry", paths[0], p)
		}
	}
	return nil
}
