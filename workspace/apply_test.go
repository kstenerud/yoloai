package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- formatApplyError ---

func TestFormatApplyError_PatchFailed(t *testing.T) {
	gitErr := fmt.Errorf("error: patch failed: handler.go:42\nerror: handler.go: patch does not apply: exit status 1")
	err := formatApplyError(gitErr, "/tmp/project")
	assert.Contains(t, err.Error(), "handler.go")
	assert.Contains(t, err.Error(), "42")
	assert.Contains(t, err.Error(), "conflict")
}

func TestFormatApplyError_Unknown(t *testing.T) {
	gitErr := fmt.Errorf("some unusual error: exit status 1")
	err := formatApplyError(gitErr, "/tmp/project")
	assert.Contains(t, err.Error(), "git apply failed")
	assert.Contains(t, err.Error(), "/tmp/project")
}

func TestFormatApplyError_DoesNotExist(t *testing.T) {
	gitErr := fmt.Errorf("error: foo.txt: does not exist in working directory: exit status 1")
	err := formatApplyError(gitErr, "/tmp/project")
	assert.Contains(t, err.Error(), "no longer exists")
	assert.Contains(t, err.Error(), "foo.txt")
}

func TestFormatApplyError_AlreadyExists(t *testing.T) {
	gitErr := fmt.Errorf("error: bar.txt: already exists in working directory: exit status 1")
	err := formatApplyError(gitErr, "/tmp/project")
	assert.Contains(t, err.Error(), "already exists")
	assert.Contains(t, err.Error(), "bar.txt")
}

// --- IsGitRepo ---

func TestIsGitRepo_True(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	assert.True(t, IsGitRepo(dir))
}

func TestIsGitRepo_False(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, IsGitRepo(dir))
}

// --- ContiguousPrefixEnd ---

func TestContiguousPrefixEnd_AllApplied(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "first"},
		{SHA: "bbb", Subject: "second"},
		{SHA: "ccc", Subject: "third"},
	}
	applied := map[string]bool{"aaa": true, "bbb": true, "ccc": true}
	assert.Equal(t, 2, ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_PrefixOnly(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "first"},
		{SHA: "bbb", Subject: "second"},
		{SHA: "ccc", Subject: "third"},
	}
	applied := map[string]bool{"aaa": true, "bbb": true}
	assert.Equal(t, 1, ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_FirstNotApplied(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "first"},
		{SHA: "bbb", Subject: "second"},
	}
	applied := map[string]bool{"bbb": true}
	assert.Equal(t, -1, ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_NoneApplied(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "first"},
		{SHA: "bbb", Subject: "second"},
	}
	applied := map[string]bool{}
	assert.Equal(t, -1, ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_EmptyCommits(t *testing.T) {
	applied := map[string]bool{"aaa": true}
	assert.Equal(t, -1, ContiguousPrefixEnd(nil, applied))
}

func TestContiguousPrefixEnd_SingleApplied(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "only"},
	}
	applied := map[string]bool{"aaa": true}
	assert.Equal(t, 0, ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_GapInMiddle(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "first"},
		{SHA: "bbb", Subject: "second"},
		{SHA: "ccc", Subject: "third"},
	}
	// First applied, second not, third applied - breaks at second
	applied := map[string]bool{"aaa": true, "ccc": true}
	assert.Equal(t, 0, ContiguousPrefixEnd(commits, applied))
}

// --- formatAMError ---

func TestFormatAMError_ContainsGuidance(t *testing.T) {
	output := []byte("Applying: fix bug\nerror: patch failed")
	err := formatAMError(output, "/tmp/target")
	msg := err.Error()
	assert.Contains(t, msg, "cd /tmp/target")
	assert.Contains(t, msg, "--continue")
	assert.Contains(t, msg, "--skip")
	assert.Contains(t, msg, "--abort")
}

func TestFormatAMError_IncludesOutput(t *testing.T) {
	output := []byte("Applying: my commit\nConflict in file.txt")
	err := formatAMError(output, "/tmp/target")
	msg := err.Error()
	assert.Contains(t, msg, "Applying: my commit")
	assert.Contains(t, msg, "Conflict in file.txt")
}

func TestFormatAMError_EmptyOutput(t *testing.T) {
	err := formatAMError([]byte(""), "/tmp/target")
	msg := err.Error()
	assert.Contains(t, msg, "git am failed in /tmp/target")
	assert.Contains(t, msg, "--continue")
}

// --- helper: generatePatch creates a git diff patch by modifying a file ---

func generatePatch(t *testing.T, dir, filename, oldContent, newContent string) []byte {
	t.Helper()
	writeTestFile(t, dir, filename, oldContent)
	gitAdd(t, dir, filename)
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, filename, newContent)
	gitAdd(t, dir, filename)

	cmd := exec.Command("git", "-C", dir, "diff", "--cached") //nolint:gosec // G204: test helper with known command
	out, err := cmd.Output()
	require.NoError(t, err, "git diff --cached failed")
	require.NotEmpty(t, out, "patch should not be empty")

	// Reset the staged change so the repo is back at old content
	runGit(t, dir, "reset", "HEAD", "--", filename)
	runGit(t, dir, "checkout", "--", filename)

	return out
}

// --- CheckPatch ---

func TestCheckPatch_CleanApply(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")

	err := CheckPatch(patch, dir, true)
	assert.NoError(t, err)
}

func TestCheckPatch_Conflict(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")

	// Change the file to something different so the patch conflicts
	writeTestFile(t, dir, "file.txt", "completely different content\n")
	gitAdd(t, dir, "file.txt")
	gitCommit(t, dir, "diverge")

	err := CheckPatch(patch, dir, true)
	assert.Error(t, err)
}

func TestCheckPatch_NonGitDir(t *testing.T) {
	// Create a git repo to generate the patch
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	patch := generatePatch(t, repoDir, "file.txt", "old content\n", "new content\n")

	// Create a non-git target directory with the expected old content
	targetDir := t.TempDir()
	writeTestFile(t, targetDir, "file.txt", "old content\n")

	err := CheckPatch(patch, targetDir, false)
	assert.NoError(t, err)
}

// --- ApplyPatch ---

func TestApplyPatch_ApplyInGitRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")

	err := ApplyPatch(patch, dir, true)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "file.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "new content\n", string(content))
}

func TestApplyPatch_NonGitDir(t *testing.T) {
	// Create a git repo to generate the patch
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	patch := generatePatch(t, repoDir, "file.txt", "old content\n", "new content\n")

	// Create a non-git target directory with the expected old content
	targetDir := t.TempDir()
	writeTestFile(t, targetDir, "file.txt", "old content\n")

	err := ApplyPatch(patch, targetDir, false)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(targetDir, "file.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "new content\n", string(content))
}

func TestApplyPatch_ConflictReturnsError(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")

	// Change the file so the patch conflicts
	writeTestFile(t, dir, "file.txt", "completely different content\n")
	gitAdd(t, dir, "file.txt")
	gitCommit(t, dir, "diverge")

	err := ApplyPatch(patch, dir, true)
	assert.Error(t, err)
}

// --- ApplyFormatPatch ---

func TestApplyFormatPatch_EmptyFilesList(t *testing.T) {
	err := ApplyFormatPatch("/nonexistent", nil, "/nonexistent")
	assert.NoError(t, err)
}

// --- withTempGitDir ---

func TestWithTempGitDir_CallsFn(t *testing.T) {
	var calledWith string
	err := withTempGitDir(func(tmpDir string) error {
		calledWith = tmpDir
		// The temp dir should be a valid git repo
		assert.True(t, IsGitRepo(tmpDir))
		return nil
	})
	require.NoError(t, err)
	assert.NotEmpty(t, calledWith)
}

func TestWithTempGitDir_PropagatesError(t *testing.T) {
	sentinel := fmt.Errorf("test sentinel error")
	err := withTempGitDir(func(tmpDir string) error {
		return sentinel
	})
	assert.ErrorIs(t, err, sentinel)
}

func TestWithTempGitDir_CleansUp(t *testing.T) {
	var capturedDir string
	err := withTempGitDir(func(tmpDir string) error {
		capturedDir = tmpDir
		// Verify the dir exists while inside the callback
		_, statErr := os.Stat(tmpDir)
		require.NoError(t, statErr)
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, capturedDir)

	// After return, the temp dir should no longer exist
	_, err = os.Stat(capturedDir)
	assert.True(t, os.IsNotExist(err), "temp dir should be cleaned up after withTempGitDir returns")
}

// --- runGitApply ---

func TestRunGitApply_ValidPatch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")

	err := runGitApply(dir, patch)
	assert.NoError(t, err)

	// Verify the file was changed
	content, err := os.ReadFile(filepath.Join(dir, "file.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "new content\n", string(content))
}

func TestRunGitApply_InvalidPatch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "dummy.txt", "dummy\n")
	gitAdd(t, dir, "dummy.txt")
	gitCommit(t, dir, "initial")

	err := runGitApply(dir, []byte("this is not a valid patch"))
	assert.Error(t, err)
}
