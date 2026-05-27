// ABOUTME: Unit tests for diff generation across :copy, :overlay, and :rw sandbox modes.
// ABOUTME: Tests loadDiffContext, LoadAllDiffContexts, GenerateDiff, and related helpers.

package patch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
)

// createCopySandbox sets up a sandbox directory structure with a :copy mode
// workdir containing a git repo at baseline. Returns the sandbox name and
// work directory path.
func createCopySandbox(t *testing.T, tmpDir, name, hostPath string) string {
	t.Helper()

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(hostPath))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	// Init git repo and create baseline
	initGitRepo(t, workDir)
	writeTestFile(t, workDir, "file.txt", "original content\n")
	gitAdd(t, workDir, ".")
	gitCommit(t, workDir, "yoloai baseline")

	// Get baseline SHA
	sha := gitHEAD(t, workDir)

	// Write meta.json
	meta := &store.Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: store.WorkdirMeta{
			HostPath:    hostPath,
			MountPath:   hostPath,
			Mode:        "copy",
			BaselineSHA: sha,
		},
	}
	require.NoError(t, store.SaveMeta(sandboxDir, meta))

	return workDir
}

func createRWSandbox(t *testing.T, tmpDir, name, hostPath string) {
	t.Helper()

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: store.WorkdirMeta{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      "rw",
		},
	}
	require.NoError(t, store.SaveMeta(sandboxDir, meta))
}

func gitHEAD(t *testing.T, dir string) string {
	t.Helper()
	sha, err := workspace.HeadSHA(dir)
	require.NoError(t, err)
	return sha
}

// GenerateDiff tests

func TestGenerateDiff_CopyMode_ModifiedFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-mod", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified content\n")

	rt := getTestRuntime(t)
	result, err := GenerateDiff(context.Background(), DiffOptions{Name: "test-mod", Layout: testLayout(tmpDir), Runtime: rt})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "modified content")
	assert.Contains(t, result, "original content")
}

func TestGenerateDiff_CopyMode_UntrackedFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-new", "/tmp/project")
	writeTestFile(t, workDir, "created.txt", "new file content\n")

	rt := getTestRuntime(t)
	result, err := GenerateDiff(context.Background(), DiffOptions{Name: "test-new", Layout: testLayout(tmpDir), Runtime: rt})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "created.txt")
	assert.Contains(t, result, "new file content")
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

	rt := getTestRuntime(t)
	result, err := GenerateDiff(context.Background(), DiffOptions{Name: "test-bin", Layout: testLayout(tmpDir), Runtime: rt})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	// --binary produces a GIT binary patch
	assert.Contains(t, result, "GIT binary patch")
}

func TestGenerateDiff_CopyMode_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandbox(t, tmpDir, "test-empty", "/tmp/project")
	// No modifications

	rt := getTestRuntime(t)
	result, err := GenerateDiff(context.Background(), DiffOptions{Name: "test-empty", Layout: testLayout(tmpDir), Runtime: rt})
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestGenerateDiff_CopyMode_PathFilter(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-filter", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified\n")
	writeTestFile(t, workDir, "other.txt", "also new\n")

	rt := getTestRuntime(t)
	result, err := GenerateDiff(context.Background(), DiffOptions{
		Name:    "test-filter",
		Layout:  testLayout(tmpDir),
		Paths:   []string{"file.txt"},
		Runtime: rt,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "file.txt")
	assert.NotContains(t, result, "other.txt")
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

	rt := getTestRuntime(t)
	result, err := GenerateDiff(context.Background(), DiffOptions{Name: "test-rw", Layout: testLayout(tmpDir), Runtime: rt})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "modified")
}

func TestGenerateDiff_RWMode_NotGitRepo(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	hostDir := filepath.Join(tmpDir, "plain-dir")
	require.NoError(t, os.MkdirAll(hostDir, 0750))

	createRWSandbox(t, tmpDir, "test-rw-nogit", hostDir)

	rt := getTestRuntime(t)
	result, err := GenerateDiff(context.Background(), DiffOptions{Name: "test-rw-nogit", Layout: testLayout(tmpDir), Runtime: rt})
	require.NoError(t, err)
	// Q-U: non-git :rw collapses to "no changes" (empty string)
	// rather than the previous informational message.
	assert.Empty(t, result)
}

// GenerateDiffStat tests

func TestGenerateDiffStat_CopyMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-stat", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified content\n")
	writeTestFile(t, workDir, "new.txt", "added file\n")

	rt := getTestRuntime(t)
	result, err := GenerateDiff(context.Background(), DiffOptions{Name: "test-stat", Layout: testLayout(tmpDir), Stat: true, Runtime: rt})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "file.txt")
	assert.Contains(t, result, "new.txt")
	// Stat output contains insertion/deletion counts
	assert.Contains(t, result, "insertion")
}

// loadDiffContext tests

func TestLoadDiffContext_SandboxNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	_, _, _, err := loadDiffContext(testLayout(tmpDir), "nonexistent")
	assert.ErrorIs(t, err, sandbox.ErrSandboxNotFound)
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
	rt := getTestRuntime(t)
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-cdiff-single")
	require.NoError(t, err)
	require.Len(t, commits, 2)

	result, err := GenerateCommitDiff(CommitDiffOptions{
		Layout: testLayout(tmpDir),
		Name:   "test-cdiff-single",
		Ref:    commits[0].SHA,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "feature.txt")
	assert.NotContains(t, result, "other.txt")
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

	rt := getTestRuntime(t)
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-cdiff-range")
	require.NoError(t, err)
	require.Len(t, commits, 3)

	// Range: first..third should include second and third
	ref := commits[0].SHA + ".." + commits[2].SHA
	result, err := GenerateCommitDiff(CommitDiffOptions{
		Layout: testLayout(tmpDir),
		Name:   "test-cdiff-range",
		Ref:    ref,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "b.txt")
	assert.Contains(t, result, "c.txt")
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

	rt := getTestRuntime(t)
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-cdiff-stat")
	require.NoError(t, err)
	require.Len(t, commits, 1)

	result, err := GenerateCommitDiff(CommitDiffOptions{
		Layout: testLayout(tmpDir),
		Name:   "test-cdiff-stat",
		Ref:    commits[0].SHA,
		Stat:   true,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "feature.txt")
	assert.Contains(t, result, "1 file changed")
}

func TestGenerateCommitDiff_RWError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	hostDir := filepath.Join(tmpDir, "rw-dir")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	createRWSandbox(t, tmpDir, "test-cdiff-rw", hostDir)

	_, err := GenerateCommitDiff(CommitDiffOptions{
		Layout: testLayout(tmpDir),
		Name:   "test-cdiff-rw",
		Ref:    "abc123",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), ":rw directories")
}

// ListCommitsWithStats tests

func TestListCommitsWithStats_NoCommits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandbox(t, tmpDir, "test-lcws-none", "/tmp/project")

	rt := getTestRuntime(t)
	commits, err := ListCommitsWithStats(context.Background(), testLayout(tmpDir), rt, "test-lcws-none")
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

	rt := getTestRuntime(t)
	commits, err := ListCommitsWithStats(context.Background(), testLayout(tmpDir), rt, "test-lcws-stats")
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

	meta := &store.Meta{
		Name:      "no-baseline",
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: store.WorkdirMeta{
			HostPath: "/tmp/test",
			Mode:     "copy",
			// BaselineSHA intentionally empty
		},
	}
	require.NoError(t, store.SaveMeta(sandboxDir, meta))

	_, _, _, err := loadDiffContext(testLayout(tmpDir), "no-baseline")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no baseline SHA")
}

// --- loadDiffContext mode tests ---

func TestLoadDiffContext_CopyMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "ctx-copy"
	hostPath := "/tmp/project"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: store.WorkdirMeta{
			HostPath:    hostPath,
			MountPath:   hostPath,
			Mode:        "copy",
			BaselineSHA: "abc123",
		},
	}
	require.NoError(t, store.SaveMeta(sandboxDir, meta))

	workDir, baselineSHA, mode, err := loadDiffContext(testLayout(tmpDir), name)
	require.NoError(t, err)
	assert.Equal(t, "copy", mode)
	assert.Equal(t, "abc123", baselineSHA)
	assert.Equal(t, store.WorkDir(sandboxDir, hostPath), workDir)
}

func TestLoadDiffContext_OverlayMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "ctx-overlay"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: store.WorkdirMeta{
			HostPath:    "/tmp/project",
			MountPath:   "/container/project",
			Mode:        "overlay",
			BaselineSHA: "overlay-sha",
		},
	}
	require.NoError(t, store.SaveMeta(sandboxDir, meta))

	workDir, baselineSHA, mode, err := loadDiffContext(testLayout(tmpDir), name)
	require.NoError(t, err)
	assert.Equal(t, "overlay", mode)
	assert.Equal(t, "overlay-sha", baselineSHA)
	assert.Equal(t, "/container/project", workDir)
}

func TestLoadDiffContext_OverlayMode_FallbackToHostPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "ctx-overlay-fallback"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: store.WorkdirMeta{
			HostPath: "/tmp/project",
			Mode:     "overlay",
			// MountPath intentionally empty
		},
	}
	require.NoError(t, store.SaveMeta(sandboxDir, meta))

	workDir, _, mode, err := loadDiffContext(testLayout(tmpDir), name)
	require.NoError(t, err)
	assert.Equal(t, "overlay", mode)
	assert.Equal(t, "/tmp/project", workDir)
}

func TestLoadDiffContext_RWMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "ctx-rw"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: store.WorkdirMeta{
			HostPath: "/tmp/project",
			Mode:     "rw",
		},
	}
	require.NoError(t, store.SaveMeta(sandboxDir, meta))

	workDir, baselineSHA, mode, err := loadDiffContext(testLayout(tmpDir), name)
	require.NoError(t, err)
	assert.Equal(t, "rw", mode)
	assert.Equal(t, "HEAD", baselineSHA)
	assert.Equal(t, "/tmp/project", workDir)
}

func TestLoadDiffContext_UnsupportedMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "ctx-unknown"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: store.WorkdirMeta{
			HostPath: "/tmp/project",
			Mode:     "bogus",
		},
	}
	require.NoError(t, store.SaveMeta(sandboxDir, meta))

	_, _, _, err := loadDiffContext(testLayout(tmpDir), name)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported workdir mode")
}

// --- LoadAllDiffContexts tests ---

func TestLoadAllDiffContexts_SingleCopyWorkdir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "all-copy"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: store.WorkdirMeta{
			HostPath:    "/tmp/project",
			MountPath:   "/tmp/project",
			Mode:        "copy",
			BaselineSHA: "sha1",
		},
	}
	require.NoError(t, store.SaveMeta(sandboxDir, meta))

	contexts, err := LoadAllDiffContexts(testLayout(tmpDir), name)
	require.NoError(t, err)
	require.Len(t, contexts, 1)
	assert.Equal(t, "copy", contexts[0].Mode)
	assert.Equal(t, "/tmp/project", contexts[0].HostPath)
	assert.Equal(t, "sha1", contexts[0].BaselineSHA)
}

// Q-U (2026-05-25): the workdir is the only diffable directory.
// LoadAllDiffContexts ignores meta.Directories entirely — aux dirs
// are aux mounts, not diff sources. The test regress-guards both
// "aux entries are not in the result" and "the existing meta on disk
// is tolerated when aux entries are present" (a real on-disk state
// for sandboxes created before Q-U landed).
func TestLoadAllDiffContexts_WorkdirOnly_IgnoresAuxEntries(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "workdir-only"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: store.WorkdirMeta{
			HostPath:    "/tmp/project",
			Mode:        "copy",
			BaselineSHA: "sha-main",
		},
		Directories: []store.DirMeta{
			// Pre-Q-U sandboxes may have these on disk. We must ignore
			// them rather than fault.
			{HostPath: "/tmp/aux-rw", Mode: "rw"},
			{HostPath: "/tmp/aux-ro", Mode: "ro"},
		},
	}
	require.NoError(t, store.SaveMeta(sandboxDir, meta))

	contexts, err := LoadAllDiffContexts(testLayout(tmpDir), name)
	require.NoError(t, err)
	require.Len(t, contexts, 1)
	assert.Equal(t, "copy", contexts[0].Mode)
	assert.Equal(t, "/tmp/project", contexts[0].HostPath)
	assert.Equal(t, "sha-main", contexts[0].BaselineSHA)
}

func TestLoadAllDiffContexts_NoAuxDirs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "all-noaux"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: store.WorkdirMeta{
			HostPath:    "/tmp/project",
			Mode:        "copy",
			BaselineSHA: "sha-only",
		},
	}
	require.NoError(t, store.SaveMeta(sandboxDir, meta))

	contexts, err := LoadAllDiffContexts(testLayout(tmpDir), name)
	require.NoError(t, err)
	require.Len(t, contexts, 1)
	assert.Equal(t, "/tmp/project", contexts[0].HostPath)
}

func TestLoadAllDiffContexts_OverlayWorkdirWithMountPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "all-overlay-mount"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Meta{
		Name:      name,
		Agent:     "test",
		CreatedAt: time.Now(),
		Workdir: store.WorkdirMeta{
			HostPath:    "/host/project",
			MountPath:   "/container/project",
			Mode:        "overlay",
			BaselineSHA: "sha-ovl",
		},
	}
	require.NoError(t, store.SaveMeta(sandboxDir, meta))

	contexts, err := LoadAllDiffContexts(testLayout(tmpDir), name)
	require.NoError(t, err)
	require.Len(t, contexts, 1)
	assert.Equal(t, "overlay", contexts[0].Mode)
	assert.Equal(t, "/container/project", contexts[0].WorkDir)
	assert.Equal(t, "/host/project", contexts[0].HostPath)
}
