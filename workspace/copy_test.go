package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CopyDir tests

func TestCopyDir_Basic(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, src, "file.txt", "hello")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0750))
	writeTestFile(t, src, "sub/nested.txt", "world")

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	content, err := os.ReadFile(filepath.Join(dst, "file.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))

	content, err = os.ReadFile(filepath.Join(dst, "sub", "nested.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "world", string(content))
}

func TestCopyDir_SourceMissing(t *testing.T) {
	err := CopyDir("/nonexistent/path", filepath.Join(t.TempDir(), "dst"))
	assert.Error(t, err)
}

func TestCopyDir_BrokenSymlink(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.Symlink("/nonexistent/target", filepath.Join(src, "broken")))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	link, err := os.Readlink(filepath.Join(dst, "broken"))
	require.NoError(t, err)
	assert.Equal(t, "/nonexistent/target", link)
}

func TestCopyDir_ValidSymlink(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, src, "real.txt", "content")
	require.NoError(t, os.Symlink("real.txt", filepath.Join(src, "link.txt")))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	// Must be a symlink, not a regular file copy.
	fi, err := os.Lstat(filepath.Join(dst, "link.txt"))
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "should be a symlink")

	link, err := os.Readlink(filepath.Join(dst, "link.txt"))
	require.NoError(t, err)
	assert.Equal(t, "real.txt", link)
}

func TestCopyDir_RelativeSymlink(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0750))
	writeTestFile(t, src, "sub/target.txt", "data")
	require.NoError(t, os.Symlink("sub/target.txt", filepath.Join(src, "rel")))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	link, err := os.Readlink(filepath.Join(dst, "rel"))
	require.NoError(t, err)
	assert.Equal(t, "sub/target.txt", link)
}

func TestCopyDir_SymlinkToDirectory(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "realdir"), 0750))
	writeTestFile(t, src, "realdir/file.txt", "inside")
	require.NoError(t, os.Symlink("realdir", filepath.Join(src, "linkdir")))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	// linkdir should be a symlink, not a real directory.
	fi, err := os.Lstat(filepath.Join(dst, "linkdir"))
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "should be a symlink")

	link, err := os.Readlink(filepath.Join(dst, "linkdir"))
	require.NoError(t, err)
	assert.Equal(t, "realdir", link)
}

func TestCopyDir_PreservesPermissions(t *testing.T) {
	src := t.TempDir()
	f := filepath.Join(src, "exec.sh")
	require.NoError(t, os.WriteFile(f, []byte("#!/bin/sh"), 0755)) //nolint:gosec

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	fi, err := os.Stat(filepath.Join(dst, "exec.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0755), fi.Mode().Perm())
}

func TestCopyDir_PreservesModTime(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, src, "file.txt", "hello")
	past := time.Date(2020, 6, 15, 12, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(filepath.Join(src, "file.txt"), past, past))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	fi, err := os.Stat(filepath.Join(dst, "file.txt"))
	require.NoError(t, err)
	assert.True(t, fi.ModTime().Equal(past), "mod time should be preserved, got %v", fi.ModTime())
}

func TestCopyDir_EmptyDirectory(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "empty"), 0750))

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, CopyDir(src, dst))

	fi, err := os.Stat(filepath.Join(dst, "empty"))
	require.NoError(t, err)
	assert.True(t, fi.IsDir(), "empty dir should be preserved")
}

func TestCopyDir_SourceNotDirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(f, []byte("data"), 0600))

	err := CopyDir(f, filepath.Join(t.TempDir(), "dst"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")
}

// RemoveGitDirs tests

func TestRemoveGitDirs_RemovesGitDirectory(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0750))
	writeTestFile(t, dir, ".git/HEAD", "ref: refs/heads/main")

	require.NoError(t, RemoveGitDirs(dir))

	_, err := os.Stat(gitDir)
	assert.True(t, os.IsNotExist(err), ".git directory should be removed")
}

func TestRemoveGitDirs_RemovesNestedGit(t *testing.T) {
	dir := t.TempDir()
	subGitDir := filepath.Join(dir, "sub", ".git")
	require.NoError(t, os.MkdirAll(subGitDir, 0750))
	writeTestFile(t, dir, "sub/.git/HEAD", "ref: refs/heads/main")

	require.NoError(t, RemoveGitDirs(dir))

	_, err := os.Stat(subGitDir)
	assert.True(t, os.IsNotExist(err), "nested .git directory should be removed")
}

func TestRemoveGitDirs_RemovesGitFile(t *testing.T) {
	dir := t.TempDir()
	// Worktree-style .git file (not a directory)
	writeTestFile(t, dir, ".git", "gitdir: /some/other/path")

	require.NoError(t, RemoveGitDirs(dir))

	_, err := os.Stat(filepath.Join(dir, ".git"))
	assert.True(t, os.IsNotExist(err), ".git file should be removed")
}

func TestRemoveGitDirs_PreservesOtherFiles(t *testing.T) {
	dir := t.TempDir()
	// Create .git dir and non-git files
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0750))
	writeTestFile(t, dir, ".git/HEAD", "ref: refs/heads/main")
	writeTestFile(t, dir, "file.txt", "keep me")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0750))
	writeTestFile(t, dir, "sub/other.txt", "keep me too")

	require.NoError(t, RemoveGitDirs(dir))

	// .git should be gone
	_, err := os.Stat(filepath.Join(dir, ".git"))
	assert.True(t, os.IsNotExist(err), ".git directory should be removed")

	// Other files should be preserved
	content, err := os.ReadFile(filepath.Join(dir, "file.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "keep me", string(content))

	content, err = os.ReadFile(filepath.Join(dir, "sub", "other.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "keep me too", string(content))
}

func TestRemoveGitDirs_NoopWhenNoGit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0750))
	writeTestFile(t, dir, "sub/other.txt", "world")

	err := RemoveGitDirs(dir)
	assert.NoError(t, err)

	// All files should still be present
	content, err := os.ReadFile(filepath.Join(dir, "file.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))

	content, err = os.ReadFile(filepath.Join(dir, "sub", "other.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "world", string(content))
}
