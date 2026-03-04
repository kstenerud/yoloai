package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- CopyDiff ---

func TestCopyDiff_NoChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)
	result, err := CopyDiff(dir, sha, nil, false)
	require.NoError(t, err)
	assert.True(t, result.Empty)
	assert.Equal(t, "", result.Output)
	assert.Equal(t, "copy", result.Mode)
	assert.Equal(t, dir, result.WorkDir)
}

func TestCopyDiff_WithChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)

	// Make a change
	writeTestFile(t, dir, "file.txt", "hello world\n")

	result, err := CopyDiff(dir, sha, nil, false)
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Contains(t, result.Output, "hello world")
	assert.Equal(t, "copy", result.Mode)
}

func TestCopyDiff_WithStat(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)

	writeTestFile(t, dir, "file.txt", "hello world\n")

	result, err := CopyDiff(dir, sha, nil, true)
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Contains(t, result.Output, "file.txt")
	// Stat output shows change count
	assert.Contains(t, result.Output, "1 file changed")
}

func TestCopyDiff_WithPathFilter(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "a.txt", "a\n")
	writeTestFile(t, dir, "b.txt", "b\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)

	writeTestFile(t, dir, "a.txt", "aaa\n")
	writeTestFile(t, dir, "b.txt", "bbb\n")

	// Only show changes to a.txt
	result, err := CopyDiff(dir, sha, []string{"a.txt"}, false)
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Contains(t, result.Output, "a.txt")
	assert.NotContains(t, result.Output, "b.txt")
}

func TestCopyDiff_NewUntrackedFile(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "existing.txt", "existing\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)

	// Add a new file
	writeTestFile(t, dir, "new.txt", "new content\n")

	result, err := CopyDiff(dir, sha, nil, false)
	require.NoError(t, err)
	assert.False(t, result.Empty)
	// StageUntracked should pick up the new file
	assert.Contains(t, result.Output, "new.txt")
}

// --- RWDiff ---

func TestRWDiff_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	result, err := RWDiff(dir, nil, false)
	require.NoError(t, err)
	assert.True(t, result.Empty)
	assert.Contains(t, result.Output, "not a git repository")
	assert.Equal(t, "rw", result.Mode)
}

func TestRWDiff_NoChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	result, err := RWDiff(dir, nil, false)
	require.NoError(t, err)
	assert.True(t, result.Empty)
	assert.Equal(t, "rw", result.Mode)
}

func TestRWDiff_WithChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "file.txt", "hello world\n")

	result, err := RWDiff(dir, nil, false)
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Contains(t, result.Output, "hello world")
	assert.Equal(t, "rw", result.Mode)
}

func TestRWDiff_WithStat(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "file.txt", "hello world\n")

	result, err := RWDiff(dir, nil, true)
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Contains(t, result.Output, "file.txt")
}

func TestRWDiff_WithPathFilter(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "a.txt", "a\n")
	writeTestFile(t, dir, "b.txt", "b\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "a.txt", "aaa\n")
	writeTestFile(t, dir, "b.txt", "bbb\n")

	result, err := RWDiff(dir, []string{"a.txt"}, false)
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Contains(t, result.Output, "a.txt")
	assert.NotContains(t, result.Output, "b.txt")
}

// headSHA returns the HEAD SHA of a git repo.
func headSHA(t *testing.T, dir string) string {
	t.Helper()
	sha, err := HeadSHA(dir)
	require.NoError(t, err)
	return sha
}
