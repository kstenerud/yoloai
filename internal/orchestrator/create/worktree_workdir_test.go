// ABOUTME: setupWorkdir against a linked-worktree workdir must never touch the
// ABOUTME: user's real repo. A worktree's .git is a pointer into the main repo,
// ABOUTME: and host git follows it, so an unsevered copy makes the baseline commit
// ABOUTME: land on the user's own branch (DF116).
package create

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// worktreeFixture builds a real repo with a linked worktree checked out on
// branch `feature`, and returns both paths.
func worktreeFixture(t *testing.T) (mainRepo, worktree string) {
	t.Helper()
	root := t.TempDir()
	mainRepo = filepath.Join(root, "main")
	worktree = filepath.Join(root, "wt")
	require.NoError(t, os.MkdirAll(mainRepo, 0o750))

	testutil.InitGitRepo(t, mainRepo)
	writeTestFile(t, mainRepo, "app.js", "v1")
	testutil.RunGit(t, mainRepo, "add", "-A")
	gitCommit(t, mainRepo, "initial")
	testutil.RunGit(t, mainRepo, "worktree", "add", "-q", worktree, "-b", "feature")
	return mainRepo, worktree
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	return testutil.RunGitOutput(t, dir, "rev-parse", "HEAD")
}

func setupCopyAllWorkdir(t *testing.T, path string) (string, error) {
	t.Helper()
	sandboxDir := filepath.Join(t.TempDir(), "sandbox")
	workdir := &DirSpec{Path: path, Mode: DirMode("copy"), IncludeIgnored: true}
	_, sha, err := setupWorkdir(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), sandboxDir, workdir, &mockDockerRuntime{})
	return sha, err
}

// The reported defect: a dirty worktree's uncommitted work gets committed onto
// the user's own branch, and `git status` then reads clean so nothing shows.
func TestSetupWorkdir_CopyAllFromDirtyWorktree_LeavesHostRepoAlone(t *testing.T) {
	mainRepo, wt := worktreeFixture(t)
	writeTestFile(t, wt, "app.js", "v1\nuncommitted work")

	headBefore := headOf(t, wt)
	mainHeadBefore := headOf(t, mainRepo)
	statusBefore := testutil.RunGitOutput(t, wt, "status", "--porcelain")
	require.NotEmpty(t, statusBefore, "fixture must be dirty or it proves nothing")

	_, err := setupCopyAllWorkdir(t, wt)
	require.NoError(t, err)

	assert.Equal(t, headBefore, headOf(t, wt), "the user's branch must not have moved")
	assert.Equal(t, statusBefore, testutil.RunGitOutput(t, wt, "status", "--porcelain"),
		"the user's uncommitted work must still be uncommitted")
	assert.Equal(t, mainHeadBefore, headOf(t, mainRepo), "the main repo's own branch must not have moved")
}

// The data-loss variant, and the nastier one: it needs no dirty repo and no
// prompt. CopyDir strips build artifacts from the work copy, so tracked ones
// surface as staged deletions and the baseline commit removes them from the
// user's branch.
func TestSetupWorkdir_CopyAllFromCleanWorktree_KeepsTrackedBuildArtifacts(t *testing.T) {
	mainRepo, wt := worktreeFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(wt, "node_modules"), 0o750))
	writeTestFile(t, wt, filepath.Join("node_modules", "lib.js"), "vendored dep")
	testutil.RunGit(t, wt, "add", "-A")
	gitCommit(t, wt, "track node_modules")

	require.Empty(t, testutil.RunGitOutput(t, wt, "status", "--porcelain"), "fixture must be clean")
	headBefore := headOf(t, wt)
	mainHeadBefore := headOf(t, mainRepo)
	filesBefore := testutil.RunGitOutput(t, wt, "ls-files")
	require.Contains(t, filesBefore, "node_modules/lib.js")

	_, err := setupCopyAllWorkdir(t, wt)
	require.NoError(t, err)

	assert.Equal(t, headBefore, headOf(t, wt), "the user's branch must not have moved")
	assert.Equal(t, filesBefore, testutil.RunGitOutput(t, wt, "ls-files"),
		"tracked files must not be deleted from the user's branch")
	assert.Empty(t, testutil.RunGitOutput(t, wt, "status", "--porcelain"),
		"a clean repo must still be clean")
	assert.Equal(t, mainHeadBefore, headOf(t, mainRepo), "the main repo's own branch must not have moved")
}

// Severing the link must still leave a usable sandbox: a real baseline commit,
// in a repo of the copy's own, holding the worktree's files.
func TestSetupWorkdir_CopyAllFromWorktree_ProducesStandaloneBaseline(t *testing.T) {
	_, wt := worktreeFixture(t)
	writeTestFile(t, wt, "app.js", "v1\nuncommitted work")

	sandboxDir := filepath.Join(t.TempDir(), "sandbox")
	workdir := &DirSpec{Path: wt, Mode: DirMode("copy"), IncludeIgnored: true}
	workCopyDir, sha, err := setupWorkdir(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), sandboxDir, workdir, &mockDockerRuntime{})
	require.NoError(t, err)

	assert.Len(t, sha, 40, "the work copy needs a baseline of its own")
	assert.NotEqual(t, headOf(t, wt), sha, "a fresh baseline is not the source repo's HEAD")

	info, err := os.Lstat(filepath.Join(workCopyDir, ".git"))
	require.NoError(t, err, "the work copy must have a repo")
	assert.True(t, info.IsDir(), "and it must be its own, not a link to the user's")

	assert.Equal(t, sha, headOf(t, workCopyDir))
	assert.Empty(t, testutil.RunGitOutput(t, workCopyDir, "status", "--porcelain"),
		"the baseline commits the copy's current state, so the agent starts clean")

	body, err := os.ReadFile(filepath.Join(workCopyDir, "app.js")) //nolint:gosec // G304: test temp dir
	require.NoError(t, err)
	assert.Equal(t, "v1\nuncommitted work", string(body), "the agent must see the work in progress")
}

// The default mode was already safe; keep it that way.
func TestSetupWorkdir_CopyFromWorktree_LeavesHostRepoAlone(t *testing.T) {
	_, wt := worktreeFixture(t)
	writeTestFile(t, wt, "app.js", "v1\nuncommitted work")

	headBefore := headOf(t, wt)
	statusBefore := testutil.RunGitOutput(t, wt, "status", "--porcelain")

	sandboxDir := filepath.Join(t.TempDir(), "sandbox")
	workdir := &DirSpec{Path: wt, Mode: DirMode("copy")}
	_, sha, err := setupWorkdir(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), sandboxDir, workdir, &mockDockerRuntime{})
	require.NoError(t, err)

	assert.Len(t, sha, 40)
	assert.Equal(t, headBefore, headOf(t, wt))
	assert.Equal(t, statusBefore, testutil.RunGitOutput(t, wt, "status", "--porcelain"))
}

// A normal repo must keep its history: the sever targets links, not repos.
func TestSetupWorkdir_CopyAllFromNormalRepo_PreservesHistory(t *testing.T) {
	dir := t.TempDir()
	testutil.InitGitRepo(t, dir)
	writeTestFile(t, dir, "app.js", "v1")
	testutil.RunGit(t, dir, "add", "-A")
	gitCommit(t, dir, "initial")

	sandboxDir := filepath.Join(t.TempDir(), "sandbox")
	workdir := &DirSpec{Path: dir, Mode: DirMode("copy"), IncludeIgnored: true}
	workCopyDir, sha, err := setupWorkdir(context.Background(), git.NewTestHostWithEnv(testutil.GitEnv()), sandboxDir, workdir, &mockDockerRuntime{})
	require.NoError(t, err)

	assert.Equal(t, headOf(t, dir), sha, "a real repo's history survives, so its HEAD is the baseline")
	assert.Equal(t, "initial", testutil.RunGitOutput(t, workCopyDir, "log", "-1", "--format=%s"))
}

// DF121: :rw tracks no baseline. `yoloai baseline` says so to the user's face
// ("baseline is not tracked for :rw directories") and diff substitutes the
// literal HEAD for a live mount, so recording the source's HEAD here produced a
// value nothing read, that `sandbox info` printed anyway, and that went stale on
// the next commit.
func TestCreateDirBaseline_RWTracksNoBaseline(t *testing.T) {
	dir := t.TempDir()
	testutil.InitGitRepo(t, dir)
	writeTestFile(t, dir, "app.js", "v1")
	testutil.RunGit(t, dir, "add", "-A")
	gitCommit(t, dir, "initial")

	sha, err := createDirBaseline(context.Background(),
		git.NewTestHostWithEnv(testutil.GitEnv()),
		&DirSpec{Path: dir, Mode: DirMode("rw")},
		filepath.Join(t.TempDir(), "unused"),
		&mockDockerRuntime{})
	require.NoError(t, err)
	assert.Empty(t, sha, "a live mount has no work copy to baseline, and the rest of the code already agrees")
}

func TestCreateDirBaseline_RODirTracksNoBaseline(t *testing.T) {
	dir := t.TempDir()
	testutil.InitGitRepo(t, dir)
	writeTestFile(t, dir, "app.js", "v1")
	testutil.RunGit(t, dir, "add", "-A")
	gitCommit(t, dir, "initial")

	sha, err := createDirBaseline(context.Background(),
		git.NewTestHostWithEnv(testutil.GitEnv()),
		&DirSpec{Path: dir, Mode: DirMode("ro")},
		filepath.Join(t.TempDir(), "unused"),
		&mockDockerRuntime{})
	require.NoError(t, err)
	assert.Empty(t, sha)
}
