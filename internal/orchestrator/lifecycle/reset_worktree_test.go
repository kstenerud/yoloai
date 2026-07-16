// ABOUTME: In-place reset rsyncs the host tree verbatim, so a linked-worktree
// ABOUTME: workdir would land its .git pointer in the work copy — over the
// ABOUTME: baseline repo, and resolving back to the user's real repo from the
// ABOUTME: host, which is where the new baseline would then be read from (DF116).
package lifecycle

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/testutil"
)

func requireRsync(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Fatalf("rsync is required for in-place reset tests and is not on PATH "+
			"(D112 — required tooling, no carve-out; install rsync): %v", err)
	}
}

// makeWorktreeRepo builds a real repo with a linked worktree checked out on
// branch `feature`. Real git, not a hand-written `gitdir:` string: the point is
// what git itself puts in that file.
func makeWorktreeRepo(t *testing.T, root string) (mainRepo, worktree string) {
	t.Helper()
	mainRepo = filepath.Join(root, "main")
	worktree = filepath.Join(root, "wt")
	require.NoError(t, os.MkdirAll(mainRepo, 0o750))
	testutil.InitGitRepo(t, mainRepo)
	testutil.WriteFile(t, mainRepo, "file.txt", "v1\n")
	testutil.GitAdd(t, mainRepo, ".")
	testutil.GitCommit(t, mainRepo, "initial")
	testutil.RunGit(t, mainRepo, "worktree", "add", "-q", worktree, "-b", "feature")
	return mainRepo, worktree
}

// newWorkCopy stands in for what create leaves behind: the source's files and a
// baseline repo of the work copy's own.
func newWorkCopy(t *testing.T, content string) string {
	t.Helper()
	workDir := filepath.Join(t.TempDir(), "work")
	require.NoError(t, os.MkdirAll(workDir, 0o750))
	testutil.WriteFile(t, workDir, "file.txt", content)
	testutil.InitGitRepo(t, workDir)
	testutil.GitAdd(t, workDir, ".")
	testutil.GitCommit(t, workDir, "yoloai baseline")
	return workDir
}

func TestResyncWorkCopy_FromWorktree_KeepsBaselineLocal(t *testing.T) {
	requireRsync(t)
	_, wt := makeWorktreeRepo(t, t.TempDir())
	workDir := newWorkCopy(t, "v1\n")

	testutil.WriteFile(t, wt, "file.txt", "v2 upstream\n")
	hostHead := gitHEAD(t, wt)
	hostStatus := testutil.RunGitOutput(t, wt, "status", "--porcelain")
	require.NotEmpty(t, hostStatus, "fixture must be dirty or the read-through would have nothing to prove")

	sha, err := resyncWorkCopy(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), testutil.GitEnv(), wt, workDir)
	require.NoError(t, err)

	// The work copy must own its baseline rather than borrow the user's.
	assert.Len(t, sha, 40)
	assert.NotEqual(t, hostHead, sha, "baseline must not be the user's HEAD, read through a gitlink")
	info, err := os.Lstat(filepath.Join(workDir, ".git"))
	require.NoError(t, err, "the work copy still needs a repo after the sever")
	assert.True(t, info.IsDir(), "and it must be its own, not a pointer at the user's")
	assert.Equal(t, sha, gitHEAD(t, workDir))

	// The upstream change still has to arrive — severing must not cost the resync.
	body, err := os.ReadFile(filepath.Join(workDir, "file.txt")) //nolint:gosec // G304: test temp dir
	require.NoError(t, err)
	assert.Equal(t, "v2 upstream\n", string(body))

	// And the user's repo must be exactly as it was.
	assert.Equal(t, hostHead, gitHEAD(t, wt), "the user's branch must not have moved")
	assert.Equal(t, hostStatus, testutil.RunGitOutput(t, wt, "status", "--porcelain"))
}

// A normal repo rsyncs its real .git across, and adopting that history as the
// baseline is the intended behaviour — the sever targets links, not repos.
//
// The work copy starts empty rather than carrying a baseline repo of its own:
// rsync merges directory trees instead of replacing them, so an existing .git
// would be unioned with the source's rather than overwritten, and what survives
// depends on rsync's size+mtime quick check. That is DF118, not this test's
// subject, and asserting through it would only make this flaky.
func TestResyncWorkCopy_FromNormalRepo_AdoptsHostHistory(t *testing.T) {
	requireRsync(t)
	origDir := filepath.Join(t.TempDir(), "orig")
	require.NoError(t, os.MkdirAll(origDir, 0o750))
	testutil.InitGitRepo(t, origDir)
	testutil.WriteFile(t, origDir, "file.txt", "v1\n")
	testutil.GitAdd(t, origDir, ".")
	testutil.GitCommit(t, origDir, "initial")

	workDir := filepath.Join(t.TempDir(), "work")
	require.NoError(t, os.MkdirAll(workDir, 0o750))

	sha, err := resyncWorkCopy(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), testutil.GitEnv(), origDir, workDir)
	require.NoError(t, err)

	assert.Equal(t, gitHEAD(t, origDir), sha, "a real repo's history rsyncs across, so its HEAD is the baseline")
	assert.Equal(t, "initial", testutil.RunGitOutput(t, workDir, "log", "-1", "--format=%s"))
	info, err := os.Lstat(filepath.Join(workDir, ".git"))
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "a real .git directory is history worth keeping, not a link to sever")
}

// A non-repo source has no history to adopt, so the copy gets a fresh baseline.
func TestResyncWorkCopy_FromPlainDir_CreatesFreshBaseline(t *testing.T) {
	requireRsync(t)
	origDir := filepath.Join(t.TempDir(), "orig")
	require.NoError(t, os.MkdirAll(origDir, 0o750))
	testutil.WriteFile(t, origDir, "file.txt", "v1\n")

	workDir := newWorkCopy(t, "stale\n")

	sha, err := resyncWorkCopy(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), testutil.GitEnv(), origDir, workDir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)
	assert.Equal(t, sha, gitHEAD(t, workDir))
}
