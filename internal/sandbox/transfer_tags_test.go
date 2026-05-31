// ABOUTME: Tests for host-side tag transfer — covers the provided-shaMap path,
// ABOUTME: the matching fallback, unmatched tags, and the git-target check.
package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/testutil"
)

func TestTransferTags_EmptyIsNoOp(t *testing.T) {
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))
	createTestSandbox(t, tmp, "box", filepath.Join(tmp, "host"), store.DirModeCopy)

	res, err := TransferTags(layout, "box", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, &TransferTagsResult{}, res)
}

func TestTransferTags_ProvidedSHAMap(t *testing.T) {
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))

	hostDir := filepath.Join(tmp, "host")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	testutil.InitGitRepo(t, hostDir)
	testutil.WriteFile(t, hostDir, "a.txt", "hello")
	testutil.GitAdd(t, hostDir, "a.txt")
	testutil.GitCommit(t, hostDir, "first")
	hostSHA := testutil.GitRevParse(t, hostDir)

	createTestSandbox(t, tmp, "box", hostDir, store.DirModeCopy)

	tags := []TagInfo{
		{Name: "v1", SHA: "AAAA1111", Message: "release one"},
		{Name: "v2", SHA: "bbbb2222"},
	}
	shaMap := map[string]string{
		"aaaa1111": hostSHA,
		"bbbb2222": hostSHA,
	}

	res, err := TransferTags(layout, "box", tags, shaMap)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Applied)
	assert.Equal(t, 0, res.Skipped)
	for _, o := range res.Outcomes {
		assert.True(t, o.Applied, "tag %s should be applied", o.Name)
	}
	assert.Equal(t, hostSHA, testutil.RunGitOutput(t, hostDir, "rev-list", "-n", "1", "v1"))
	assert.Equal(t, hostSHA, testutil.RunGitOutput(t, hostDir, "rev-list", "-n", "1", "v2"))
}

func TestTransferTags_UnmatchedTag(t *testing.T) {
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))

	hostDir := filepath.Join(tmp, "host")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	testutil.InitGitRepo(t, hostDir)
	testutil.WriteFile(t, hostDir, "a.txt", "hello")
	testutil.GitAdd(t, hostDir, "a.txt")
	testutil.GitCommit(t, hostDir, "first")
	hostSHA := testutil.GitRevParse(t, hostDir)

	createTestSandbox(t, tmp, "box", hostDir, store.DirModeCopy)

	tags := []TagInfo{
		{Name: "v1", SHA: "aaaa1111"},
		{Name: "orphan", SHA: "ffff9999"},
	}
	shaMap := map[string]string{"aaaa1111": hostSHA}

	res, err := TransferTags(layout, "box", tags, shaMap)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	assert.Equal(t, 1, res.Skipped)

	byName := map[string]TagOutcome{}
	for _, o := range res.Outcomes {
		byName[o.Name] = o
	}
	assert.True(t, byName["v1"].Applied)
	assert.True(t, byName["orphan"].Unmatched)
	assert.False(t, byName["orphan"].Applied)
}

func TestTransferTags_MatchingFallback(t *testing.T) {
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))

	// Pinned author date so the independent host/work commits match on
	// (author, timestamp, subject) inside BuildSHAMapByMatching.
	const date = "@1700000000 +0000"

	hostDir := filepath.Join(tmp, "host")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	testutil.InitGitRepo(t, hostDir)
	testutil.WriteFile(t, hostDir, "a.txt", "hello")
	testutil.GitAdd(t, hostDir, "a.txt")
	testutil.RunGit(t, hostDir, "commit", "-m", "shared subject", "--date", date)
	hostSHA := testutil.GitRevParse(t, hostDir)

	createTestSandbox(t, tmp, "box", hostDir, store.DirModeCopy)

	// Sandbox work copy: an independent repo whose commit carries identical
	// metadata so the matching fallback (empty shaMap) can pair it to the host.
	workDir := store.WorkDir(layout.SandboxDir("box"), hostDir)
	require.NoError(t, os.MkdirAll(workDir, 0750))
	testutil.InitGitRepo(t, workDir)
	testutil.WriteFile(t, workDir, "a.txt", "hello")
	testutil.GitAdd(t, workDir, "a.txt")
	testutil.RunGit(t, workDir, "commit", "-m", "shared subject", "--date", date)
	sandboxSHA := testutil.GitRevParse(t, workDir)

	tags := []TagInfo{{Name: "v1", SHA: sandboxSHA}}

	res, err := TransferTags(layout, "box", tags, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Applied)
	assert.Equal(t, hostSHA, testutil.RunGitOutput(t, hostDir, "rev-list", "-n", "1", "v1"))
}

func TestTargetIsGitRepo(t *testing.T) {
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))

	gitDir := filepath.Join(tmp, "git")
	require.NoError(t, os.MkdirAll(gitDir, 0750))
	testutil.InitGitRepo(t, gitDir)
	createTestSandbox(t, tmp, "gitbox", gitDir, store.DirModeCopy)

	plainDir := filepath.Join(tmp, "plain")
	require.NoError(t, os.MkdirAll(plainDir, 0750))
	createTestSandbox(t, tmp, "plainbox", plainDir, store.DirModeCopy)

	isGit, err := TargetIsGitRepo(layout, "gitbox")
	require.NoError(t, err)
	assert.True(t, isGit)

	isGit, err = TargetIsGitRepo(layout, "plainbox")
	require.NoError(t, err)
	assert.False(t, isGit)
}
