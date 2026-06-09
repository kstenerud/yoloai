package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NewGitCmdWithEnv tests

func TestNewGitCmdWithEnv_DisablesHooks(t *testing.T) {
	cmd := NewGitCmdWithEnv(testEnv(), "/tmp", "status")
	// Should contain -c core.hooksPath=/dev/null
	args := cmd.Args
	assert.Contains(t, args, "-c")
	assert.Contains(t, args, "core.hooksPath=/dev/null")
}

func TestNewGitCmdWithEnv_SetsDirectory(t *testing.T) {
	cmd := NewGitCmdWithEnv(testEnv(), "/some/dir", "log", "--oneline")
	args := cmd.Args
	assert.Contains(t, args, "-C")
	assert.Contains(t, args, "/some/dir")
	assert.Contains(t, args, "log")
	assert.Contains(t, args, "--oneline")
}

// RunGitCmdWithEnv tests

func TestRunGitCmdWithEnv_Success(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	err := RunGitCmdWithEnv(testEnv(), dir, "status")
	assert.NoError(t, err)
}

func TestRunGitCmdWithEnv_Failure(t *testing.T) {
	dir := t.TempDir()
	// Not a git repo -- should fail
	err := RunGitCmdWithEnv(testEnv(), dir, "log")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "git log")
}

// HeadSHAWithEnv tests

func TestHeadSHAWithEnv_ValidRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "content")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha, err := HeadSHAWithEnv(testEnv(), dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)
}

func TestHeadSHAWithEnv_NoCommits(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Empty repo with no commits -- rev-parse HEAD should fail
	_, err := HeadSHAWithEnv(testEnv(), dir)
	assert.Error(t, err)
}

func TestHeadSHAWithEnv_NotGitRepo(t *testing.T) {
	_, err := HeadSHAWithEnv(testEnv(), t.TempDir())
	assert.Error(t, err)
}

// StageUntrackedWithEnv tests

func TestStageUntrackedWithEnv_NewFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "a.txt", "a")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	// Add new untracked file
	writeTestFile(t, dir, "b.txt", "b")

	require.NoError(t, StageUntrackedWithEnv(testEnv(), dir))

	// Verify it's staged
	cmd := NewGitCmdWithEnv(testEnv(), dir, "diff", "--cached", "--name-only")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "b.txt")
}

func TestStageUntrackedWithEnv_EmptyRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// No files to stage -- should succeed without error
	assert.NoError(t, StageUntrackedWithEnv(testEnv(), dir))
}

func TestStageUntrackedWithEnv_DeletedFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "a.txt", "a")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	// Delete a tracked file
	require.NoError(t, os.Remove(filepath.Join(dir, "a.txt")))

	require.NoError(t, StageUntrackedWithEnv(testEnv(), dir))

	// Verify deletion is staged
	cmd := NewGitCmdWithEnv(testEnv(), dir, "diff", "--cached", "--name-only")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "a.txt")
}

func TestStageUntrackedWithEnv_RetriesOnIndexLock(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "a.txt", "a")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")
	writeTestFile(t, dir, "b.txt", "b")

	// Simulate a concurrent process holding index.lock briefly
	lockPath := filepath.Join(dir, ".git", "index.lock")
	require.NoError(t, os.WriteFile(lockPath, []byte{}, 0o600))
	go func() {
		// Remove the lock after a short delay, within the retry window
		time.Sleep(150 * time.Millisecond)
		_ = os.Remove(lockPath)
	}()

	require.NoError(t, StageUntrackedWithEnv(testEnv(), dir))

	cmd := NewGitCmdWithEnv(testEnv(), dir, "diff", "--cached", "--name-only")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "b.txt")
}

// IsIndexLocked tests

func TestIsIndexLocked_DetectsLockError(t *testing.T) {
	err := fmt.Errorf("git [add -A]: exit status 128: fatal: Unable to create '.git/index.lock': File exists")
	assert.True(t, IsIndexLocked(err))
}

func TestIsIndexLocked_NilIsNotLocked(t *testing.T) {
	assert.False(t, IsIndexLocked(nil))
}

func TestIsIndexLocked_OtherErrorIsNotLocked(t *testing.T) {
	assert.False(t, IsIndexLocked(fmt.Errorf("some other git error")))
}

// BaselineUncommittedChangesWithEnv tests

func TestBaselineUncommittedChangesWithEnv_DirtyTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "original\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	originalSHA, err := HeadSHAWithEnv(testEnv(), dir)
	require.NoError(t, err)

	// Make the tree dirty
	writeTestFile(t, dir, "file.txt", "modified\n")
	writeTestFile(t, dir, "new.txt", "untracked\n")

	newSHA, err := BaselineUncommittedChangesWithEnv(testEnv(), dir)
	require.NoError(t, err)
	assert.NotEqual(t, originalSHA, newSHA, "should have created a new commit")
	assert.Len(t, newSHA, 40)

	// Verify the pre-session commit message
	cmd := NewGitCmdWithEnv(testEnv(), dir, "log", "-1", "--format=%s")
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "yoloai: pre-session state", strings.TrimSpace(string(out)))
}

func TestBaselineUncommittedChangesWithEnv_CleanTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "content\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	originalSHA, err := HeadSHAWithEnv(testEnv(), dir)
	require.NoError(t, err)

	newSHA, err := BaselineUncommittedChangesWithEnv(testEnv(), dir)
	require.NoError(t, err)
	assert.Equal(t, originalSHA, newSHA, "clean tree should not create a new commit")
}

// BaselineWithEnv tests

func TestBaselineWithEnv_CreatesRepoWithCommit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")

	sha, err := BaselineWithEnv(testEnv(), dir)
	require.NoError(t, err)

	// SHA should be a 40-character hex string
	assert.Len(t, sha, 40)
	assert.Regexp(t, `^[0-9a-f]{40}$`, sha)

	// Verify git log contains the baseline commit message
	cmd := NewGitCmdWithEnv(testEnv(), dir, "log", "--oneline")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "yoloai baseline")
}

func TestBaselineWithEnv_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	sha, err := BaselineWithEnv(testEnv(), dir)
	require.NoError(t, err)

	// Even with no files, --allow-empty should produce a valid commit
	assert.Len(t, sha, 40)
	assert.Regexp(t, `^[0-9a-f]{40}$`, sha)
}

func TestBaselineWithEnv_SetsUserConfig(t *testing.T) {
	dir := t.TempDir()

	_, err := BaselineWithEnv(testEnv(), dir)
	require.NoError(t, err)

	// Verify user.email was set to yoloai@localhost
	cmd := NewGitCmdWithEnv(testEnv(), dir, "config", "user.email")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "yoloai@localhost", strings.TrimSpace(string(output)))
}
