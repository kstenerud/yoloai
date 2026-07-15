// ABOUTME: ListProjectFiles walks a git worktree honoring .gitignore (including
// ABOUTME: nested ignores) and errors when the directory isn't a git repo.

package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkfile writes content to dir/rel, creating intermediate directories (rel may be
// a nested path like "sub/a.txt").
func mkfile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

func TestListProjectFiles_HonorsGitignore(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	mkfile(t, dir, ".gitignore", "*.secret\n.env\nignored/\n*.log\n!keep.log\n")
	mkfile(t, dir, "tracked.txt", "t")
	mkfile(t, dir, "untracked.txt", "u")
	mkfile(t, dir, "api.secret", "SECRET")
	mkfile(t, dir, ".env", "TOKEN=xyz")
	mkfile(t, dir, "ignored/x.bin", "junk")
	mkfile(t, dir, "debug.log", "noise")
	mkfile(t, dir, "keep.log", "negated back in")
	mkfile(t, dir, "sub/kept.txt", "nested")
	runGit(t, dir, "add", "tracked.txt", ".gitignore")

	files, isRepo, err := NewTestHostWithEnv(testEnv()).ListProjectFiles(context.Background(), dir)
	require.NoError(t, err)
	require.True(t, isRepo)

	assert.Contains(t, files, "tracked.txt")
	assert.Contains(t, files, "untracked.txt")
	assert.Contains(t, files, "sub/kept.txt")
	assert.Contains(t, files, "keep.log", "negated (!keep.log) re-included")
	assert.NotContains(t, files, "api.secret")
	assert.NotContains(t, files, ".env")
	assert.NotContains(t, files, "debug.log")
	for _, f := range files {
		assert.False(t, strings.HasPrefix(f, "ignored/"), "ignored dir excluded, got %q", f)
	}
}

func TestListProjectFiles_NestedGitignore(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	mkfile(t, dir, "a.txt", "a")
	mkfile(t, dir, "sub/.gitignore", "local.*\n")
	mkfile(t, dir, "sub/keep.txt", "keep")
	mkfile(t, dir, "sub/local.secret", "SECRET")

	files, _, err := NewTestHostWithEnv(testEnv()).ListProjectFiles(context.Background(), dir)
	require.NoError(t, err)
	assert.Contains(t, files, "sub/keep.txt")
	assert.NotContains(t, files, "sub/local.secret", "nested .gitignore honored")
}

func TestListProjectFiles_SubdirRelativeToQueriedDir(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	mkfile(t, repo, ".gitignore", "*.secret\n")
	mkfile(t, repo, "sub/a.txt", "a")
	mkfile(t, repo, "sub/b.secret", "SECRET")

	files, isRepo, err := NewTestHostWithEnv(testEnv()).ListProjectFiles(context.Background(), filepath.Join(repo, "sub"))
	require.NoError(t, err)
	require.True(t, isRepo)
	assert.Contains(t, files, "a.txt", "paths relative to the queried subdir")
	assert.NotContains(t, files, "b.secret", "repo-root .gitignore honored in subdir")
}

func TestListProjectFiles_NotARepo(t *testing.T) {
	files, isRepo, err := NewTestHostWithEnv(testEnv()).ListProjectFiles(context.Background(), t.TempDir())
	require.NoError(t, err)
	assert.False(t, isRepo, "non-repo dir reported as not a work tree")
	assert.Nil(t, files)
}
