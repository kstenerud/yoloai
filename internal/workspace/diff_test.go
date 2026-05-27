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
	out, err := CopyDiff(dir, sha, nil, false, false)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestCopyDiff_WithChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)
	writeTestFile(t, dir, "file.txt", "hello world\n")

	out, err := CopyDiff(dir, sha, nil, false, false)
	require.NoError(t, err)
	assert.Contains(t, out, "hello world")
}

func TestCopyDiff_WithStat(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)
	writeTestFile(t, dir, "file.txt", "hello world\n")

	out, err := CopyDiff(dir, sha, nil, true, false)
	require.NoError(t, err)
	assert.Contains(t, out, "file.txt")
	assert.Contains(t, out, "1 file changed")
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

	out, err := CopyDiff(dir, sha, []string{"a.txt"}, false, false)
	require.NoError(t, err)
	assert.Contains(t, out, "a.txt")
	assert.NotContains(t, out, "b.txt")
}

func TestCopyDiff_NewUntrackedFile(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "existing.txt", "existing\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)
	writeTestFile(t, dir, "new.txt", "new content\n")

	out, err := CopyDiff(dir, sha, nil, false, false)
	require.NoError(t, err)
	// StageUntracked should pick up the new file.
	assert.Contains(t, out, "new.txt")
}

// --- RWDiff ---

// :rw pointed at a non-git tree was previously a special
// "informational" DiffResult. Q-U collapses it to an empty string —
// callers treat non-git :rw the same as "no changes".
func TestRWDiff_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	out, err := RWDiff(dir, nil, false, false)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestRWDiff_NoChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	out, err := RWDiff(dir, nil, false, false)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestRWDiff_WithChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "file.txt", "hello world\n")

	out, err := RWDiff(dir, nil, false, false)
	require.NoError(t, err)
	assert.Contains(t, out, "hello world")
}

func TestRWDiff_WithStat(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "file.txt", "hello world\n")

	out, err := RWDiff(dir, nil, true, false)
	require.NoError(t, err)
	assert.Contains(t, out, "file.txt")
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

	out, err := RWDiff(dir, []string{"a.txt"}, false, false)
	require.NoError(t, err)
	assert.Contains(t, out, "a.txt")
	assert.NotContains(t, out, "b.txt")
}

// headSHA returns the HEAD SHA of a git repo.
func headSHA(t *testing.T, dir string) string {
	t.Helper()
	sha, err := HeadSHA(dir)
	require.NoError(t, err)
	return sha
}
