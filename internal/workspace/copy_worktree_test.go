// ABOUTME: A work copy must never keep a .git link. Covers IsGitLink/RemoveGitLink
// ABOUTME: and CopyProjectDir against real linked worktrees and submodules, whose
// ABOUTME: git dir lives outside the copied tree and resolves back to the source
// ABOUTME: repo on the host (DF116).
package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeWorktree builds a real repo in main and adds a linked worktree beside it,
// returning both paths. The worktree's .git is a file, not a directory — the
// whole point, and the reason a synthetic `gitdir:` string is not good enough
// here: only real git decides what it writes into it.
func makeWorktree(t *testing.T, root string) (mainRepo, worktree string) {
	t.Helper()
	mainRepo = filepath.Join(root, "main")
	worktree = filepath.Join(root, "wt")
	require.NoError(t, os.MkdirAll(mainRepo, 0o750))
	makeRepo(t, mainRepo)
	runGit(t, mainRepo, "worktree", "add", "-q", worktree, "-b", "feature")
	return mainRepo, worktree
}

func TestMakeWorktree_GitIsAFileNotADir(t *testing.T) {
	_, wt := makeWorktree(t, t.TempDir())

	info, err := os.Lstat(filepath.Join(wt, ".git"))
	require.NoError(t, err)
	assert.False(t, info.IsDir(), "a linked worktree's .git must be a file for these tests to mean anything")
}

func TestIsGitLink(t *testing.T) {
	_, wt := makeWorktree(t, t.TempDir())
	assert.True(t, IsGitLink(wt), "linked worktree keeps its git dir elsewhere")

	realRepo := t.TempDir()
	makeRepo(t, realRepo)
	assert.False(t, IsGitLink(realRepo), "a real .git directory is not a link")

	assert.False(t, IsGitLink(t.TempDir()), "no .git at all is not a link")
}

func TestRemoveGitLink_RemovesWorktreeLink(t *testing.T) {
	_, wt := makeWorktree(t, t.TempDir())

	removed, err := RemoveGitLink(wt)
	require.NoError(t, err)
	assert.True(t, removed)
	assert.False(t, exists(filepath.Join(wt, ".git")))
}

func TestRemoveGitLink_KeepsRealGitDir(t *testing.T) {
	dir := t.TempDir()
	makeRepo(t, dir)

	removed, err := RemoveGitLink(dir)
	require.NoError(t, err)
	assert.False(t, removed, "a real .git directory is history worth keeping, not a link")
	assert.True(t, exists(filepath.Join(dir, ".git")))
}

func TestRemoveGitLink_NoGitIsNoOp(t *testing.T) {
	removed, err := RemoveGitLink(t.TempDir())
	require.NoError(t, err)
	assert.False(t, removed)
}

// A .git symlink points outside the copy just as a gitlink file does, and
// os.Stat-based checks follow it — so Lstat semantics are load-bearing.
func TestRemoveGitLink_RemovesSymlink(t *testing.T) {
	realRepo := t.TempDir()
	makeRepo(t, realRepo)

	dir := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(realRepo, ".git"), filepath.Join(dir, ".git")))

	removed, err := RemoveGitLink(dir)
	require.NoError(t, err)
	assert.True(t, removed)
	assert.False(t, exists(filepath.Join(dir, ".git")))
	assert.True(t, exists(filepath.Join(realRepo, ".git")), "removing the link must not touch its target")
}

// :copy-all copies the source wholesale, so it is the branch that would carry a
// worktree's gitlink into the work copy — the DF116 path.
func TestCopyProjectDir_CopyAllFromWorktree_SeversGitLink(t *testing.T) {
	_, wt := makeWorktree(t, t.TempDir())
	write(t, filepath.Join(wt, "ignored.log"), "SECRET")

	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(wt, dst, true, true, listFn()))

	assert.False(t, exists(filepath.Join(dst, ".git")), "work copy must not point back at the source repo")
	assert.True(t, exists(filepath.Join(dst, "a.txt")), ":copy-all still copies the files")
	assert.True(t, exists(filepath.Join(dst, "ignored.log")), ":copy-all still includes ignored files")
}

func TestCopyProjectDir_CopyFromWorktree_HasNoGitLink(t *testing.T) {
	_, wt := makeWorktree(t, t.TempDir())

	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(wt, dst, false, true, listFn("a.txt")))

	assert.False(t, exists(filepath.Join(dst, ".git")))
	assert.True(t, exists(filepath.Join(dst, "a.txt")))
}

// The sever must not cost a normal repo its history: that is what :copy is for.
func TestCopyProjectDir_NormalRepoKeepsGitDir(t *testing.T) {
	src := t.TempDir()
	makeRepo(t, src)

	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(src, dst, false, true, listFn("a.txt", ".gitignore")))

	info, err := os.Lstat(filepath.Join(dst, ".git"))
	require.NoError(t, err, "a real repo's history must survive the copy")
	assert.True(t, info.IsDir())
	assert.Equal(t, "initial commit", runGit(t, dst, "log", "-1", "--format=%s"))
}

func TestCopyProjectDir_CopyAllFromNormalRepo_KeepsGitDir(t *testing.T) {
	src := t.TempDir()
	makeRepo(t, src)

	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(src, dst, true, true, listFn()))

	info, err := os.Lstat(filepath.Join(dst, ".git"))
	require.NoError(t, err)
	assert.True(t, info.IsDir(), ":copy-all's whole point is copying everything, .git included")
}

// A submodule's gitlink is relative (`gitdir: ../.git/modules/x`) where a
// worktree's is absolute, so the sever must key off the .git entry's kind rather
// than anything in the link text.
func TestCopyProjectDir_CopyAllFromSubmodule_SeversGitLink(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "lib")
	require.NoError(t, os.MkdirAll(lib, 0o750))
	makeRepo(t, lib)

	super := filepath.Join(root, "super")
	require.NoError(t, os.MkdirAll(super, 0o750))
	makeRepo(t, super)
	runGit(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "libdir")

	subdir := filepath.Join(super, "libdir")
	require.True(t, IsGitLink(subdir), "a submodule keeps its git dir in the superproject")

	dst := filepath.Join(t.TempDir(), "out")
	require.NoError(t, CopyProjectDir(subdir, dst, true, true, listFn()))

	assert.False(t, exists(filepath.Join(dst, ".git")))
	assert.True(t, exists(filepath.Join(dst, "a.txt")))
}
