package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GeneratePatch tests

func TestGeneratePatch_CopyMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-patch", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified content\n")

	patch, stat, err := GeneratePatch("test-patch", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "file.txt")
	assert.Contains(t, string(patch), "modified content")
}

func TestGeneratePatch_RWMode_Error(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	hostDir := filepath.Join(tmpDir, "rw-dir")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	createRWSandbox(t, tmpDir, "test-rw-patch", hostDir)

	_, _, err := GeneratePatch("test-rw-patch", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), ":rw directories")
}

func TestGeneratePatch_PathFilter(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-patch-filter", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "changed\n")
	writeTestFile(t, workDir, "other.txt", "also changed\n")

	patch, stat, err := GeneratePatch("test-patch-filter", []string{"file.txt"})
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "file.txt")
	assert.NotContains(t, stat, "other.txt")
	assert.NotContains(t, string(patch), "also changed")
}

func TestGeneratePatch_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandbox(t, tmpDir, "test-patch-empty", "/tmp/project")

	patch, stat, err := GeneratePatch("test-patch-empty", nil)
	require.NoError(t, err)
	assert.Empty(t, patch)
	assert.Empty(t, stat)
}

// ApplyPatch tests

func TestApplyPatch_GitTarget(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-apply-git", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified by agent\n")

	patch, _, err := GeneratePatch("test-apply-git", nil)
	require.NoError(t, err)

	// Create target git repo with original content
	targetDir := filepath.Join(tmpDir, "target-git")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	require.NoError(t, ApplyPatch(patch, targetDir, true))

	content, err := os.ReadFile(filepath.Join(targetDir, "file.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "modified by agent\n", string(content))
}

func TestApplyPatch_NonGitTarget(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-apply-nongit", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified by agent\n")

	patch, _, err := GeneratePatch("test-apply-nongit", nil)
	require.NoError(t, err)

	// Create target dir (no git) with original content
	targetDir := filepath.Join(tmpDir, "target-plain")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	writeTestFile(t, targetDir, "file.txt", "original content\n")

	require.NoError(t, ApplyPatch(patch, targetDir, false))

	content, err := os.ReadFile(filepath.Join(targetDir, "file.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "modified by agent\n", string(content))
}

func TestApplyPatch_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-apply-new", "/tmp/project")
	writeTestFile(t, workDir, "created.txt", "brand new file\n")

	patch, _, err := GeneratePatch("test-apply-new", nil)
	require.NoError(t, err)

	// Target has original file but not the new one
	targetDir := filepath.Join(tmpDir, "target-new")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	require.NoError(t, ApplyPatch(patch, targetDir, true))

	content, err := os.ReadFile(filepath.Join(targetDir, "created.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "brand new file\n", string(content))
}

func TestApplyPatch_DeleteFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create sandbox with two files at baseline, then delete one
	name := "test-apply-del"
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", name)
	hostPath := "/tmp/project"
	workDir := filepath.Join(sandboxDir, "work", EncodePath(hostPath))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	initGitRepo(t, workDir)
	writeTestFile(t, workDir, "keep.txt", "keep this\n")
	writeTestFile(t, workDir, "remove.txt", "delete me\n")
	gitAdd(t, workDir, ".")
	gitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &Meta{
		Name:  name,
		Agent: "test",
		Workdir: WorkdirMeta{
			HostPath:    hostPath,
			MountPath:   hostPath,
			Mode:        "copy",
			BaselineSHA: sha,
		},
	}
	require.NoError(t, SaveMeta(sandboxDir, meta))

	// Delete the file in work copy
	require.NoError(t, os.Remove(filepath.Join(workDir, "remove.txt")))

	patch, _, err := GeneratePatch(name, nil)
	require.NoError(t, err)

	// Target has both files
	targetDir := filepath.Join(tmpDir, "target-del")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "keep.txt", "keep this\n")
	writeTestFile(t, targetDir, "remove.txt", "delete me\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	require.NoError(t, ApplyPatch(patch, targetDir, true))

	assert.FileExists(t, filepath.Join(targetDir, "keep.txt"))
	assert.NoFileExists(t, filepath.Join(targetDir, "remove.txt"))
}

// CheckPatch tests

func TestCheckPatch_Conflict(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-conflict", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "agent version\n")

	patch, _, err := GeneratePatch("test-conflict", nil)
	require.NoError(t, err)

	// Target has different content than what patch expects
	targetDir := filepath.Join(tmpDir, "target-conflict")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "completely different content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	err = CheckPatch(patch, targetDir, true)
	assert.Error(t, err)
}

func TestCheckPatch_Clean(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-clean", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified\n")

	patch, _, err := GeneratePatch("test-clean", nil)
	require.NoError(t, err)

	// Target matches original
	targetDir := filepath.Join(tmpDir, "target-clean")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	err = CheckPatch(patch, targetDir, true)
	assert.NoError(t, err)
}

// IsGitRepo tests

func TestIsGitRepo_True(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	assert.True(t, IsGitRepo(dir))
}

func TestIsGitRepo_False(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, IsGitRepo(dir))
}

// formatApplyError tests

func TestFormatApplyError_PatchFailed(t *testing.T) {
	gitErr := fmt.Errorf("error: patch failed: handler.go:42\nerror: handler.go: patch does not apply: exit status 1")
	err := formatApplyError(gitErr, "/tmp/project")
	assert.Contains(t, err.Error(), "handler.go")
	assert.Contains(t, err.Error(), "42")
	assert.Contains(t, err.Error(), "conflict")
}

func TestFormatApplyError_Unknown(t *testing.T) {
	gitErr := fmt.Errorf("some unusual error: exit status 1")
	err := formatApplyError(gitErr, "/tmp/project")
	assert.Contains(t, err.Error(), "git apply failed")
	assert.Contains(t, err.Error(), "/tmp/project")
}

// createCopySandboxWithCommits builds on createCopySandbox and adds agent
// commits after the baseline. Each entry in commits is {subject, filename, content}.
func createCopySandboxWithCommits(t *testing.T, tmpDir, name, hostPath string, commits []struct {
	subject  string
	filename string
	content  string
}) string {
	t.Helper()
	workDir := createCopySandbox(t, tmpDir, name, hostPath)
	for _, c := range commits {
		writeTestFile(t, workDir, c.filename, c.content)
		gitAdd(t, workDir, ".")
		gitCommit(t, workDir, c.subject)
	}
	return workDir
}

// ListCommitsBeyondBaseline tests

func TestListCommitsBeyondBaseline_NoCommits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandbox(t, tmpDir, "test-list-none", "/tmp/project")

	commits, err := ListCommitsBeyondBaseline("test-list-none")
	require.NoError(t, err)
	assert.Empty(t, commits)
}

func TestListCommitsBeyondBaseline_Single(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-list-one", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add feature X", "feature.txt", "feature X\n"},
	})

	commits, err := ListCommitsBeyondBaseline("test-list-one")
	require.NoError(t, err)
	require.Len(t, commits, 1)
	assert.Equal(t, "add feature X", commits[0].Subject)
	assert.Len(t, commits[0].SHA, 40)
}

func TestListCommitsBeyondBaseline_Multiple(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-list-multi", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"first commit", "a.txt", "a\n"},
		{"second commit", "b.txt", "b\n"},
		{"third commit", "c.txt", "c\n"},
	})

	commits, err := ListCommitsBeyondBaseline("test-list-multi")
	require.NoError(t, err)
	require.Len(t, commits, 3)
	assert.Equal(t, "first commit", commits[0].Subject)
	assert.Equal(t, "second commit", commits[1].Subject)
	assert.Equal(t, "third commit", commits[2].Subject)
}

func TestListCommitsBeyondBaseline_RWError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	hostDir := filepath.Join(tmpDir, "rw-dir")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	createRWSandbox(t, tmpDir, "test-list-rw", hostDir)

	_, err := ListCommitsBeyondBaseline("test-list-rw")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), ":rw directories")
}

// HasUncommittedChanges tests

func TestHasUncommittedChanges_Clean(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandbox(t, tmpDir, "test-wip-clean", "/tmp/project")

	has, err := HasUncommittedChanges("test-wip-clean")
	require.NoError(t, err)
	assert.False(t, has)
}

func TestHasUncommittedChanges_Modified(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-wip-mod", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified\n")

	has, err := HasUncommittedChanges("test-wip-mod")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestHasUncommittedChanges_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-wip-new", "/tmp/project")
	writeTestFile(t, workDir, "brand-new.txt", "new file\n")

	has, err := HasUncommittedChanges("test-wip-new")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestHasUncommittedChanges_OnlyCommits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-wip-committed", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add stuff", "new.txt", "committed\n"},
	})

	has, err := HasUncommittedChanges("test-wip-committed")
	require.NoError(t, err)
	assert.False(t, has)
}

// GenerateFormatPatch tests

func TestGenerateFormatPatch_Single(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-fp-one", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add feature", "feature.txt", "feature\n"},
	})

	patchDir, files, err := GenerateFormatPatch("test-fp-one", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	require.Len(t, files, 1)
	assert.Contains(t, files[0], ".patch")

	// Verify patch file contains the commit subject
	data, err := os.ReadFile(filepath.Join(patchDir, files[0])) //nolint:gosec
	require.NoError(t, err)
	assert.Contains(t, string(data), "add feature")
}

func TestGenerateFormatPatch_Multiple(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-fp-multi", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"first", "a.txt", "a\n"},
		{"second", "b.txt", "b\n"},
		{"third", "c.txt", "c\n"},
	})

	patchDir, files, err := GenerateFormatPatch("test-fp-multi", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	require.Len(t, files, 3)

	// Patches should be in order
	data0, _ := os.ReadFile(filepath.Join(patchDir, files[0])) //nolint:gosec
	data2, _ := os.ReadFile(filepath.Join(patchDir, files[2])) //nolint:gosec
	assert.Contains(t, string(data0), "first")
	assert.Contains(t, string(data2), "third")
}

func TestGenerateFormatPatch_NoCommits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandbox(t, tmpDir, "test-fp-empty", "/tmp/project")

	patchDir, files, err := GenerateFormatPatch("test-fp-empty", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	assert.Empty(t, files)
}

func TestGenerateFormatPatch_PathFilter(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-fp-filter", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"change a", "a.txt", "a content\n"},
		{"change b", "b.txt", "b content\n"},
	})

	patchDir, files, err := GenerateFormatPatch("test-fp-filter", []string{"a.txt"})
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	require.Len(t, files, 1)
	data, _ := os.ReadFile(filepath.Join(patchDir, files[0])) //nolint:gosec
	assert.Contains(t, string(data), "change a")
}

// GenerateWIPDiff tests

func TestGenerateWIPDiff_NoChanges(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandbox(t, tmpDir, "test-wip-diff-none", "/tmp/project")

	patch, stat, err := GenerateWIPDiff("test-wip-diff-none", nil)
	require.NoError(t, err)
	assert.Empty(t, patch)
	assert.Empty(t, stat)
}

func TestGenerateWIPDiff_Modified(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandboxWithCommits(t, tmpDir, "test-wip-diff-mod", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"committed change", "committed.txt", "committed\n"},
	})

	// Add uncommitted changes on top
	writeTestFile(t, workDir, "wip.txt", "work in progress\n")

	patch, stat, err := GenerateWIPDiff("test-wip-diff-mod", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "wip.txt")
	assert.Contains(t, string(patch), "work in progress")
	// Should NOT include the committed change
	assert.NotContains(t, string(patch), "committed.txt")
}

func TestGenerateWIPDiff_PathFilter(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-wip-diff-filter", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "changed\n")
	writeTestFile(t, workDir, "other.txt", "also changed\n")

	patch, stat, err := GenerateWIPDiff("test-wip-diff-filter", []string{"file.txt"})
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "file.txt")
	assert.NotContains(t, stat, "other.txt")
}

// ApplyFormatPatch tests

func TestApplyFormatPatch_Single(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-am-one", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add feature", "feature.txt", "feature content\n"},
	})

	patchDir, files, err := GenerateFormatPatch("test-am-one", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	// Create target git repo with matching baseline
	targetDir := filepath.Join(tmpDir, "target-am-one")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	require.NoError(t, ApplyFormatPatch(patchDir, files, targetDir))

	content, err := os.ReadFile(filepath.Join(targetDir, "feature.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "feature content\n", string(content))
}

func TestApplyFormatPatch_Multiple(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-am-multi", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"first commit", "a.txt", "a\n"},
		{"second commit", "b.txt", "b\n"},
	})

	patchDir, files, err := GenerateFormatPatch("test-am-multi", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	// Create target
	targetDir := filepath.Join(tmpDir, "target-am-multi")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	require.NoError(t, ApplyFormatPatch(patchDir, files, targetDir))

	// Both files should exist
	assert.FileExists(t, filepath.Join(targetDir, "a.txt"))
	assert.FileExists(t, filepath.Join(targetDir, "b.txt"))

	// Verify commits were created (initial + 2 applied = 3 total)
	cmd := newGitCmd(targetDir, "rev-list", "--count", "HEAD")
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "3", strings.TrimSpace(string(out)))
}

func TestApplyFormatPatch_Conflict(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-am-conflict", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"modify file", "file.txt", "agent version of file\n"},
	})

	patchDir, files, err := GenerateFormatPatch("test-am-conflict", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	// Create target with conflicting content
	targetDir := filepath.Join(tmpDir, "target-am-conflict")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "completely different content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	err = ApplyFormatPatch(patchDir, files, targetDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "git am failed")
	assert.Contains(t, err.Error(), "--abort")
}

func TestApplyFormatPatch_PreservesAuthorship(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-am-author", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"my commit message", "new.txt", "new content\n"},
	})

	patchDir, files, err := GenerateFormatPatch("test-am-author", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	// Create target
	targetDir := filepath.Join(tmpDir, "target-am-author")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	require.NoError(t, ApplyFormatPatch(patchDir, files, targetDir))

	// Verify the commit message was preserved
	cmd := newGitCmd(targetDir, "log", "-1", "--format=%s")
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "my commit message", strings.TrimSpace(string(out)))
}

// End-to-end flow tests

func TestApplyFlow_CommitsOnly(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-flow-commits", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add feature A", "a.txt", "feature A\n"},
		{"add feature B", "b.txt", "feature B\n"},
	})

	// Verify state: commits but no WIP
	commits, err := ListCommitsBeyondBaseline("test-flow-commits")
	require.NoError(t, err)
	assert.Len(t, commits, 2)

	hasWIP, err := HasUncommittedChanges("test-flow-commits")
	require.NoError(t, err)
	assert.False(t, hasWIP)

	// Generate and apply
	patchDir, files, err := GenerateFormatPatch("test-flow-commits", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	targetDir := filepath.Join(tmpDir, "target-flow-commits")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	require.NoError(t, ApplyFormatPatch(patchDir, files, targetDir))

	// Verify both files exist with correct content
	contentA, _ := os.ReadFile(filepath.Join(targetDir, "a.txt")) //nolint:gosec
	contentB, _ := os.ReadFile(filepath.Join(targetDir, "b.txt")) //nolint:gosec
	assert.Equal(t, "feature A\n", string(contentA))
	assert.Equal(t, "feature B\n", string(contentB))

	// Verify 3 commits (initial + 2 applied)
	cmd := newGitCmd(targetDir, "rev-list", "--count", "HEAD")
	out, _ := cmd.Output()
	assert.Equal(t, "3", strings.TrimSpace(string(out)))
}

func TestApplyFlow_CommitsAndWIP(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandboxWithCommits(t, tmpDir, "test-flow-both", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"committed feature", "committed.txt", "committed\n"},
	})

	// Add WIP on top
	writeTestFile(t, workDir, "wip.txt", "wip content\n")

	// Verify state
	commits, err := ListCommitsBeyondBaseline("test-flow-both")
	require.NoError(t, err)
	assert.Len(t, commits, 1)

	hasWIP, err := HasUncommittedChanges("test-flow-both")
	require.NoError(t, err)
	assert.True(t, hasWIP)

	// Apply commits first
	patchDir, files, err := GenerateFormatPatch("test-flow-both", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	targetDir := filepath.Join(tmpDir, "target-flow-both")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	require.NoError(t, ApplyFormatPatch(patchDir, files, targetDir))

	// Then apply WIP
	wipPatch, _, err := GenerateWIPDiff("test-flow-both", nil)
	require.NoError(t, err)
	require.NotEmpty(t, wipPatch)

	require.NoError(t, ApplyPatch(wipPatch, targetDir, true))

	// Verify committed file exists
	assert.FileExists(t, filepath.Join(targetDir, "committed.txt"))
	// Verify WIP file exists
	assert.FileExists(t, filepath.Join(targetDir, "wip.txt"))
	wipContent, _ := os.ReadFile(filepath.Join(targetDir, "wip.txt")) //nolint:gosec
	assert.Equal(t, "wip content\n", string(wipContent))
}

func TestApplyFlow_WIPOnly(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-flow-wip", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "wip changes\n")

	// Verify state: no commits, only WIP
	commits, err := ListCommitsBeyondBaseline("test-flow-wip")
	require.NoError(t, err)
	assert.Empty(t, commits)

	hasWIP, err := HasUncommittedChanges("test-flow-wip")
	require.NoError(t, err)
	assert.True(t, hasWIP)

	// Falls back to squash path — use GeneratePatch
	patch, stat, err := GeneratePatch("test-flow-wip", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "file.txt")

	// Apply to target
	targetDir := filepath.Join(tmpDir, "target-flow-wip")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	require.NoError(t, ApplyPatch(patch, targetDir, true))

	content, _ := os.ReadFile(filepath.Join(targetDir, "file.txt")) //nolint:gosec
	assert.Equal(t, "wip changes\n", string(content))
}

func TestApplyFlow_NonGitFallback(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-flow-nongit", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add feature", "feature.txt", "feature\n"},
	})

	// Non-git target → must fall back to squash
	// The CLI does this, but we test the underlying primitives:
	// GeneratePatch works for squash fallback
	patch, stat, err := GeneratePatch("test-flow-nongit", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "feature.txt")

	// Apply to non-git target
	targetDir := filepath.Join(tmpDir, "target-flow-nongit")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	writeTestFile(t, targetDir, "file.txt", "original content\n")

	require.NoError(t, ApplyPatch(patch, targetDir, false))

	// Verify the feature file was created
	assert.FileExists(t, filepath.Join(targetDir, "feature.txt"))
	content, _ := os.ReadFile(filepath.Join(targetDir, "feature.txt")) //nolint:gosec
	assert.Equal(t, "feature\n", string(content))
}
