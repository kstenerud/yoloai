// ABOUTME: WorkCopy must hand the agent a clean starting point whatever the copy
// ABOUTME: turned out to be — a real repo, a dirty one, an empty one, or none —
// ABOUTME: because every later diff is read against the SHA it returns (DF120).
package baseline

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

func hostGit(t *testing.T) *git.Git {
	t.Helper()
	return git.NewTestHostWithEnv(testutil.GitEnv())
}

func repoWithCommit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testutil.InitGitRepo(t, dir)
	testutil.WriteFile(t, dir, "app.js", "v1\n")
	testutil.GitAdd(t, dir, ".")
	testutil.GitCommit(t, dir, "initial")
	return dir
}

// The DF120 case. A copy of a source with uncommitted work arrives dirty; if the
// baseline were HEAD, every one of those edits would read as the agent's from the
// moment the sandbox opened.
func TestWorkCopy_DirtyRepo_CommitsSoTheAgentStartsClean(t *testing.T) {
	dir := repoWithCommit(t)
	head := testutil.RunGitOutput(t, dir, "rev-parse", "HEAD")
	testutil.WriteFile(t, dir, "app.js", "v1\nthe user's work in progress\n")

	sha, err := WorkCopy(context.Background(), hostGit(t), dir)
	require.NoError(t, err)

	assert.Len(t, sha, 40)
	assert.NotEqual(t, head, sha, "the dirty state is committed, so the baseline moves past HEAD")
	assert.Equal(t, sha, testutil.RunGitOutput(t, dir, "rev-parse", "HEAD"))
	assert.Empty(t, testutil.RunGitOutput(t, dir, "status", "--porcelain"),
		"the agent starts clean, so a later diff shows only what the agent did")
	assert.Equal(t, "yoloai: pre-session state", testutil.RunGitOutput(t, dir, "log", "-1", "--format=%s"))

	// The work itself must survive being committed — it is the user's.
	body, err := os.ReadFile(filepath.Join(dir, "app.js")) //nolint:gosec // G304: test temp dir
	require.NoError(t, err)
	assert.Equal(t, "v1\nthe user's work in progress\n", string(body))
}

func TestWorkCopy_CleanRepo_KeepsHeadAsBaseline(t *testing.T) {
	dir := repoWithCommit(t)
	head := testutil.RunGitOutput(t, dir, "rev-parse", "HEAD")

	sha, err := WorkCopy(context.Background(), hostGit(t), dir)
	require.NoError(t, err)

	assert.Equal(t, head, sha, "nothing to commit, so HEAD already describes the starting point")
	assert.Equal(t, "initial", testutil.RunGitOutput(t, dir, "log", "-1", "--format=%s"),
		"and no empty pre-session commit is manufactured")
}

func TestWorkCopy_NonRepo_CreatesFreshBaseline(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteFile(t, dir, "app.js", "v1\n")

	sha, err := WorkCopy(context.Background(), hostGit(t), dir)
	require.NoError(t, err)

	assert.Len(t, sha, 40)
	assert.Equal(t, sha, testutil.RunGitOutput(t, dir, "rev-parse", "HEAD"))
	assert.Empty(t, testutil.RunGitOutput(t, dir, "status", "--porcelain"))
}

// A repo with no commits has no HEAD to read and no history worth keeping, so it
// is replaced rather than baselined against something unreadable.
func TestWorkCopy_EmptyRepo_StartsOver(t *testing.T) {
	dir := t.TempDir()
	testutil.InitGitRepo(t, dir)
	testutil.WriteFile(t, dir, "app.js", "v1\n")

	sha, err := WorkCopy(context.Background(), hostGit(t), dir)
	require.NoError(t, err)

	assert.Len(t, sha, 40)
	assert.Equal(t, sha, testutil.RunGitOutput(t, dir, "rev-parse", "HEAD"))
	assert.Empty(t, testutil.RunGitOutput(t, dir, "status", "--porcelain"))
}

func TestWorkCopy_EmptyDir_StillGetsABaseline(t *testing.T) {
	sha, err := WorkCopy(context.Background(), hostGit(t), t.TempDir())
	require.NoError(t, err)
	assert.Len(t, sha, 40, "allow-empty means even an empty dir has a starting point to diff against")
}
