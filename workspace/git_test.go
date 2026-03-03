package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NewGitCmd tests

func TestNewGitCmd_DisablesHooks(t *testing.T) {
	cmd := NewGitCmd("/tmp", "status")
	// Should contain -c core.hooksPath=/dev/null
	args := cmd.Args
	assert.Contains(t, args, "-c")
	assert.Contains(t, args, "core.hooksPath=/dev/null")
}

func TestNewGitCmd_SetsDirectory(t *testing.T) {
	cmd := NewGitCmd("/some/dir", "log", "--oneline")
	args := cmd.Args
	assert.Contains(t, args, "-C")
	assert.Contains(t, args, "/some/dir")
	assert.Contains(t, args, "log")
	assert.Contains(t, args, "--oneline")
}

// RunGitCmd tests

func TestRunGitCmd_Success(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	err := RunGitCmd(dir, "status")
	assert.NoError(t, err)
}

func TestRunGitCmd_Failure(t *testing.T) {
	dir := t.TempDir()
	// Not a git repo -- should fail
	err := RunGitCmd(dir, "log")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "git log")
}

// HeadSHA tests

func TestHeadSHA_ValidRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "content")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha, err := HeadSHA(dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)
}

func TestHeadSHA_NoCommits(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Empty repo with no commits -- rev-parse HEAD should fail
	_, err := HeadSHA(dir)
	assert.Error(t, err)
}

func TestHeadSHA_NotGitRepo(t *testing.T) {
	_, err := HeadSHA(t.TempDir())
	assert.Error(t, err)
}

// StageUntracked tests

func TestStageUntracked_NewFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "a.txt", "a")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	// Add new untracked file
	writeTestFile(t, dir, "b.txt", "b")

	require.NoError(t, StageUntracked(dir))

	// Verify it's staged
	cmd := NewGitCmd(dir, "diff", "--cached", "--name-only")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "b.txt")
}

func TestStageUntracked_EmptyRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// No files to stage -- should succeed without error
	assert.NoError(t, StageUntracked(dir))
}

func TestStageUntracked_DeletedFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "a.txt", "a")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	// Delete a tracked file
	require.NoError(t, os.Remove(filepath.Join(dir, "a.txt")))

	require.NoError(t, StageUntracked(dir))

	// Verify deletion is staged
	cmd := NewGitCmd(dir, "diff", "--cached", "--name-only")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "a.txt")
}
