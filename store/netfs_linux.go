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

// isNetworkFilesystemMagic reports whether magic is a known network or
// FUSE filesystem magic number. This is a pure classifier — no I/O, no
// side effects — making it the primary unit-test target for FS detection.
func isNetworkFilesystemMagic(magic int64) bool {
	_, ok := networkMagics[magic]
	return ok
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
	name, ok := networkMagics[st.Type]
	return name, ok
}
