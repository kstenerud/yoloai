// ABOUTME: Stub network-filesystem classifier for platforms other than Linux/Darwin.
// ABOUTME: Always reports "not a network filesystem"; probing is not implemented here.

//go:build !linux && !darwin

package store

// isNetworkFilesystemMagic is a stub on platforms other than Linux.
// Linux-specific f_type magic numbers have no meaning on these systems.
func isNetworkFilesystemMagic(_ int64) bool { return false }

// networkFilesystemName is a stub on platforms other than Linux and Darwin.
// Network filesystem detection is not implemented for this platform.
func networkFilesystemName(_ string) (string, bool) { return "", false }
