package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createCopySandbox sets up a sandbox directory structure with a :copy mode
// workdir containing a git repo at baseline. Returns the sandbox name and
// work directory path.
func createCopySandbox(t *testing.T, tmpDir, name, hostPath string) string {
	t.Helper()

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", EncodePath(hostPath))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	// Init git repo and create baseline
	initGitRepo(t, workDir)
	writeTestFile(t, workDir, "file.txt", "original content\n")
	gitAdd(t, workDir, ".")
	gitCommit(t, workDir, "yoloai baseline")

	// Get baseline SHA
	sha := gitHEAD(t, workDir)

	// Write meta.json
	meta := &Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:    hostPath,
			MountPath:   hostPath,
			Mode:        "copy",
			BaselineSHA: sha,
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	return workDir
}

func createRWSandbox(t *testing.T, tmpDir, name, hostPath string) {
	t.Helper()

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      "rw",
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))
}

func gitHEAD(t *testing.T, dir string) string {
	t.Helper()
	sha, err := gitHeadSHA(dir)
	require.NoError(t, err)
	return sha
}

// GenerateDiff tests

func TestGenerateDiff_CopyMode_ModifiedFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-mod", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified content\n")

	result, err := GenerateDiff(DiffOptions{Name: "test-mod"})
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Equal(t, "copy", result.Mode)
	assert.Contains(t, result.Output, "modified content")
	assert.Contains(t, result.Output, "original content")
}

func TestGenerateDiff_CopyMode_UntrackedFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-new", "/tmp/project")
	writeTestFile(t, workDir, "created.txt", "new file content\n")

	result, err := GenerateDiff(DiffOptions{Name: "test-new"})
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Contains(t, result.Output, "created.txt")
	assert.Contains(t, result.Output, "new file content")
}

func TestGenerateDiff_CopyMode_BinaryFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-bin", "/tmp/project")
	// Write binary content (contains null bytes)
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "image.bin"),
		[]byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0x01, 0x02, 0x03},
		0600,
	))

	result, err := GenerateDiff(DiffOptions{Name: "test-bin"})
	require.NoError(t, err)
	assert.False(t, result.Empty)
	// --binary produces a GIT binary patch
	assert.Contains(t, result.Output, "GIT binary patch")
}

func TestGenerateDiff_CopyMode_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandbox(t, tmpDir, "test-empty", "/tmp/project")
	// No modifications

	result, err := GenerateDiff(DiffOptions{Name: "test-empty"})
	require.NoError(t, err)
	assert.True(t, result.Empty)
}

func TestGenerateDiff_CopyMode_PathFilter(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-filter", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified\n")
	writeTestFile(t, workDir, "other.txt", "also new\n")

	result, err := GenerateDiff(DiffOptions{
		Name:  "test-filter",
		Paths: []string{"file.txt"},
	})
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Contains(t, result.Output, "file.txt")
	assert.NotContains(t, result.Output, "other.txt")
}

func TestGenerateDiff_RWMode_GitRepo(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create a live host dir that is a git repo
	hostDir := filepath.Join(tmpDir, "host-project")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	initGitRepo(t, hostDir)
	writeTestFile(t, hostDir, "file.txt", "original\n")
	gitAdd(t, hostDir, ".")
	gitCommit(t, hostDir, "initial")

	createRWSandbox(t, tmpDir, "test-rw", hostDir)

	// Modify the live file
	writeTestFile(t, hostDir, "file.txt", "modified\n")

	result, err := GenerateDiff(DiffOptions{Name: "test-rw"})
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Equal(t, "rw", result.Mode)
	assert.Contains(t, result.Output, "modified")
}

func TestGenerateDiff_RWMode_NotGitRepo(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	hostDir := filepath.Join(tmpDir, "plain-dir")
	require.NoError(t, os.MkdirAll(hostDir, 0750))

	createRWSandbox(t, tmpDir, "test-rw-nogit", hostDir)

	result, err := GenerateDiff(DiffOptions{Name: "test-rw-nogit"})
	require.NoError(t, err)
	assert.True(t, result.Empty)
	assert.Contains(t, result.Output, "not a git repository")
}

// GenerateDiffStat tests

func TestGenerateDiffStat_CopyMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-stat", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified content\n")
	writeTestFile(t, workDir, "new.txt", "added file\n")

	result, err := GenerateDiffStat(DiffOptions{Name: "test-stat"})
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Contains(t, result.Output, "file.txt")
	assert.Contains(t, result.Output, "new.txt")
	// Stat output contains insertion/deletion counts
	assert.Contains(t, result.Output, "insertion")
}

// loadDiffContext tests

func TestLoadDiffContext_SandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	_, _, _, err := loadDiffContext("nonexistent")
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

// GenerateCommitDiff tests

func TestGenerateCommitDiff_SingleCommit(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandboxWithCommits(t, tmpDir, "test-cdiff-single", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add feature", "feature.txt", "feature content\n"},
		{"add other", "other.txt", "other content\n"},
	})

	// Get SHA of first commit
	commits, err := ListCommitsBeyondBaseline("test-cdiff-single")
	require.NoError(t, err)
	require.Len(t, commits, 2)

	result, err := GenerateCommitDiff(CommitDiffOptions{
		Name: "test-cdiff-single",
		Ref:  commits[0].SHA,
	})
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Contains(t, result.Output, "feature.txt")
	assert.NotContains(t, result.Output, "other.txt")
	_ = workDir
}

func TestGenerateCommitDiff_Range(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-cdiff-range", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"first", "a.txt", "a\n"},
		{"second", "b.txt", "b\n"},
		{"third", "c.txt", "c\n"},
	})

	commits, err := ListCommitsBeyondBaseline("test-cdiff-range")
	require.NoError(t, err)
	require.Len(t, commits, 3)

	// Range: first..third should include second and third
	ref := commits[0].SHA + ".." + commits[2].SHA
	result, err := GenerateCommitDiff(CommitDiffOptions{
		Name: "test-cdiff-range",
		Ref:  ref,
	})
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Contains(t, result.Output, "b.txt")
	assert.Contains(t, result.Output, "c.txt")
}

func TestGenerateCommitDiff_Stat(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-cdiff-stat", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add feature", "feature.txt", "feature content\n"},
	})

	commits, err := ListCommitsBeyondBaseline("test-cdiff-stat")
	require.NoError(t, err)
	require.Len(t, commits, 1)

	result, err := GenerateCommitDiff(CommitDiffOptions{
		Name: "test-cdiff-stat",
		Ref:  commits[0].SHA,
		Stat: true,
	})
	require.NoError(t, err)
	assert.False(t, result.Empty)
	assert.Contains(t, result.Output, "feature.txt")
	assert.Contains(t, result.Output, "1 file changed")
}

func TestGenerateCommitDiff_RWError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	hostDir := filepath.Join(tmpDir, "rw-dir")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	createRWSandbox(t, tmpDir, "test-cdiff-rw", hostDir)

	_, err := GenerateCommitDiff(CommitDiffOptions{
		Name: "test-cdiff-rw",
		Ref:  "abc123",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), ":rw directories")
}

// ListCommitsWithStats tests

func TestListCommitsWithStats_NoCommits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandbox(t, tmpDir, "test-lcws-none", "/tmp/project")

	commits, err := ListCommitsWithStats("test-lcws-none")
	require.NoError(t, err)
	assert.Empty(t, commits)
}

func TestListCommitsWithStats_HasStats(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-lcws-stats", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add feature", "feature.txt", "feature content\n"},
		{"add other", "other.txt", "other content\n"},
	})

	commits, err := ListCommitsWithStats("test-lcws-stats")
	require.NoError(t, err)
	require.Len(t, commits, 2)
	assert.Equal(t, "add feature", commits[0].Subject)
	assert.Contains(t, commits[0].Stat, "feature.txt")
	assert.Equal(t, "add other", commits[1].Subject)
	assert.Contains(t, commits[1].Stat, "other.txt")
}

// loadDiffContext tests

func TestLoadDiffContext_NoBaseline(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "no-baseline")
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &Meta{
		Name:      "no-baseline",
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: WorkdirMeta{
			HostPath: "/tmp/test",
			Mode:     "copy",
			// BaselineSHA intentionally empty
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	_, _, _, err := loadDiffContext("no-baseline")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no baseline SHA")
}
