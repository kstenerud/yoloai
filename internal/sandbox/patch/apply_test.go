// ABOUTME: Unit tests for patch generation, baseline advancement, format-patch, selective apply,
// ABOUTME: uncommitted diff, and commit ref resolution in sandbox/patch.

package patch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// hostGitRuntime returns the runtime to pass to :copy/:rw patch operations in
// tests: none. For those modes git runs on the host filesystem, and the git
// package's sandbox scope only dispatches to the runtime when it implements
// runtime.GitExecer (Tart only) — a nil Runtime falls through to host git. So
// these tests need no backend and no daemon; passing nil keeps them in the unit suite.
func hostGitRuntime() runtime.Runtime {
	return nil
}

// GeneratePatch tests

func TestGeneratePatch_CopyMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-patch", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified content\n")

	rt := hostGitRuntime()
	patch, stat, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-patch", "", nil, true)
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

	rt := hostGitRuntime()
	_, _, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-rw-patch", "", nil, true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), ":rw directories")
}

func TestGeneratePatch_PathFilter(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-patch-filter", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "changed\n")
	writeTestFile(t, workDir, "other.txt", "also changed\n")

	rt := hostGitRuntime()
	patch, stat, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-patch-filter", "", []string{"file.txt"}, true)
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

	rt := hostGitRuntime()
	patch, stat, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-patch-empty", "", nil, true)
	require.NoError(t, err)
	assert.Empty(t, patch)
	assert.Empty(t, stat)
}

// TestGeneratePatch_IncludeUncommittedFalse_ExcludesUncommitted verifies the new
// commits-only default: a sandbox with one committed change + an uncommitted
// edit produces a patch that contains the commit only.
func TestGeneratePatch_IncludeUncommittedFalse_ExcludesUncommitted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandboxWithCommits(t, tmpDir, "test-wip-excluded", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add committed.txt", "committed.txt", "committed body\n"},
	})
	// Uncommitted edit AFTER the commit.
	writeTestFile(t, workDir, "wip.txt", "wip body\n")

	rt := hostGitRuntime()
	patch, _, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-wip-excluded", "", nil, false)
	require.NoError(t, err)
	assert.Contains(t, string(patch), "committed.txt", "committed change must be in patch")
	assert.NotContains(t, string(patch), "wip.txt", "uncommitted file must NOT be in patch when includeUncommitted=false")
}

// TestGeneratePatch_IncludeUncommittedTrue_IncludesUncommitted is the mirror: with
// the flag on, the same sandbox produces a patch containing both files.
func TestGeneratePatch_IncludeUncommittedTrue_IncludesUncommitted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandboxWithCommits(t, tmpDir, "test-wip-included", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add committed.txt", "committed.txt", "committed body\n"},
	})
	writeTestFile(t, workDir, "wip.txt", "wip body\n")

	rt := hostGitRuntime()
	patch, _, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-wip-included", "", nil, true)
	require.NoError(t, err)
	assert.Contains(t, string(patch), "committed.txt")
	assert.Contains(t, string(patch), "wip.txt", "uncommitted file must be in patch when includeUncommitted=true")
}

// ApplyPatch tests

func TestApplyPatch_GitTarget(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-apply-git", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified by agent\n")

	rt := hostGitRuntime()
	patch, _, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-apply-git", "", nil, true)
	require.NoError(t, err)

	// Create target git repo with original content
	targetDir := filepath.Join(tmpDir, "target-git")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	require.NoError(t, git.NewHostWithEnv(testEnv()).ApplyPatch(context.Background(), patch, targetDir, true))

	content, err := os.ReadFile(filepath.Join(targetDir, "file.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "modified by agent\n", string(content))
}

func TestApplyPatch_NonGitTarget(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-apply-nongit", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified by agent\n")

	rt := hostGitRuntime()
	patch, _, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-apply-nongit", "", nil, true)
	require.NoError(t, err)

	// Create target dir (no git) with original content
	targetDir := filepath.Join(tmpDir, "target-plain")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	writeTestFile(t, targetDir, "file.txt", "original content\n")

	require.NoError(t, git.NewHostWithEnv(testEnv()).ApplyPatch(context.Background(), patch, targetDir, false))

	content, err := os.ReadFile(filepath.Join(targetDir, "file.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "modified by agent\n", string(content))
}

func TestApplyPatch_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-apply-new", "/tmp/project")
	writeTestFile(t, workDir, "created.txt", "brand new file\n")

	rt := hostGitRuntime()
	patch, _, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-apply-new", "", nil, true)
	require.NoError(t, err)

	// Target has original file but not the new one
	targetDir := filepath.Join(tmpDir, "target-new")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	require.NoError(t, git.NewHostWithEnv(testEnv()).ApplyPatch(context.Background(), patch, targetDir, true))

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
	workDir := filepath.Join(sandboxDir, "work", store.EncodePath(hostPath))
	require.NoError(t, os.MkdirAll(workDir, 0750))

	initGitRepo(t, workDir)
	writeTestFile(t, workDir, "keep.txt", "keep this\n")
	writeTestFile(t, workDir, "remove.txt", "delete me\n")
	gitAdd(t, workDir, ".")
	gitCommit(t, workDir, "yoloai baseline")
	sha := gitHEAD(t, workDir)

	meta := &store.Environment{
		Name:      name,
		AgentType: "test",
		Dirs: []store.DirEnvironment{{
			HostPath:    hostPath,
			MountPath:   hostPath,
			Mode:        "copy",
			BaselineSHA: sha,
		}},
	}
	require.NoError(t, store.SaveEnvironment(sandboxDir, meta))

	// Delete the file in work copy
	require.NoError(t, os.Remove(filepath.Join(workDir, "remove.txt")))

	rt := hostGitRuntime()
	patch, _, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, name, "", nil, true)
	require.NoError(t, err)

	// Target has both files
	targetDir := filepath.Join(tmpDir, "target-del")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "keep.txt", "keep this\n")
	writeTestFile(t, targetDir, "remove.txt", "delete me\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	require.NoError(t, git.NewHostWithEnv(testEnv()).ApplyPatch(context.Background(), patch, targetDir, true))

	assert.FileExists(t, filepath.Join(targetDir, "keep.txt"))
	assert.NoFileExists(t, filepath.Join(targetDir, "remove.txt"))
}

// CheckPatch tests

func TestCheckPatch_Conflict(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-conflict", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "agent version\n")

	rt := hostGitRuntime()
	patch, _, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-conflict", "", nil, true)
	require.NoError(t, err)

	// Target has different content than what patch expects
	targetDir := filepath.Join(tmpDir, "target-conflict")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "completely different content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	err = git.NewHostWithEnv(testEnv()).CheckPatch(context.Background(), patch, targetDir, true)
	assert.Error(t, err)
}

func TestCheckPatch_Clean(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-clean", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified\n")

	rt := hostGitRuntime()
	patch, _, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-clean", "", nil, true)
	require.NoError(t, err)

	// Target matches original
	targetDir := filepath.Join(tmpDir, "target-clean")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	err = git.NewHostWithEnv(testEnv()).CheckPatch(context.Background(), patch, targetDir, true)
	assert.NoError(t, err)
}

// IsGitRepo and formatApplyError tests moved to workspace/

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

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-list-none", "")
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

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-list-one", "")
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

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-list-multi", "")
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

	rt := hostGitRuntime()
	_, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-list-rw", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), ":rw directories")
}

// HasUncommittedChanges tests

func TestHasUncommittedChanges_Clean(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandbox(t, tmpDir, "test-wip-clean", "/tmp/project")

	rt := hostGitRuntime()
	has, err := HasUncommittedChanges(context.Background(), testLayout(tmpDir), rt, "test-wip-clean", "")
	require.NoError(t, err)
	assert.False(t, has)
}

func TestHasUncommittedChanges_Modified(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-wip-mod", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "modified\n")

	rt := hostGitRuntime()
	has, err := HasUncommittedChanges(context.Background(), testLayout(tmpDir), rt, "test-wip-mod", "")
	require.NoError(t, err)
	assert.True(t, has)
}

func TestHasUncommittedChanges_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-wip-new", "/tmp/project")
	writeTestFile(t, workDir, "brand-new.txt", "new file\n")

	rt := hostGitRuntime()
	has, err := HasUncommittedChanges(context.Background(), testLayout(tmpDir), rt, "test-wip-new", "")
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

	rt := hostGitRuntime()
	has, err := HasUncommittedChanges(context.Background(), testLayout(tmpDir), rt, "test-wip-committed", "")
	require.NoError(t, err)
	assert.False(t, has)
}

// gitExecRuntime is a GitExecer fake (the Tart/containerd seam) that lets the
// HasUncommittedChanges exit-code discrimination be tested deterministically.
// `git add -A` always succeeds; `git diff --quiet` returns the configured error.
// The host-git tests above only ever exercise the *exec.ExitError branch — this
// fake reaches the *runtime.ExecError branch that non-host backends actually hit.
type gitExecRuntime struct {
	runtime.Runtime // embedded: only GitExec is invoked here, rest stay nil
	diffErr         error
}

func (g *gitExecRuntime) GitExec(_ context.Context, _, _ string, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "diff" {
		return "", g.diffErr
	}
	return "", nil // git add -A
}

func (g *gitExecRuntime) Descriptor() runtime.BackendDescriptor {
	return runtime.BackendDescriptor{
		Type:         "gitexec",
		Capabilities: runtime.BackendCaps{FilesystemLocality: runtime.LocalitySandboxSide},
	}
}

// TestHasUncommittedChanges_ExecErrorExit1 pins the backend (Tart/containerd)
// path: a *runtime.ExecError with code 1 from `git diff --quiet` means "dirty",
// not a failure. No host-git test can reach this branch.
func TestHasUncommittedChanges_ExecErrorExit1(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	createCopySandbox(t, tmpDir, "test-wip-execerr1", "/tmp/project")

	rt := &gitExecRuntime{diffErr: &runtime.ExecError{ExitCode: 1}}
	has, err := HasUncommittedChanges(context.Background(), testLayout(tmpDir), rt, "test-wip-execerr1", "")
	require.NoError(t, err)
	assert.True(t, has, "exit code 1 from git diff --quiet means uncommitted changes exist")
}

// TestHasUncommittedChanges_ExecErrorOther guards the discrimination: a non-1
// exit (a genuine git failure) must surface as an error, never be misreported
// as clean or dirty.
func TestHasUncommittedChanges_ExecErrorOther(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	createCopySandbox(t, tmpDir, "test-wip-execerr128", "/tmp/project")

	rt := &gitExecRuntime{diffErr: &runtime.ExecError{ExitCode: 128, Stderr: "fatal: not a git repository"}}
	_, err := HasUncommittedChanges(context.Background(), testLayout(tmpDir), rt, "test-wip-execerr128", "")
	require.Error(t, err, "a non-1 git failure must surface, not be swallowed as clean/dirty")
	assert.Contains(t, err.Error(), "git diff --quiet")
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

	rt := hostGitRuntime()
	patchDir, files, err := GenerateFormatPatch(context.Background(), testLayout(tmpDir), rt, "test-fp-one", "", nil)
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

	rt := hostGitRuntime()
	patchDir, files, err := GenerateFormatPatch(context.Background(), testLayout(tmpDir), rt, "test-fp-multi", "", nil)
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

	rt := hostGitRuntime()
	patchDir, files, err := GenerateFormatPatch(context.Background(), testLayout(tmpDir), rt, "test-fp-empty", "", nil)
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

	rt := hostGitRuntime()
	patchDir, files, err := GenerateFormatPatch(context.Background(), testLayout(tmpDir), rt, "test-fp-filter", "", []string{"a.txt"})
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	require.Len(t, files, 1)
	data, _ := os.ReadFile(filepath.Join(patchDir, files[0])) //nolint:gosec
	assert.Contains(t, string(data), "change a")
}

// GenerateUncommittedDiff tests

func TestGenerateUncommittedDiff_NoChanges(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandbox(t, tmpDir, "test-wip-diff-none", "/tmp/project")

	rt := hostGitRuntime()
	patch, stat, err := GenerateUncommittedDiff(context.Background(), testLayout(tmpDir), rt, "test-wip-diff-none", "", nil)
	require.NoError(t, err)
	assert.Empty(t, patch)
	assert.Empty(t, stat)
}

func TestGenerateUncommittedDiff_Modified(t *testing.T) {
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

	rt := hostGitRuntime()
	patch, stat, err := GenerateUncommittedDiff(context.Background(), testLayout(tmpDir), rt, "test-wip-diff-mod", "", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "wip.txt")
	assert.Contains(t, string(patch), "work in progress")
	// Should NOT include the committed change
	assert.NotContains(t, string(patch), "committed.txt")
}

func TestGenerateUncommittedDiff_PathFilter(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-wip-diff-filter", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "changed\n")
	writeTestFile(t, workDir, "other.txt", "also changed\n")

	rt := hostGitRuntime()
	patch, stat, err := GenerateUncommittedDiff(context.Background(), testLayout(tmpDir), rt, "test-wip-diff-filter", "", []string{"file.txt"})
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

	rt := hostGitRuntime()
	patchDir, files, err := GenerateFormatPatch(context.Background(), testLayout(tmpDir), rt, "test-am-one", "", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	// Create target git repo with matching baseline
	targetDir := filepath.Join(tmpDir, "target-am-one")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	_, err = git.NewHostWithEnv(testEnv()).ApplyFormatPatch(context.Background(), patchDir, files, targetDir)
	require.NoError(t, err)

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

	rt := hostGitRuntime()
	patchDir, files, err := GenerateFormatPatch(context.Background(), testLayout(tmpDir), rt, "test-am-multi", "", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	// Create target
	targetDir := filepath.Join(tmpDir, "target-am-multi")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	_, err = git.NewHostWithEnv(testEnv()).ApplyFormatPatch(context.Background(), patchDir, files, targetDir)
	require.NoError(t, err)

	// Both files should exist
	assert.FileExists(t, filepath.Join(targetDir, "a.txt"))
	assert.FileExists(t, filepath.Join(targetDir, "b.txt"))

	// Verify commits were created (initial + 2 applied = 3 total)
	out, err := git.NewHostWithEnv(testEnv()).Run(context.Background(), targetDir, "rev-list", "--count", "HEAD")
	require.NoError(t, err)
	assert.Equal(t, "3", strings.TrimSpace(out))
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

	rt := hostGitRuntime()
	patchDir, files, err := GenerateFormatPatch(context.Background(), testLayout(tmpDir), rt, "test-am-conflict", "", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	// Create target with conflicting content
	targetDir := filepath.Join(tmpDir, "target-am-conflict")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "completely different content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	_, err = git.NewHostWithEnv(testEnv()).ApplyFormatPatch(context.Background(), patchDir, files, targetDir)
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

	rt := hostGitRuntime()
	patchDir, files, err := GenerateFormatPatch(context.Background(), testLayout(tmpDir), rt, "test-am-author", "", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	// Create target
	targetDir := filepath.Join(tmpDir, "target-am-author")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	_, err = git.NewHostWithEnv(testEnv()).ApplyFormatPatch(context.Background(), patchDir, files, targetDir)
	require.NoError(t, err)

	// Verify the commit message was preserved
	out, err := git.NewHostWithEnv(testEnv()).Run(context.Background(), targetDir, "log", "-1", "--format=%s")
	require.NoError(t, err)
	assert.Equal(t, "my commit message", strings.TrimSpace(out))
}

// AdvanceBaseline tests

func TestAdvanceBaseline_UpdatesMeta(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "test-advance-meta"
	createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add feature", "feature.txt", "feature\n"},
	})

	// Before: commits visible
	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name, "")
	require.NoError(t, err)
	require.Len(t, commits, 1)

	// Advance baseline
	require.NoError(t, AdvanceBaseline(context.Background(), testLayout(tmpDir), rt, name, ""))

	// After: no commits beyond baseline
	commits, err = ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name, "")
	require.NoError(t, err)
	assert.Empty(t, commits)

	// Verify environment.json has new SHA
	meta, err := store.LoadEnvironment(testLayout(tmpDir).SandboxDir(name))
	require.NoError(t, err)
	workDir := store.WorkDir(testLayout(tmpDir).SandboxDir(name), meta.Workdir().HostPath)
	headSHA := gitHEAD(t, workDir)
	assert.Equal(t, headSHA, meta.Workdir().BaselineSHA)
}

func TestAdvanceBaseline_DiffEmptyAfterAdvance(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "test-advance-diff"
	createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"change file", "file.txt", "changed\n"},
	})

	// Before: diff is non-empty
	rt := hostGitRuntime()
	patch, _, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, name, "", nil, true)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)

	// Advance baseline
	require.NoError(t, AdvanceBaseline(context.Background(), testLayout(tmpDir), rt, name, ""))

	// After: diff is empty
	patch, stat, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, name, "", nil, true)
	require.NoError(t, err)
	assert.Empty(t, patch)
	assert.Empty(t, stat)
}

func TestAdvanceBaseline_RWNoop(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	hostDir := filepath.Join(tmpDir, "rw-dir")
	require.NoError(t, os.MkdirAll(hostDir, 0750))
	createRWSandbox(t, tmpDir, "test-advance-rw", hostDir)

	// Should return nil (no-op)
	rt := hostGitRuntime()
	assert.NoError(t, AdvanceBaseline(context.Background(), testLayout(tmpDir), rt, "test-advance-rw", ""))
}

func TestAdvanceBaseline_NewCommitsStillVisible(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "test-advance-new"
	workDir := createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"old commit", "old.txt", "old\n"},
	})

	// Advance baseline past old commit
	rt := hostGitRuntime()
	require.NoError(t, AdvanceBaseline(context.Background(), testLayout(tmpDir), rt, name, ""))

	// Add a new commit after advancing
	writeTestFile(t, workDir, "new.txt", "new\n")
	gitAdd(t, workDir, ".")
	gitCommit(t, workDir, "new commit")

	// Only new commit should be visible
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name, "")
	require.NoError(t, err)
	require.Len(t, commits, 1)
	assert.Equal(t, "new commit", commits[0].Subject)
}

// ResolveRef tests

func TestResolveRef_FullSHA(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-resolve-full", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add feature", "feature.txt", "feature\n"},
	})

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-resolve-full", "")
	require.NoError(t, err)
	require.Len(t, commits, 1)

	resolved, err := ResolveRef(context.Background(), testLayout(tmpDir), rt, "test-resolve-full", "", commits[0].SHA)
	require.NoError(t, err)
	assert.Equal(t, commits[0].SHA, resolved.SHA)
	assert.Equal(t, "add feature", resolved.Subject)
}

func TestResolveRef_ShortSHA(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-resolve-short", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add feature", "feature.txt", "feature\n"},
	})

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-resolve-short", "")
	require.NoError(t, err)
	require.Len(t, commits, 1)

	// Use first 7 chars
	resolved, err := ResolveRef(context.Background(), testLayout(tmpDir), rt, "test-resolve-short", "", commits[0].SHA[:7])
	require.NoError(t, err)
	assert.Equal(t, commits[0].SHA, resolved.SHA)
}

func TestResolveRef_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-resolve-nf", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add feature", "feature.txt", "feature\n"},
	})

	rt := hostGitRuntime()
	_, err := ResolveRef(context.Background(), testLayout(tmpDir), rt, "test-resolve-nf", "", "deadbeef")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ResolveRefs tests

func TestResolveRefs_SingleRef(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-resolverefs-single", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"first", "a.txt", "a\n"},
		{"second", "b.txt", "b\n"},
	})

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-resolverefs-single", "")
	require.NoError(t, err)
	require.Len(t, commits, 2)

	resolved, err := ResolveRefs(context.Background(), testLayout(tmpDir), rt, "test-resolverefs-single", "", []string{commits[1].SHA[:7]})
	require.NoError(t, err)
	require.Len(t, resolved, 1)
	assert.Equal(t, commits[1].SHA, resolved[0].SHA)
}

func TestResolveRefs_Range(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-resolverefs-range", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"first", "a.txt", "a\n"},
		{"second", "b.txt", "b\n"},
		{"third", "c.txt", "c\n"},
	})

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-resolverefs-range", "")
	require.NoError(t, err)
	require.Len(t, commits, 3)

	// Range first..third = second and third (exclusive start, inclusive end)
	rangeRef := commits[0].SHA[:7] + ".." + commits[2].SHA[:7]
	resolved, err := ResolveRefs(context.Background(), testLayout(tmpDir), rt, "test-resolverefs-range", "", []string{rangeRef})
	require.NoError(t, err)
	require.Len(t, resolved, 2)
	assert.Equal(t, "second", resolved[0].Subject)
	assert.Equal(t, "third", resolved[1].Subject)
}

func TestResolveRefs_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-resolverefs-nf", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"first", "a.txt", "a\n"},
	})

	rt := hostGitRuntime()
	_, err := ResolveRefs(context.Background(), testLayout(tmpDir), rt, "test-resolverefs-nf", "", []string{"deadbeef"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ContiguousPrefixEnd tests

func TestContiguousPrefixEnd_AllApplied(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "A"},
		{SHA: "bbb", Subject: "B"},
		{SHA: "ccc", Subject: "C"},
	}
	applied := map[string]bool{"aaa": true, "bbb": true, "ccc": true}
	assert.Equal(t, 2, git.ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_PartialPrefix(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "A"},
		{SHA: "bbb", Subject: "B"},
		{SHA: "ccc", Subject: "C"},
	}
	// Applied A and B but not C
	applied := map[string]bool{"aaa": true, "bbb": true}
	assert.Equal(t, 1, git.ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_Gap(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "A"},
		{SHA: "bbb", Subject: "B"},
		{SHA: "ccc", Subject: "C"},
	}
	// Applied A and C (gap at B) — prefix is just A
	applied := map[string]bool{"aaa": true, "ccc": true}
	assert.Equal(t, 0, git.ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_NoPrefix(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "A"},
		{SHA: "bbb", Subject: "B"},
		{SHA: "ccc", Subject: "C"},
	}
	// Applied C and D but not A — no contiguous prefix from start
	applied := map[string]bool{"ccc": true}
	assert.Equal(t, -1, git.ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_Empty(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "A"},
	}
	applied := map[string]bool{}
	assert.Equal(t, -1, git.ContiguousPrefixEnd(commits, applied))
}

// GenerateFormatPatchForRefs tests

func TestGenerateFormatPatchForRefs_Single(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-fpref-single", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"first", "a.txt", "a\n"},
		{"second", "b.txt", "b\n"},
	})

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-fpref-single", "")
	require.NoError(t, err)
	require.Len(t, commits, 2)

	// Only generate patch for the second commit
	patchDir, files, err := GenerateFormatPatchForRefs(context.Background(), testLayout(tmpDir), rt, "test-fpref-single", "", []string{commits[1].SHA}, nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	require.Len(t, files, 1)
	data, _ := os.ReadFile(filepath.Join(patchDir, files[0])) //nolint:gosec
	assert.Contains(t, string(data), "second")
}

func TestGenerateFormatPatchForRefs_Multiple(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	createCopySandboxWithCommits(t, tmpDir, "test-fpref-multi", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"first", "a.txt", "a\n"},
		{"second", "b.txt", "b\n"},
		{"third", "c.txt", "c\n"},
	})

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-fpref-multi", "")
	require.NoError(t, err)
	require.Len(t, commits, 3)

	// Only generate patches for first and third
	patchDir, files, err := GenerateFormatPatchForRefs(context.Background(), testLayout(tmpDir), rt, "test-fpref-multi", "", []string{commits[0].SHA, commits[2].SHA}, nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	require.Len(t, files, 2)
}

// AdvanceBaselineTo tests

func TestAdvanceBaselineTo_UpdatesMeta(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "test-advto-meta"
	createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"first", "a.txt", "a\n"},
		{"second", "b.txt", "b\n"},
		{"third", "c.txt", "c\n"},
	})

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name, "")
	require.NoError(t, err)
	require.Len(t, commits, 3)

	// Advance to second commit (not HEAD)
	require.NoError(t, AdvanceBaselineTo(testLayout(tmpDir), name, "", commits[1].SHA))

	// Verify meta has the second commit's SHA
	meta, err := store.LoadEnvironment(testLayout(tmpDir).SandboxDir(name))
	require.NoError(t, err)
	assert.Equal(t, commits[1].SHA, meta.Workdir().BaselineSHA)

	// Only third commit should remain visible
	remaining, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name, "")
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	assert.Equal(t, "third", remaining[0].Subject)
}

// Selective apply end-to-end test

func TestSelectiveApplyFlow(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "test-selective-e2e"
	createCopySandboxWithCommits(t, tmpDir, name, "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"add A", "a.txt", "a content\n"},
		{"add B", "b.txt", "b content\n"},
		{"add C", "c.txt", "c content\n"},
	})

	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name, "")
	require.NoError(t, err)
	require.Len(t, commits, 3)

	// Resolve first two commits
	resolved, err := ResolveRefs(context.Background(), testLayout(tmpDir), rt, name, "", []string{commits[0].SHA[:7], commits[1].SHA[:7]})
	require.NoError(t, err)
	require.Len(t, resolved, 2)

	// Generate patches for selected commits
	shas := []string{resolved[0].SHA, resolved[1].SHA}
	patchDir, files, err := GenerateFormatPatchForRefs(context.Background(), testLayout(tmpDir), rt, name, "", shas, nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	// Create target
	targetDir := filepath.Join(tmpDir, "target-selective")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	// Apply
	_, err = git.NewHostWithEnv(testEnv()).ApplyFormatPatch(context.Background(), patchDir, files, targetDir)
	require.NoError(t, err)

	// Verify only A and B exist, not C
	assert.FileExists(t, filepath.Join(targetDir, "a.txt"))
	assert.FileExists(t, filepath.Join(targetDir, "b.txt"))
	assert.NoFileExists(t, filepath.Join(targetDir, "c.txt"))

	// Verify contiguous prefix advancement
	appliedSet := map[string]bool{
		commits[0].SHA: true,
		commits[1].SHA: true,
	}
	prefixEnd := git.ContiguousPrefixEnd(commits, appliedSet)
	assert.Equal(t, 1, prefixEnd) // advances to index 1 (commit B)

	// Advance baseline
	require.NoError(t, AdvanceBaselineTo(testLayout(tmpDir), name, "", commits[prefixEnd].SHA))

	// Only C should remain
	remaining, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name, "")
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	assert.Equal(t, "add C", remaining[0].Subject)
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

	// Verify state: commits but no uncommitted changes
	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-flow-commits", "")
	require.NoError(t, err)
	assert.Len(t, commits, 2)

	hasUncommitted, err := HasUncommittedChanges(context.Background(), testLayout(tmpDir), rt, "test-flow-commits", "")
	require.NoError(t, err)
	assert.False(t, hasUncommitted)

	// Generate and apply
	patchDir, files, err := GenerateFormatPatch(context.Background(), testLayout(tmpDir), rt, "test-flow-commits", "", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	targetDir := filepath.Join(tmpDir, "target-flow-commits")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	_, err = git.NewHostWithEnv(testEnv()).ApplyFormatPatch(context.Background(), patchDir, files, targetDir)
	require.NoError(t, err)

	// Verify both files exist with correct content
	contentA, _ := os.ReadFile(filepath.Join(targetDir, "a.txt")) //nolint:gosec
	contentB, _ := os.ReadFile(filepath.Join(targetDir, "b.txt")) //nolint:gosec
	assert.Equal(t, "feature A\n", string(contentA))
	assert.Equal(t, "feature B\n", string(contentB))

	// Verify 3 commits (initial + 2 applied)
	out, _ := git.NewHostWithEnv(testEnv()).Run(context.Background(), targetDir, "rev-list", "--count", "HEAD")
	assert.Equal(t, "3", strings.TrimSpace(out))
}

func TestApplyFlow_CommitsAndUncommitted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandboxWithCommits(t, tmpDir, "test-flow-both", "/tmp/project", []struct {
		subject  string
		filename string
		content  string
	}{
		{"committed feature", "committed.txt", "committed\n"},
	})

	// Add uncommitted changes on top
	writeTestFile(t, workDir, "wip.txt", "wip content\n")

	// Verify state
	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-flow-both", "")
	require.NoError(t, err)
	assert.Len(t, commits, 1)

	hasUncommitted, err := HasUncommittedChanges(context.Background(), testLayout(tmpDir), rt, "test-flow-both", "")
	require.NoError(t, err)
	assert.True(t, hasUncommitted)

	// Apply commits first
	patchDir, files, err := GenerateFormatPatch(context.Background(), testLayout(tmpDir), rt, "test-flow-both", "", nil)
	require.NoError(t, err)
	defer os.RemoveAll(patchDir) //nolint:errcheck

	targetDir := filepath.Join(tmpDir, "target-flow-both")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "file.txt", "original content\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	_, err = git.NewHostWithEnv(testEnv()).ApplyFormatPatch(context.Background(), patchDir, files, targetDir)
	require.NoError(t, err)

	// Then apply uncommitted changes
	uncommittedPatch, _, err := GenerateUncommittedDiff(context.Background(), testLayout(tmpDir), rt, "test-flow-both", "", nil)
	require.NoError(t, err)
	require.NotEmpty(t, uncommittedPatch)

	require.NoError(t, git.NewHostWithEnv(testEnv()).ApplyPatch(context.Background(), uncommittedPatch, targetDir, true))

	// Verify committed file exists
	assert.FileExists(t, filepath.Join(targetDir, "committed.txt"))
	// Verify uncommitted file exists
	assert.FileExists(t, filepath.Join(targetDir, "wip.txt"))
	uncommittedContent, _ := os.ReadFile(filepath.Join(targetDir, "wip.txt")) //nolint:gosec
	assert.Equal(t, "wip content\n", string(uncommittedContent))
}

func TestApplyFlow_UncommittedOnly(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	workDir := createCopySandbox(t, tmpDir, "test-flow-wip", "/tmp/project")
	writeTestFile(t, workDir, "file.txt", "wip changes\n")

	// Verify state: no commits, only uncommitted changes
	rt := hostGitRuntime()
	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, "test-flow-wip", "")
	require.NoError(t, err)
	assert.Empty(t, commits)

	hasUncommitted, err := HasUncommittedChanges(context.Background(), testLayout(tmpDir), rt, "test-flow-wip", "")
	require.NoError(t, err)
	assert.True(t, hasUncommitted)

	// Falls back to squash path — use GeneratePatch
	patch, stat, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-flow-wip", "", nil, true)
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

	require.NoError(t, git.NewHostWithEnv(testEnv()).ApplyPatch(context.Background(), patch, targetDir, true))

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
	rt := hostGitRuntime()
	patch, stat, err := GeneratePatch(context.Background(), testLayout(tmpDir), rt, "test-flow-nongit", "", nil, true)
	require.NoError(t, err)
	assert.NotEmpty(t, patch)
	assert.Contains(t, stat, "feature.txt")

	// Apply to non-git target
	targetDir := filepath.Join(tmpDir, "target-flow-nongit")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	writeTestFile(t, targetDir, "file.txt", "original content\n")

	require.NoError(t, git.NewHostWithEnv(testEnv()).ApplyPatch(context.Background(), patch, targetDir, false))

	// Verify the feature file was created
	assert.FileExists(t, filepath.Join(targetDir, "feature.txt"))
	content, _ := os.ReadFile(filepath.Join(targetDir, "feature.txt")) //nolint:gosec
	assert.Equal(t, "feature\n", string(content))
}

// TestApplySeries_NonGitTargetRefuses verifies comply-or-complain (D26/D27): a
// series replay onto a non-git host target refuses with a typed *UsageError
// rather than silently degrading to a net-diff apply. The refusal precedes any
// runtime call, so rt is nil here.
func TestApplySeries_NonGitTargetRefuses(t *testing.T) {
	tmpDir := t.TempDir()
	name := "series-nongit"
	layout := testLayout(tmpDir)
	require.NoError(t, os.MkdirAll(layout.SandboxDir(name), 0750))

	hostPath := filepath.Join(tmpDir, "plain-project")
	require.NoError(t, os.MkdirAll(hostPath, 0750)) // exists but not a git repo

	meta := &store.Environment{
		Name:      name,
		AgentType: "test",
		Dirs: []store.DirEnvironment{{
			HostPath:    hostPath,
			MountPath:   hostPath,
			Mode:        "copy",
			BaselineSHA: "abc",
		}},
	}
	require.NoError(t, store.SaveEnvironment(layout.SandboxDir(name), meta))

	_, err := ApplySeries(context.Background(), layout, nil, name, ApplySeriesOptions{})
	require.Error(t, err)
	var ue *yoerrors.UsageError
	require.ErrorAs(t, err, &ue, "non-git target must yield a *UsageError")
}

// setupSeriesApplyFixture builds a real git host target plus a copy-mode sandbox
// whose HostPath points at it, seeded with three add-only commits (A, B, C). The
// commits only add files, so git am replays them cleanly onto any base.
func setupSeriesApplyFixture(t *testing.T, tmpDir, name string) (targetDir string) {
	t.Helper()
	targetDir = filepath.Join(tmpDir, "host-target")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	initGitRepo(t, targetDir)
	writeTestFile(t, targetDir, "seed.txt", "seed\n")
	gitAdd(t, targetDir, ".")
	gitCommit(t, targetDir, "initial")

	createCopySandboxWithCommits(t, tmpDir, name, targetDir, []struct {
		subject  string
		filename string
		content  string
	}{
		{"add A", "a.txt", "a\n"},
		{"add B", "b.txt", "b\n"},
		{"add C", "c.txt", "c\n"},
	})
	return targetDir
}

// TestApplySeries_FullReplay drives ApplySeries with no refs: all beyond-baseline
// commits replay onto the host as a series, each AppliedCommit carries a host
// SHA, and the baseline advances to HEAD (nothing remains beyond baseline).
func TestApplySeries_FullReplay(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "series-full"
	targetDir := setupSeriesApplyFixture(t, tmpDir, name)
	rt := hostGitRuntime()

	result, err := ApplySeries(context.Background(), testLayout(tmpDir), rt, name, ApplySeriesOptions{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Commits, 3)

	assert.FileExists(t, filepath.Join(targetDir, "a.txt"))
	assert.FileExists(t, filepath.Join(targetDir, "b.txt"))
	assert.FileExists(t, filepath.Join(targetDir, "c.txt"))
	for _, c := range result.Commits {
		assert.NotEmpty(t, c.SourceSHA, "commit %q should carry its source SHA", c.Subject)
		assert.NotEmpty(t, c.HostSHA, "commit %q should carry the rewritten host SHA", c.Subject)
	}

	remaining, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name, "")
	require.NoError(t, err)
	assert.Empty(t, remaining, "baseline should advance to HEAD after a full replay")
}

// TestApplySeries_SelectiveRefs drives ApplySeries with a Refs subset: only the
// named commits replay, and the baseline advances across the contiguous applied
// prefix so the unselected tail commit remains beyond baseline.
func TestApplySeries_SelectiveRefs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "series-selective"
	targetDir := setupSeriesApplyFixture(t, tmpDir, name)
	rt := hostGitRuntime()

	commits, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name, "")
	require.NoError(t, err)
	require.Len(t, commits, 3)

	// Apply the contiguous prefix A, B (commits[0], commits[1]); leave C.
	result, err := ApplySeries(context.Background(), testLayout(tmpDir), rt, name, ApplySeriesOptions{
		Refs: []string{commits[0].SHA[:7], commits[1].SHA[:7]},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Commits, 2)

	assert.FileExists(t, filepath.Join(targetDir, "a.txt"))
	assert.FileExists(t, filepath.Join(targetDir, "b.txt"))
	assert.NoFileExists(t, filepath.Join(targetDir, "c.txt"))

	remaining, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name, "")
	require.NoError(t, err)
	require.Len(t, remaining, 1, "baseline should advance across the applied prefix only")
	assert.Equal(t, "add C", remaining[0].Subject)
}

// TestApplySeries_DryRunDoesNotApply verifies DryRun reports the commits that
// would replay without touching the host target or advancing the baseline.
func TestApplySeries_DryRunDoesNotApply(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	name := "series-dryrun"
	targetDir := setupSeriesApplyFixture(t, tmpDir, name)
	rt := hostGitRuntime()

	result, err := ApplySeries(context.Background(), testLayout(tmpDir), rt, name, ApplySeriesOptions{DryRun: true})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Commits, 3)

	// Nothing applied to the host, and no host SHAs assigned (no git am ran).
	assert.NoFileExists(t, filepath.Join(targetDir, "a.txt"))
	for _, c := range result.Commits {
		assert.Empty(t, c.HostSHA, "dry run must not rewrite commits onto the host")
	}

	// Baseline unchanged: all three commits still beyond baseline.
	remaining, err := ListCommitsBeyondBaseline(context.Background(), testLayout(tmpDir), rt, name, "")
	require.NoError(t, err)
	assert.Len(t, remaining, 3)
}
