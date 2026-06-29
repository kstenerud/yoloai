// ABOUTME: Darwin filesystem-type classifier using statfs(2) Fstypename string.
// ABOUTME: Identifies NFS/SMB/WebDAV/AFP filesystems where flock is unreliable.

//go:build darwin

package store

import "golang.org/x/sys/unix"

// networkFSTypeNames maps macOS statfs Fstypename strings to human-readable
// labels for filesystems where POSIX advisory locking (flock) is unreliable.
// macOS VirtioFS and SMB are common in corporate/university environments where
// home directories are network-mounted.
var networkFSTypeNames = map[string]string{
	"nfs":    "NFS",
	"smbfs":  "SMB/CIFS",
	"webdav": "WebDAV",
	"afpfs":  "AFP",
}

// networkFilesystemName calls statfs(2) on path and returns a short
// human-readable filesystem name (e.g. "NFS", "SMB/CIFS") when the filesystem
// is a known network type. Returns ("", false) for local filesystems or on
// any statfs error — probe failures are silently swallowed so they never
// block a lock acquisition.
func networkFilesystemName(path string) (string, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return "", false
	}
	typeName := unix.ByteSliceToString(st.Fstypename[:])
	name, ok := networkFSTypeNames[typeName]
	return name, ok
}
