package workspace

import (
	"os"
	"path/filepath"
	"testing"

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
