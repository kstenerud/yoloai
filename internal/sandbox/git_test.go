package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newGitCmd tests

func TestNewGitCmd_DisablesHooks(t *testing.T) {
	cmd := newGitCmd("/tmp", "status")
	// Should contain -c core.hooksPath=/dev/null
	args := cmd.Args
	assert.Contains(t, args, "-c")
	assert.Contains(t, args, "core.hooksPath=/dev/null")
}

func TestNewGitCmd_SetsDirectory(t *testing.T) {
	cmd := newGitCmd("/some/dir", "log", "--oneline")
	args := cmd.Args
	assert.Contains(t, args, "-C")
	assert.Contains(t, args, "/some/dir")
	assert.Contains(t, args, "log")
	assert.Contains(t, args, "--oneline")
}

// runGitCmd tests

func TestRunGitCmd_Success(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	err := runGitCmd(dir, "status")
	assert.NoError(t, err)
}

func TestRunGitCmd_Failure(t *testing.T) {
	dir := t.TempDir()
	// Not a git repo — should fail
	err := runGitCmd(dir, "log")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "git log")
}

// gitHeadSHA tests

func TestGitHeadSHA_ValidRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "content")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha, err := gitHeadSHA(dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)
}

func TestGitHeadSHA_NoCommits(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Empty repo with no commits — rev-parse HEAD should fail
	_, err := gitHeadSHA(dir)
	assert.Error(t, err)
}

func TestGitHeadSHA_NotGitRepo(t *testing.T) {
	_, err := gitHeadSHA(t.TempDir())
	assert.Error(t, err)
}

// stageUntracked tests

func TestStageUntracked_NewFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "a.txt", "a")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	// Add new untracked file
	writeTestFile(t, dir, "b.txt", "b")

	require.NoError(t, stageUntracked(dir))

	// Verify it's staged
	cmd := newGitCmd(dir, "diff", "--cached", "--name-only")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "b.txt")
}

func TestStageUntracked_EmptyRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// No files to stage — should succeed without error
	assert.NoError(t, stageUntracked(dir))
}

func TestStageUntracked_DeletedFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "a.txt", "a")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	// Delete a tracked file
	require.NoError(t, os.Remove(filepath.Join(dir, "a.txt")))

	require.NoError(t, stageUntracked(dir))

	// Verify deletion is staged
	cmd := newGitCmd(dir, "diff", "--cached", "--name-only")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "a.txt")
}
