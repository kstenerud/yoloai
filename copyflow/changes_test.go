// ABOUTME: Tests for DetectChanges and HasUnappliedWorkVia git-status helpers.
package copyflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstenerud/yoloai/internal/git"
)

// DetectChanges tests

func TestDetectChanges_NoWorkDir(t *testing.T) {
	assert.Equal(t, "-", DetectChanges(context.Background(), git.NewHostWithEnv(testEnv()), "/nonexistent/path"))
}

func TestDetectChanges_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "-", DetectChanges(context.Background(), git.NewHostWithEnv(testEnv()), dir))
}

func TestDetectChanges_CleanRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	assert.Equal(t, "no", DetectChanges(context.Background(), git.NewHostWithEnv(testEnv()), dir))
}

func TestDetectChanges_DirtyRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "file.txt", "modified")
	assert.Equal(t, "yes", DetectChanges(context.Background(), git.NewHostWithEnv(testEnv()), dir))
}

func TestDetectChanges_UntrackedFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "new.txt", "untracked")
	assert.Equal(t, "yes", DetectChanges(context.Background(), git.NewHostWithEnv(testEnv()), dir))
}

// HasUnappliedWorkVia tests. A host-scoped runner runs git directly on the host
// repo (behaviorally identical to a sandbox runner over a non-GitExecer backend),
// so these exercise the real git logic on host repos; the WorkUnknown fail-safe
// path (a GitExecer backend reporting ErrNotRunning) is covered in the lifecycle
// package where a runtime mock exists.

func TestHasUnappliedWorkVia_NoWorkDir(t *testing.T) {
	assert.Equal(t, WorkClean, HasUnappliedWorkVia(context.Background(), git.NewHostWithEnv(testEnv()), "/nonexistent/path", "abc123"))
}

func TestHasUnappliedWorkVia_CleanAtBaseline(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := gitRevParse(t, dir)
	assert.Equal(t, WorkClean, HasUnappliedWorkVia(context.Background(), git.NewHostWithEnv(testEnv()), dir, sha))
}

func TestHasUnappliedWorkVia_DirtyWorkingTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := gitRevParse(t, dir)
	writeTestFile(t, dir, "file.txt", "modified")
	assert.Equal(t, WorkDirty, HasUnappliedWorkVia(context.Background(), git.NewHostWithEnv(testEnv()), dir, sha))
}

func TestHasUnappliedWorkVia_CommitsBeyondBaseline(t *testing.T) {
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

	assert.Equal(t, WorkDirty, HasUnappliedWorkVia(context.Background(), git.NewHostWithEnv(testEnv()), dir, baselineSHA))
}

func TestHasUnappliedWorkVia_EmptyBaseline(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	// Empty baseline — can't check commits, only dirty tree
	assert.Equal(t, WorkClean, HasUnappliedWorkVia(context.Background(), git.NewHostWithEnv(testEnv()), dir, ""))
}
