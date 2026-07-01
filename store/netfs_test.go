// ABOUTME: Tests for warn-once network-FS alerting and local-filesystem negative case.
// ABOUTME: Uses fake dir paths so no real NFS mount is needed to test dedup semantics.

//go:build !windows

package store

import "testing"

// TestWarnNetworkFSOnce_FiresExactlyOnce verifies that warnNetworkFSOnce calls
// the warn function exactly once no matter how many times it is called with the
// same directory path. Repeated acquisitions must not produce repeated warnings.
func TestWarnNetworkFSOnce_FiresExactlyOnce(t *testing.T) {
	t.Parallel()

	// Use a unique fake path so this test does not share state with others
	// in the package-level netFSWarnedDirs map.
	dir := "fake-nfs://" + t.Name()
	var count int
	capture := func(_, _ string) { count++ }

	for range 5 {
		warnNetworkFSOnce(dir, "NFS", capture)
	}
	if count != 1 {
		t.Errorf("warnNetworkFSOnce called warnFn %d times, want exactly 1", count)
	}
}

// TestWarnNetworkFSOnce_DifferentDirsAreIndependent verifies that two distinct
// directory paths each trigger their own independent one-time warning. The dedup
// key is the full path, so separate paths warn separately.
func TestWarnNetworkFSOnce_DifferentDirsAreIndependent(t *testing.T) {
	t.Parallel()

	dir1 := "fake-nfs://" + t.Name() + "/alpha"
	dir2 := "fake-nfs://" + t.Name() + "/beta"
	counts := map[string]int{}
	capture := func(d, _ string) { counts[d]++ }

	for range 3 {
		warnNetworkFSOnce(dir1, "NFS", capture)
		warnNetworkFSOnce(dir2, "SMB/CIFS", capture)
	}
	if counts[dir1] != 1 {
		t.Errorf("dir1 warned %d times, want exactly 1", counts[dir1])
	}
	if counts[dir2] != 1 {
		t.Errorf("dir2 warned %d times, want exactly 1", counts[dir2])
	}
}

// TestNetworkFilesystemName_LocalFSNoWarning verifies that a real local
// filesystem (t.TempDir) is not classified as a network filesystem.
// This is the negative case: no warning should fire for local data dirs.
func TestNetworkFilesystemName_LocalFSNoWarning(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	name, isNet := networkFilesystemName(dir)
	if isNet {
		t.Errorf("networkFilesystemName(%q) = (%q, true), want (_, false) for local FS", dir, name)
	}
}
