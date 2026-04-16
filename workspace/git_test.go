package workspace

import (
	"os"
	"path/filepath"
	"strings"
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

// BaselineUncommittedChanges tests

func TestBaselineUncommittedChanges_DirtyTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "original\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	originalSHA, err := HeadSHA(dir)
	require.NoError(t, err)

	// Make the tree dirty
	writeTestFile(t, dir, "file.txt", "modified\n")
	writeTestFile(t, dir, "new.txt", "untracked\n")

	newSHA, err := BaselineUncommittedChanges(dir)
	require.NoError(t, err)
	assert.NotEqual(t, originalSHA, newSHA, "should have created a new commit")
	assert.Len(t, newSHA, 40)

	// Verify the pre-session commit message
	cmd := NewGitCmd(dir, "log", "-1", "--format=%s")
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "yoloai: pre-session state", strings.TrimSpace(string(out)))
}

func TestBaselineUncommittedChanges_CleanTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "content\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	originalSHA, err := HeadSHA(dir)
	require.NoError(t, err)

	newSHA, err := BaselineUncommittedChanges(dir)
	require.NoError(t, err)
	assert.Equal(t, originalSHA, newSHA, "clean tree should not create a new commit")
}

// Baseline tests

func TestBaseline_CreatesRepoWithCommit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")

	sha, err := Baseline(dir)
	require.NoError(t, err)

	// SHA should be a 40-character hex string
	assert.Len(t, sha, 40)
	assert.Regexp(t, `^[0-9a-f]{40}$`, sha)

	// Verify git log contains the baseline commit message
	cmd := NewGitCmd(dir, "log", "--oneline")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "yoloai baseline")
}

func TestBaseline_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	sha, err := Baseline(dir)
	require.NoError(t, err)

	// Even with no files, --allow-empty should produce a valid commit
	assert.Len(t, sha, 40)
	assert.Regexp(t, `^[0-9a-f]{40}$`, sha)
}

func TestBaseline_SetsUserConfig(t *testing.T) {
	dir := t.TempDir()

	_, err := Baseline(dir)
	require.NoError(t, err)

	// Verify user.email was set to yoloai@localhost
	cmd := NewGitCmd(dir, "config", "user.email")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "yoloai@localhost", strings.TrimSpace(string(output)))
}
