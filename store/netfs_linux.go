// ABOUTME: Linux filesystem-type classifier using statfs(2) f_type magic numbers.
// ABOUTME: Identifies NFS/CIFS/SMB2/9P/AFS/FUSE filesystems where flock is unreliable.

//go:build linux

package store

import "golang.org/x/sys/unix"

// networkMagics maps Linux statfs(2) f_type magic numbers to human-readable
// names for filesystems where POSIX advisory locking (flock) is known to be
// unreliable or non-atomic across hosts:
//
//   - NFS   0x6969:     flock semantics on NFS differ by server version and mount options;
//     cross-host exclusion is not guaranteed even on NFSv4.
//   - SMB   0xFF534D42: Windows servers honour byte-range locks, not POSIX flocks.
//   - SMB2  0xFE534D42: same server-side hazard as legacy SMB/CIFS.
//   - 9P    0x01021997: lock semantics depend on the 9P server implementation.
//   - AFS   0x5346414F: AFS uses its own distributed lock manager; POSIX flocks are no-ops.
//   - FUSE  0x65735546: reliability depends on the FUSE backing server; includes
//     cloud mounts (s3fs, rclone) and network-backed overlay filesystems.
var networkMagics = map[int64]string{
	0x6969:     "NFS",
	0xFF534D42: "SMB/CIFS",
	0xFE534D42: "SMB2",
	0x01021997: "9P",
	0x5346414F: "AFS",
	0x65735546: "FUSE",
}

// networkMagicName maps a statfs(2) f_type magic to its short name, reporting
// false for anything not in the table. It is the pure half of the probe — no
// I/O, no side effects — which is what makes it unit-testable against a literal
// magic number, and networkFilesystemName is its only caller.
//
// The split earns its keep only because production goes through it. It
// previously sat beside a networkFilesystemName that repeated the map lookup
// inline, so the unit tests exercised a parallel copy of the classifier rather
// than the code that runs — a divergence nothing would have caught, since both
// copies were right (DF108).
func networkMagicName(magic int64) (string, bool) {
	name, ok := networkMagics[magic]
	return name, ok
}

// networkFilesystemName calls statfs(2) on path and returns a short
// human-readable filesystem name (e.g. "NFS", "FUSE") when the filesystem
// is a known network type. Returns ("", false) for local filesystems or on
// any statfs error — probe failures are silently swallowed so they never
// block a lock acquisition.
func networkFilesystemName(path string) (string, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return "", false
	}
	return networkMagicName(st.Type)
}
