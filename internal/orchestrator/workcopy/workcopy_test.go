// ABOUTME: Materialize must produce the same work copy + baseline that create and
// ABOUTME: reset each open-coded before, across the mode × locality matrix, and
// ABOUTME: report why history was dropped rather than logging it.
package workcopy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
)

// fakeBackend is the minimal backend Materialize reads: only its filesystem
// locality, via LocalityOf. The embedded nil Backend panics on anything else,
// proving Materialize touches nothing more.
type fakeBackend struct {
	runtime.Backend
	locality runtime.FilesystemLocality
}

func (f fakeBackend) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Type:         "fake",
		Capabilities: runtime.BackendCaps{FilesystemLocality: f.locality},
	}
}

// hostSide is a host-filesystem backend (the work copy is host-readable).
func hostSide() fakeBackend {
	return fakeBackend{locality: runtime.LocalityHostSide}
}

func hostGit(t *testing.T) *git.Git { t.Helper(); return git.NewTestHostWithEnv(testutil.GitEnv()) }

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testutil.InitGitRepo(t, dir)
	testutil.WriteFile(t, dir, "app.js", "v1\n")
	testutil.WriteFile(t, dir, ".gitignore", ".env\n")
	testutil.WriteFile(t, dir, ".env", "SECRET")
	testutil.GitAdd(t, dir, "app.js")
	testutil.GitAdd(t, dir, ".gitignore")
	testutil.GitCommit(t, dir, "initial")
	return dir
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestMaterialize_CopyCleanRepo(t *testing.T) {
	src := repo(t)
	dst := filepath.Join(t.TempDir(), "work")

	sha, notice, err := Materialize(context.Background(), Spec{Src: src}, dst, WipeAndCopy, hostGit(t), hostSide())
	require.NoError(t, err)

	assert.Equal(t, testutil.RunGitOutput(t, src, "rev-parse", "HEAD"), sha, "clean repo: baseline is HEAD")
	assert.Equal(t, HistoryNotice{}, notice, "a plain repo drops no history")
	assert.True(t, exists(filepath.Join(dst, "app.js")))
	assert.False(t, exists(filepath.Join(dst, ".env")), ":copy honors .gitignore")
	info, err := os.Lstat(filepath.Join(dst, ".git"))
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "history preserved as a real repo")
}

func TestMaterialize_CopyDirtyRepo_CommitsSoAgentStartsClean(t *testing.T) {
	src := repo(t)
	head := testutil.RunGitOutput(t, src, "rev-parse", "HEAD")
	testutil.WriteFile(t, src, "app.js", "v1\nwork in progress\n")

	dst := filepath.Join(t.TempDir(), "work")
	sha, _, err := Materialize(context.Background(), Spec{Src: src}, dst, WipeAndCopy, hostGit(t), hostSide())
	require.NoError(t, err)

	assert.NotEqual(t, head, sha, "dirty state is committed, baseline moves past HEAD")
	assert.Empty(t, testutil.RunGitOutput(t, dst, "status", "--porcelain"), "agent starts clean")
}

func TestMaterialize_CopyAll_IncludesIgnored(t *testing.T) {
	src := repo(t)
	dst := filepath.Join(t.TempDir(), "work")

	_, _, err := Materialize(context.Background(), Spec{Src: src, IncludeIgnored: true}, dst, WipeAndCopy, hostGit(t), hostSide())
	require.NoError(t, err)
	assert.True(t, exists(filepath.Join(dst, ".env")), ":copy-all includes gitignored files")
}

// copy-strict is the user asking to strip history; it is silent (no notice).
func TestMaterialize_CopyStrict_FreshBaselineSilent(t *testing.T) {
	src := repo(t)
	dst := filepath.Join(t.TempDir(), "work")

	sha, notice, err := Materialize(context.Background(), Spec{Src: src, StripHistory: true}, dst, WipeAndCopy, hostGit(t), hostSide())
	require.NoError(t, err)

	assert.Len(t, sha, 40)
	assert.NotEqual(t, testutil.RunGitOutput(t, src, "rev-parse", "HEAD"), sha, "fresh baseline, not source HEAD")
	assert.Equal(t, HistoryNotice{}, notice, "copy-strict is a user choice, silent — no notice")
	assert.Equal(t, "yoloai baseline", testutil.RunGitOutput(t, dst, "log", "-1", "--format=%s"))
}

func TestMaterialize_Worktree_SeversLinkAndReportsIt(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	wt := filepath.Join(root, "wt")
	require.NoError(t, os.MkdirAll(main, 0o750))
	testutil.InitGitRepo(t, main)
	testutil.WriteFile(t, main, "app.js", "v1\n")
	testutil.GitAdd(t, main, ".")
	testutil.GitCommit(t, main, "initial")
	testutil.RunGit(t, main, "worktree", "add", "-q", wt, "-b", "feature")
	mainHead := testutil.RunGitOutput(t, main, "rev-parse", "HEAD")

	dst := filepath.Join(t.TempDir(), "work")
	sha, notice, err := Materialize(context.Background(), Spec{Src: wt}, dst, WipeAndCopy, hostGit(t), hostSide())
	require.NoError(t, err)

	assert.True(t, notice.SourceIsGitLink, "a worktree source is reported as a gitlink")
	assert.NotEqual(t, mainHead, sha, "fresh baseline, not the user's HEAD read through the link")
	info, err := os.Lstat(filepath.Join(dst, ".git"))
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "the copy owns its repo, not a pointer at the user's (DF116)")
}

func TestMaterialize_NonRepo_FreshBaseline(t *testing.T) {
	src := t.TempDir()
	testutil.WriteFile(t, src, "app.js", "v1\n")
	dst := filepath.Join(t.TempDir(), "work")

	sha, notice, err := Materialize(context.Background(), Spec{Src: src}, dst, WipeAndCopy, hostGit(t), hostSide())
	require.NoError(t, err)
	assert.Len(t, sha, 40)
	assert.Equal(t, HistoryNotice{}, notice)
	assert.True(t, exists(filepath.Join(dst, "app.js")))
}

// SandboxSide defers baselining into the VM: an empty SHA, but the copy is still
// staged and the notice still computed.
func TestMaterialize_SandboxSide_DefersBaseline(t *testing.T) {
	src := repo(t)
	dst := filepath.Join(t.TempDir(), "work")
	backend := fakeBackend{locality: runtime.LocalitySandboxSide}

	sha, _, err := Materialize(context.Background(), Spec{Src: src}, dst, WipeAndCopy, hostGit(t), backend)
	require.NoError(t, err)
	assert.Empty(t, sha, "SandboxSide baseline is deferred to the VM")
	assert.True(t, exists(filepath.Join(dst, "app.js")), "but the copy is staged on the host")
}

// WipeAndCopy over a stale destination (the reset --restart case) leaves exactly
// the source's files — the agent's leftovers are gone.
func TestMaterialize_RebuildsStaleDestination(t *testing.T) {
	src := repo(t)
	dst := filepath.Join(t.TempDir(), "work")
	require.NoError(t, os.MkdirAll(dst, 0o750))
	testutil.WriteFile(t, dst, "agent-leftover.txt", "stale")

	_, _, err := Materialize(context.Background(), Spec{Src: src}, dst, WipeAndCopy, hostGit(t), hostSide())
	require.NoError(t, err)
	assert.False(t, exists(filepath.Join(dst, "agent-leftover.txt")), "wipe-and-copy clears the stale dst")
	assert.True(t, exists(filepath.Join(dst, "app.js")))
}

// InPlaceAndPrune overwrites a live-mounted dst without replacing the directory:
// the inode survives (a bind-mount resolves to it), the agent's leftovers are
// pruned, and a gitignored secret is not re-imported (DF117).
func TestMaterialize_InPlaceAndPrune_OverwritesWithoutReplacing(t *testing.T) {
	src := repo(t)
	testutil.WriteFile(t, src, "upstream.txt", "from host\n")
	testutil.GitAdd(t, src, ".")
	testutil.GitCommit(t, src, "upstream")

	// dst as a live work copy: has the agent's leftover and a previously-leaked secret.
	dst := filepath.Join(t.TempDir(), "work")
	require.NoError(t, os.MkdirAll(dst, 0o750))
	testutil.WriteFile(t, dst, "agent-leftover.txt", "stale")
	testutil.WriteFile(t, dst, ".env", "LEAKED")
	before, err := os.Stat(dst)
	require.NoError(t, err)

	_, _, err = Materialize(context.Background(), Spec{Src: src}, dst, InPlaceAndPrune, hostGit(t), hostSide())
	require.NoError(t, err)

	after, err := os.Stat(dst)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after), "the dst inode must survive: a bind-mount resolves to it")
	assert.False(t, exists(filepath.Join(dst, "agent-leftover.txt")), "the agent's leftover is pruned")
	assert.False(t, exists(filepath.Join(dst, ".env")), "a leaked secret is pruned, not re-imported (DF117)")
	assert.True(t, exists(filepath.Join(dst, "upstream.txt")), "upstream arrives")
}

// .git is replaced as a unit, not merged: a work copy whose repo is unrelated to
// the source's adopts the source's history cleanly, with no dangling ref (DF118).
func TestMaterialize_InPlaceAndPrune_ReplacesGitCleanly(t *testing.T) {
	src := repo(t)
	dst := filepath.Join(t.TempDir(), "work")
	require.NoError(t, os.MkdirAll(dst, 0o750))
	testutil.InitGitRepo(t, dst)
	testutil.WriteFile(t, dst, "old.txt", "unrelated")
	testutil.GitAdd(t, dst, ".")
	testutil.GitCommit(t, dst, "unrelated baseline")

	sha, _, err := Materialize(context.Background(), Spec{Src: src}, dst, InPlaceAndPrune, hostGit(t), hostSide())
	require.NoError(t, err)

	assert.Equal(t, testutil.RunGitOutput(t, src, "rev-parse", "HEAD"), sha, "adopts the source's history")
	assert.Equal(t, "initial", testutil.RunGitOutput(t, dst, "log", "-1", "--format=%s"),
		"repo is readable — a merged .git would say 'bad object HEAD'")
}
