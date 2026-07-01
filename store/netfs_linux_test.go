// ABOUTME: Unit tests for the Linux network-filesystem magic-number classifier.
// ABOUTME: Validates that isNetworkFilesystemMagic correctly classifies known FS types.

//go:build linux

package store

import "testing"

// TestIsNetworkFilesystemMagic covers the complete known-magic table plus a
// representative set of local filesystem magic numbers. This is a pure
// function test — no mounts, no I/O.
func TestIsNetworkFilesystemMagic(t *testing.T) {
	t.Parallel()

	networkCases := []struct {
		name  string
		magic int64
	}{
		{"NFS", 0x6969},
		{"SMB/CIFS", 0xFF534D42},
		{"SMB2", 0xFE534D42},
		{"9P", 0x01021997},
		{"AFS", 0x5346414F},
		{"FUSE", 0x65735546},
	}
	for _, tc := range networkCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !isNetworkFilesystemMagic(tc.magic) {
				t.Errorf("isNetworkFilesystemMagic(0x%X) = false, want true for %s", tc.magic, tc.name)
			}
		})
	}

	localCases := []struct {
		name  string
		magic int64
	}{
		// Common local filesystems — flock(2) is reliable on all of these.
		{"ext4", 0xEF53},
		{"XFS", 0x58465342},
		{"Btrfs", 0x9123683E},
		{"tmpfs", 0x01021994},
		{"ext2/ext3", 0xEF53}, // same as ext4 magic; ext family shares it
	}
	for _, tc := range localCases {
		t.Run(tc.name+"_local", func(t *testing.T) {
			t.Parallel()
			if isNetworkFilesystemMagic(tc.magic) {
				t.Errorf("isNetworkFilesystemMagic(0x%X) = true, want false for local FS %s", tc.magic, tc.name)
			}
		})
	}
}
