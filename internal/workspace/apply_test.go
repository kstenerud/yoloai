// ABOUTME: Tests for workspace apply free-function wrappers and re-exported types.
// ABOUTME: Detailed git-apply logic tests live in internal/git/ops_test.go.
package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestContiguousPrefixEnd_NoneApplied(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "first"},
		{SHA: "bbb", Subject: "second"},
	}
	applied := map[string]bool{}
	assert.Equal(t, -1, ContiguousPrefixEnd(commits, applied))
}

// --- CheckPatch wrapper ---

func TestCheckPatch_Wrapper_CleanApply(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")

	err := CheckPatch(testEnv(), patch, dir, true)
	assert.NoError(t, err)
}

func TestCheckPatch_Wrapper_Conflict(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")

	writeTestFile(t, dir, "file.txt", "completely different content\n")
	gitAdd(t, dir, "file.txt")
	gitCommit(t, dir, "diverge")

	err := CheckPatch(testEnv(), patch, dir, true)
	assert.Error(t, err)
}

// --- ApplyPatch wrapper ---

func TestApplyPatch_Wrapper(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")
	err := ApplyPatch(testEnv(), patch, dir, true)
	require.NoError(t, err)
}

// --- ApplyFormatPatch wrapper ---

func TestApplyFormatPatch_Wrapper_EmptyFiles(t *testing.T) {
	_, err := ApplyFormatPatch(testEnv(), "/nonexistent", nil, "/nonexistent")
	assert.NoError(t, err)
}

// --- helper ---

func generatePatch(t *testing.T, dir, filename, oldContent, newContent string) []byte {
	t.Helper()
	writeTestFile(t, dir, filename, oldContent)
	gitAdd(t, dir, filename)
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, filename, newContent)
	gitAdd(t, dir, filename)

	cmd := NewGitCmdWithEnv(testEnv(), dir, "diff", "--cached")
	out, err := cmd.Output()
	require.NoError(t, err, "git diff --cached failed")
	require.NotEmpty(t, out)

	runGit(t, dir, "reset", "HEAD", "--", filename)
	runGit(t, dir, "checkout", "--", filename)

	return out
}
