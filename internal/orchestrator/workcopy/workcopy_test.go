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

// fakeBackend is the minimal backend Materialize reads: only its capabilities,
// via GitRunsInConfinement and LocalityOf. The embedded nil Backend panics on
// anything else, proving Materialize touches nothing more.
type fakeBackend struct {
	runtime.Backend
	locality    runtime.FilesystemLocality
	confinedGit bool
}

func (f fakeBackend) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Type: "fake",
		Capabilities: runtime.BackendCaps{
			FilesystemLocality:   f.locality,
			GitExecInConfinement: f.confinedGit,
		},
	}
}

// hostSide is a real backend's shape: host-readable work copy, confined git.
func hostSide() fakeBackend {
	return fakeBackend{locality: runtime.LocalityHostSide, confinedGit: true}
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

	sha, notice, err := Materialize(context.Background(), Spec{Src: src}, dst, hostGit(t), hostSide())
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
	sha, _, err := Materialize(context.Background(), Spec{Src: src}, dst, hostGit(t), hostSide())
	require.NoError(t, err)

	assert.NotEqual(t, head, sha, "dirty state is committed, baseline moves past HEAD")
	assert.Empty(t, testutil.RunGitOutput(t, dst, "status", "--porcelain"), "agent starts clean")
}

func TestMaterialize_CopyAll_IncludesIgnored(t *testing.T) {
	src := repo(t)
	dst := filepath.Join(t.TempDir(), "work")

	_, _, err := Materialize(context.Background(), Spec{Src: src, IncludeIgnored: true}, dst, hostGit(t), hostSide())
	require.NoError(t, err)
	assert.True(t, exists(filepath.Join(dst, ".env")), ":copy-all includes gitignored files")
}

// copy-strict is the user asking to strip history; it is silent — the downgrade
// notice is only for a backend that forces the strip.
func TestMaterialize_CopyStrict_FreshBaselineNoDowngradeNotice(t *testing.T) {
	src := repo(t)
	dst := filepath.Join(t.TempDir(), "work")

	sha, notice, err := Materialize(context.Background(), Spec{Src: src, StripHistory: true}, dst, hostGit(t), hostSide())
	require.NoError(t, err)

	assert.Len(t, sha, 40)
	assert.NotEqual(t, testutil.RunGitOutput(t, src, "rev-parse", "HEAD"), sha, "fresh baseline, not source HEAD")
	assert.False(t, notice.HistoryDowngraded, "user-requested strip is not a downgrade")
	assert.Equal(t, "yoloai baseline", testutil.RunGitOutput(t, dst, "log", "-1", "--format=%s"))
}

// An unconfined backend forces the strip and must say so — the path no real
// backend takes today (DF119), so only a fake reaches it.
func TestMaterialize_UnconfinedBackend_DowngradesWithNotice(t *testing.T) {
	src := repo(t)
	dst := filepath.Join(t.TempDir(), "work")
	backend := fakeBackend{locality: runtime.LocalityHostSide, confinedGit: false}

	sha, notice, err := Materialize(context.Background(), Spec{Src: src}, dst, hostGit(t), backend)
	require.NoError(t, err)

	assert.True(t, notice.HistoryDowngraded, "an unconfined backend reports the forced strip")
	assert.Len(t, sha, 40)
	// History not preserved: the copy has a fresh baseline repo of its own, not
	// the source's history — the "initial" commit is gone, only the baseline remains.
	assert.Equal(t, "yoloai baseline", testutil.RunGitOutput(t, dst, "log", "-1", "--format=%s"))
	assert.Equal(t, "1", testutil.RunGitOutput(t, dst, "rev-list", "--count", "HEAD"), "one commit: the baseline")
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
	sha, notice, err := Materialize(context.Background(), Spec{Src: wt}, dst, hostGit(t), hostSide())
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

	sha, notice, err := Materialize(context.Background(), Spec{Src: src}, dst, hostGit(t), hostSide())
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
	backend := fakeBackend{locality: runtime.LocalitySandboxSide, confinedGit: true}

	sha, _, err := Materialize(context.Background(), Spec{Src: src}, dst, hostGit(t), backend)
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

	_, _, err := Materialize(context.Background(), Spec{Src: src}, dst, hostGit(t), hostSide())
	require.NoError(t, err)
	assert.False(t, exists(filepath.Join(dst, "agent-leftover.txt")), "wipe-and-copy clears the stale dst")
	assert.True(t, exists(filepath.Join(dst, "app.js")))
}
