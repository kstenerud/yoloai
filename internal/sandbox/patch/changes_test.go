// ABOUTME: Tests for DetectChanges and HasUnappliedWork git-status helpers.
package patch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// DetectChanges tests

func TestDetectChanges_NoWorkDir(t *testing.T) {
	assert.Equal(t, "-", DetectChanges("/nonexistent/path"))
}

func TestDetectChanges_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "-", DetectChanges(dir))
}

func TestDetectChanges_CleanRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	assert.Equal(t, "no", DetectChanges(dir))
}

func TestDetectChanges_DirtyRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "file.txt", "modified")
	assert.Equal(t, "yes", DetectChanges(dir))
}

func TestDetectChanges_UntrackedFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "new.txt", "untracked")
	assert.Equal(t, "yes", DetectChanges(dir))
}

// HasUnappliedWork tests

func TestHasUnappliedWork_NoWorkDir(t *testing.T) {
	assert.False(t, HasUnappliedWork("/nonexistent/path", "abc123"))
}

func TestHasUnappliedWork_CleanAtBaseline(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := gitRevParse(t, dir)
	assert.False(t, HasUnappliedWork(dir, sha))
}

func TestHasUnappliedWork_DirtyWorkingTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := gitRevParse(t, dir)
	writeTestFile(t, dir, "file.txt", "modified")
	assert.True(t, HasUnappliedWork(dir, sha))
}

func TestHasUnappliedWork_CommitsBeyondBaseline(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	baselineSHA := gitRevParse(t, dir)

	// Make a new commit beyond baseline
	writeTestFile(t, dir, "file.txt", "modified")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "agent work")

	assert.True(t, HasUnappliedWork(dir, baselineSHA))
}

func TestHasUnappliedWork_EmptyBaseline(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	// Empty baseline — can't check commits, only dirty tree
	assert.False(t, HasUnappliedWork(dir, ""))
}
