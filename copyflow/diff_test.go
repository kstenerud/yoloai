// ABOUTME: Unit tests for diff generation across :copy, :overlay, and :rw sandbox modes.
// ABOUTME: Tests loadDiffContext, loadAllDiffContexts, GenerateDiff, and related helpers.

package copyflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/store"
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

	// Write environment.json
	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    hostPath,
			MountPath:   hostPath,
			Mode:        "copy",
			BaselineSHA: sha,
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	return workDir
}

func createRWSandbox(t *testing.T, tmpDir, name, hostPath string) {
	t.Helper()

	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:  hostPath,
			MountPath: hostPath,
			Mode:      "rw",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))
}

func gitHEAD(t *testing.T, dir string) string {
	t.Helper()
	sha, err := git.NewTestHostWithEnv(testEnv()).HeadSHA(context.Background(), dir)
	require.NoError(t, err)
	return sha
}

// GenerateDiff tests

func TestGenerateDiff_CopyMode_ModifiedFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-mod", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified content\n")

	rt := hostGitRuntime()
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

	rt := hostGitRuntime()
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

	rt := hostGitRuntime()
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

	rt := hostGitRuntime()
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

	rt := hostGitRuntime()
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

	rt := hostGitRuntime()
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

	rt := hostGitRuntime()
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

	rt := hostGitRuntime()
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

	_, _, _, err := loadDiffContext(testLayout(tmpDir), "nonexistent", "")
	assert.ErrorIs(t, err, store.ErrSandboxNotFound)
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
	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-cdiff-single", "")
	require.NoError(t, err)
	require.Len(t, commits, 2)

	result, err := GenerateCommitDiff(context.Background(), CommitDiffOptions{
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

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-cdiff-range", "")
	require.NoError(t, err)
	require.Len(t, commits, 3)

	// Range: first..third should include second and third
	ref := commits[0].SHA + ".." + commits[2].SHA
	result, err := GenerateCommitDiff(context.Background(), CommitDiffOptions{
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

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-cdiff-stat", "")
	require.NoError(t, err)
	require.Len(t, commits, 1)

	result, err := GenerateCommitDiff(context.Background(), CommitDiffOptions{
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

	_, err := GenerateCommitDiff(context.Background(), CommitDiffOptions{
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

	rt := hostGitRuntime()
	commits, err := ListCommitsWithStats(context.Background(), testLayout(tmpDir), rt, "test-lcws-none", "")
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

	rt := hostGitRuntime()
	commits, err := ListCommitsWithStats(context.Background(), testLayout(tmpDir), rt, "test-lcws-stats", "")
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

	meta := &store.Environment{
		Name:      "no-baseline",
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath: "/tmp/test",
			Mode:     "copy",
			// BaselineSHA intentionally empty
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	_, _, _, err := loadDiffContext(testLayout(tmpDir), "no-baseline", "")
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

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    hostPath,
			MountPath:   hostPath,
			Mode:        "copy",
			BaselineSHA: "abc123",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	workDir, baselineSHA, mode, err := loadDiffContext(testLayout(tmpDir), name, "")
	require.NoError(t, err)
	assert.Equal(t, store.DirModeCopy, mode)
	assert.Equal(t, "abc123", baselineSHA)
	assert.Equal(t, store.WorkDir(sandboxDir, hostPath), workDir)
}

func TestLoadDiffContext_OverlayMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "ctx-overlay"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    "/tmp/project",
			MountPath:   "/container/project",
			Mode:        "overlay",
			BaselineSHA: "overlay-sha",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	workDir, baselineSHA, mode, err := loadDiffContext(testLayout(tmpDir), name, "")
	require.NoError(t, err)
	assert.Equal(t, store.DirModeOverlay, mode)
	assert.Equal(t, "overlay-sha", baselineSHA)
	assert.Equal(t, "/container/project", workDir)
}

func TestLoadDiffContext_OverlayMode_FallbackToHostPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "ctx-overlay-fallback"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath: "/tmp/project",
			Mode:     "overlay",
			// MountPath intentionally empty
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	workDir, _, mode, err := loadDiffContext(testLayout(tmpDir), name, "")
	require.NoError(t, err)
	assert.Equal(t, store.DirModeOverlay, mode)
	assert.Equal(t, "/tmp/project", workDir)
}

func TestLoadDiffContext_RWMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "ctx-rw"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath: "/tmp/project",
			Mode:     "rw",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	workDir, baselineSHA, mode, err := loadDiffContext(testLayout(tmpDir), name, "")
	require.NoError(t, err)
	assert.Equal(t, store.DirModeRW, mode)
	assert.Equal(t, "HEAD", baselineSHA)
	assert.Equal(t, "/tmp/project", workDir)
}

func TestLoadDiffContext_UnsupportedMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "ctx-unknown"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath: "/tmp/project",
			Mode:     "bogus",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	_, _, _, err := loadDiffContext(testLayout(tmpDir), name, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported workdir mode")
}

func TestLoadDiffContext_DirSelector(t *testing.T) {
	// Verifies that a non-empty dirHostPath selects the matching dir, not Dirs[0].
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "ctx-selector"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{
			{HostPath: "/tmp/first", Mode: "copy", BaselineSHA: "sha-first"},
			{HostPath: "/tmp/second", Mode: "copy", BaselineSHA: "sha-second"},
		},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	// "" selects Dirs[0]
	_, baseline, _, err := loadDiffContext(testLayout(tmpDir), name, "")
	require.NoError(t, err)
	assert.Equal(t, "sha-first", baseline)

	// "/tmp/second" selects Dirs[1]
	_, baseline, _, err = loadDiffContext(testLayout(tmpDir), name, "/tmp/second")
	require.NoError(t, err)
	assert.Equal(t, "sha-second", baseline)

	// unknown path returns error
	_, _, _, err = loadDiffContext(testLayout(tmpDir), name, "/tmp/nope")
	assert.Error(t, err)
}

// --- loadAllDiffContexts tests ---

func TestLoadAllDiffContexts_SingleCopyWorkdir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "all-copy"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    "/tmp/project",
			MountPath:   "/tmp/project",
			Mode:        "copy",
			BaselineSHA: "sha1",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	contexts, err := loadAllDiffContexts(testLayout(tmpDir), name, "")
	require.NoError(t, err)
	require.Len(t, contexts, 1)
	assert.Equal(t, store.DirModeCopy, contexts[0].Mode)
	assert.Equal(t, "/tmp/project", contexts[0].HostPath)
	assert.Equal(t, "sha1", contexts[0].BaselineSHA)
}

// Q-U (2026-05-25): the workdir is the only diffable directory.
// loadAllDiffContexts ignores meta.Directories entirely — aux dirs
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

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    "/tmp/project",
			Mode:        "copy",
			BaselineSHA: "sha-main",
		}},
		LegacyDirectories: []store.DirEnvironment{
			// Pre-Q-U sandboxes may have these on disk. We must ignore
			// them rather than fault.
			{HostPath: "/tmp/aux-rw", Mode: "rw"},
			{HostPath: "/tmp/aux-ro", Mode: "ro"},
		},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	contexts, err := loadAllDiffContexts(testLayout(tmpDir), name, "")
	require.NoError(t, err)
	require.Len(t, contexts, 1)
	assert.Equal(t, store.DirModeCopy, contexts[0].Mode)
	assert.Equal(t, "/tmp/project", contexts[0].HostPath)
	assert.Equal(t, "sha-main", contexts[0].BaselineSHA)
}

func TestLoadAllDiffContexts_NoAuxDirs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "all-noaux"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    "/tmp/project",
			Mode:        "copy",
			BaselineSHA: "sha-only",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	contexts, err := loadAllDiffContexts(testLayout(tmpDir), name, "")
	require.NoError(t, err)
	require.Len(t, contexts, 1)
	assert.Equal(t, "/tmp/project", contexts[0].HostPath)
}

// ─── parseNumstat ────────────────────────────────────────────────────────────

func TestParseNumstat_Normal(t *testing.T) {
	input := "1\t2\tfoo.go\n3\t0\tbar.go\n"
	got := parseNumstat(input)
	require.Len(t, got, 2)
	assert.Equal(t, FileChange{Path: "foo.go", Additions: 1, Deletions: 2}, got[0])
	assert.Equal(t, FileChange{Path: "bar.go", Additions: 3, Deletions: 0}, got[1])
}

func TestParseNumstat_Binary(t *testing.T) {
	input := "-\t-\timg.png\n"
	got := parseNumstat(input)
	require.Len(t, got, 1)
	assert.Equal(t, FileChange{Path: "img.png", Additions: -1, Deletions: -1, Binary: true}, got[0])
}

func TestParseNumstat_Empty(t *testing.T) {
	assert.Nil(t, parseNumstat(""))
}

func TestParseNumstat_TrailingNewline(t *testing.T) {
	input := "5\t3\tmain.go\n"
	got := parseNumstat(input)
	require.Len(t, got, 1)
	assert.Equal(t, FileChange{Path: "main.go", Additions: 5, Deletions: 3}, got[0])
}

func TestParseNumstat_Mixed(t *testing.T) {
	input := "10\t4\ta.go\n-\t-\tb.png\n0\t7\tc.go\n"
	got := parseNumstat(input)
	require.Len(t, got, 3)
	assert.Equal(t, FileChange{Path: "a.go", Additions: 10, Deletions: 4}, got[0])
	assert.Equal(t, FileChange{Path: "b.png", Additions: -1, Deletions: -1, Binary: true}, got[1])
	assert.Equal(t, FileChange{Path: "c.go", Additions: 0, Deletions: 7}, got[2])
}

func TestParseNumstat_Rename(t *testing.T) {
	// git diff --numstat emits "{old => new}/file.go" as the path for renames.
	// parseNumstat stores the raw third field verbatim (SplitN with 3 parts).
	input := "2\t0\t{old => new}/file.go\n"
	got := parseNumstat(input)
	require.Len(t, got, 1)
	assert.Equal(t, FileChange{Path: "{old => new}/file.go", Additions: 2, Deletions: 0}, got[0])
}

func TestLoadAllDiffContexts_OverlayWorkdirWithMountPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "all-overlay-mount"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &store.Environment{
		Name:      name,
		CreatedAt: time.Now(),
		Dirs: []store.DirEnvironment{{
			HostPath:    "/host/project",
			MountPath:   "/container/project",
			Mode:        "overlay",
			BaselineSHA: "sha-ovl",
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	contexts, err := loadAllDiffContexts(testLayout(tmpDir), name, "")
	require.NoError(t, err)
	require.Len(t, contexts, 1)
	assert.Equal(t, store.DirModeOverlay, contexts[0].Mode)
	assert.Equal(t, "/container/project", contexts[0].WorkDir)
	assert.Equal(t, "/host/project", contexts[0].HostPath)
}
