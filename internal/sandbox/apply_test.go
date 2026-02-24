package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
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
