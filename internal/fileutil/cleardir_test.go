// ABOUTME: Tests that ClearDirContents empties a directory without replacing
// ABOUTME: it — the inode identity a live bind mount depends on (DF149).
package fileutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The whole point of the helper: a caller's bind mount resolves to this inode,
// so clearing must not swap it for a fresh one. RemoveAll+MkdirAll passes every
// contents-based assertion and fails this one.
func TestClearDirContents_PreservesInode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "files")
	require.NoError(t, MkdirAllPerm(dir, 0750))

	before, err := os.Stat(dir)
	require.NoError(t, err)

	require.NoError(t, ClearDirContents(dir, 0750))

	after, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after),
		"a bind mount resolves to this inode; replacing it strands the guest")
}

func TestClearDirContents_RemovesFilesAndSubtrees(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	require.NoError(t, MkdirAllPerm(dir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "top.txt"), []byte("x"), 0600))
	require.NoError(t, MkdirAllPerm(filepath.Join(dir, "nested", "deep"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nested", "deep", "y.txt"), []byte("y"), 0600))

	require.NoError(t, ClearDirContents(dir, 0750))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "both the file and the nested subtree go")
}

// Callers invoke this on a first run too, where the directory may not exist yet
// — it must create it rather than error, matching the MkdirAll it replaced.
func TestClearDirContents_CreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")

	require.NoError(t, ClearDirContents(dir, 0750))

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, os.FileMode(0750), info.Mode().Perm(),
		"perm is set explicitly to bypass the umask, as MkdirAllPerm does")
}
