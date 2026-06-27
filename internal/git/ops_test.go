package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ctx shorthand for background context in tests.
var ctx = context.Background()

// ─── formatApplyError ────────────────────────────────────────────────────────

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

func TestFormatApplyError_DoesNotExist(t *testing.T) {
	gitErr := fmt.Errorf("error: foo.txt: does not exist in working directory: exit status 1")
	err := formatApplyError(gitErr, "/tmp/project")
	assert.Contains(t, err.Error(), "no longer exists")
	assert.Contains(t, err.Error(), "foo.txt")
}

func TestFormatApplyError_AlreadyExists(t *testing.T) {
	gitErr := fmt.Errorf("error: bar.txt: already exists in working directory: exit status 1")
	err := formatApplyError(gitErr, "/tmp/project")
	assert.Contains(t, err.Error(), "already exists")
	assert.Contains(t, err.Error(), "bar.txt")
}

// ─── IsGitRepo ───────────────────────────────────────────────────────────────

func TestIsGitRepo_True(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	assert.True(t, IsGitRepo(dir))
}

func TestIsGitRepo_False(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, IsGitRepo(dir))
}

// ─── ContiguousPrefixEnd ─────────────────────────────────────────────────────

func TestContiguousPrefixEnd_AllApplied(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "first"},
		{SHA: "bbb", Subject: "second"},
		{SHA: "ccc", Subject: "third"},
	}
	applied := map[string]bool{"aaa": true, "bbb": true, "ccc": true}
	assert.Equal(t, 2, ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_PrefixOnly(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "first"},
		{SHA: "bbb", Subject: "second"},
		{SHA: "ccc", Subject: "third"},
	}
	applied := map[string]bool{"aaa": true, "bbb": true}
	assert.Equal(t, 1, ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_FirstNotApplied(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "first"},
		{SHA: "bbb", Subject: "second"},
	}
	applied := map[string]bool{"bbb": true}
	assert.Equal(t, -1, ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_NoneApplied(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "first"},
		{SHA: "bbb", Subject: "second"},
	}
	applied := map[string]bool{}
	assert.Equal(t, -1, ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_EmptyCommits(t *testing.T) {
	applied := map[string]bool{"aaa": true}
	assert.Equal(t, -1, ContiguousPrefixEnd(nil, applied))
}

func TestContiguousPrefixEnd_SingleApplied(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "only"},
	}
	applied := map[string]bool{"aaa": true}
	assert.Equal(t, 0, ContiguousPrefixEnd(commits, applied))
}

func TestContiguousPrefixEnd_GapInMiddle(t *testing.T) {
	commits := []CommitInfo{
		{SHA: "aaa", Subject: "first"},
		{SHA: "bbb", Subject: "second"},
		{SHA: "ccc", Subject: "third"},
	}
	applied := map[string]bool{"aaa": true, "ccc": true}
	assert.Equal(t, 0, ContiguousPrefixEnd(commits, applied))
}

// ─── formatAMError ───────────────────────────────────────────────────────────

func TestFormatAMError_ContainsGuidance(t *testing.T) {
	output := []byte("Applying: fix bug\nerror: patch failed")
	err := formatAMError(output, "/tmp/target")
	msg := err.Error()
	assert.Contains(t, msg, "cd /tmp/target")
	assert.Contains(t, msg, "--continue")
	assert.Contains(t, msg, "--skip")
	assert.Contains(t, msg, "--abort")
}

func TestFormatAMError_IncludesOutput(t *testing.T) {
	output := []byte("Applying: my commit\nConflict in file.txt")
	err := formatAMError(output, "/tmp/target")
	msg := err.Error()
	assert.Contains(t, msg, "Applying: my commit")
	assert.Contains(t, msg, "Conflict in file.txt")
}

func TestFormatAMError_EmptyOutput(t *testing.T) {
	err := formatAMError([]byte(""), "/tmp/target")
	msg := err.Error()
	assert.Contains(t, msg, "git am failed in /tmp/target")
	assert.Contains(t, msg, "--continue")
}

// ─── generatePatch helper ────────────────────────────────────────────────────

func generatePatch(t *testing.T, dir, filename, oldContent, newContent string) []byte {
	t.Helper()
	writeTestFile(t, dir, filename, oldContent)
	gitAdd(t, dir, filename)
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, filename, newContent)
	gitAdd(t, dir, filename)

	cmd := sysexec.Command(testutil.GitEnv(), "git", "-C", dir, "diff", "--cached")
	out, err := cmd.Output()
	require.NoError(t, err, "git diff --cached failed")
	require.NotEmpty(t, out, "patch should not be empty")

	// Reset the staged change so the repo is back at old content.
	runGit(t, dir, "reset", "HEAD", "--", filename)
	runGit(t, dir, "checkout", "--", filename)

	return out
}

// ─── CheckPatch ──────────────────────────────────────────────────────────────

func TestCheckPatch_CleanApply(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")

	err := NewHostWithEnv(testEnv()).CheckPatch(ctx, patch, dir, true)
	assert.NoError(t, err)
}

func TestCheckPatch_Conflict(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")

	// Change the file to something different so the patch conflicts.
	writeTestFile(t, dir, "file.txt", "completely different content\n")
	gitAdd(t, dir, "file.txt")
	gitCommit(t, dir, "diverge")

	err := NewHostWithEnv(testEnv()).CheckPatch(ctx, patch, dir, true)
	assert.Error(t, err)
}

func TestCheckPatch_NonGitDir(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	patch := generatePatch(t, repoDir, "file.txt", "old content\n", "new content\n")

	targetDir := t.TempDir()
	writeTestFile(t, targetDir, "file.txt", "old content\n")

	err := NewHostWithEnv(testEnv()).CheckPatch(ctx, patch, targetDir, false)
	assert.NoError(t, err)
}

// ─── ApplyPatch ──────────────────────────────────────────────────────────────

func TestApplyPatch_ApplyInGitRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")

	err := NewHostWithEnv(testEnv()).ApplyPatch(ctx, patch, dir, true)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "file.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "new content\n", string(content))
}

func TestApplyPatch_NonGitDir(t *testing.T) {
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	patch := generatePatch(t, repoDir, "file.txt", "old content\n", "new content\n")

	targetDir := t.TempDir()
	writeTestFile(t, targetDir, "file.txt", "old content\n")

	err := NewHostWithEnv(testEnv()).ApplyPatch(ctx, patch, targetDir, false)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(targetDir, "file.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "new content\n", string(content))
}

func TestApplyPatch_ConflictReturnsError(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")

	writeTestFile(t, dir, "file.txt", "completely different content\n")
	gitAdd(t, dir, "file.txt")
	gitCommit(t, dir, "diverge")

	err := NewHostWithEnv(testEnv()).ApplyPatch(ctx, patch, dir, true)
	assert.Error(t, err)
}

// ─── ApplyFormatPatch ────────────────────────────────────────────────────────

func TestApplyFormatPatch_EmptyFilesList(t *testing.T) {
	_, err := NewHostWithEnv(testEnv()).ApplyFormatPatch(ctx, "/nonexistent", nil, "/nonexistent")
	assert.NoError(t, err)
}

func TestApplyFormatPatch_EmptyTargetRepo(t *testing.T) {
	srcDir := t.TempDir()
	initGitRepo(t, srcDir)
	writeTestFile(t, srcDir, "hello.txt", "hello\n")
	gitAdd(t, srcDir, "hello.txt")
	gitCommit(t, srcDir, "add hello")

	patchDir := t.TempDir()
	runGit(t, srcDir, "format-patch", "--output-directory", patchDir, "--root", "HEAD")

	files, err := filepath.Glob(filepath.Join(patchDir, "*.patch"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	relFiles := make([]string, len(files))
	for i, f := range files {
		relFiles[i] = filepath.Base(f)
	}

	targetDir := t.TempDir()
	initGitRepo(t, targetDir)

	shaMap, err := NewHostWithEnv(testEnv()).ApplyFormatPatch(ctx, patchDir, relFiles, targetDir)
	require.NoError(t, err)
	assert.Len(t, shaMap, 1, "should have one SHA mapping")

	content, readErr := os.ReadFile(filepath.Join(targetDir, "hello.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, readErr)
	assert.Equal(t, "hello\n", string(content))
}

// ─── withTempGitDir ──────────────────────────────────────────────────────────

func TestWithTempGitDir_CallsFn(t *testing.T) {
	var calledWith string
	err := NewHostWithEnv(testEnv()).withTempGitDir(ctx, func(tmpDir string) error {
		calledWith = tmpDir
		assert.True(t, IsGitRepo(tmpDir))
		return nil
	})
	require.NoError(t, err)
	assert.NotEmpty(t, calledWith)
}

func TestWithTempGitDir_PropagatesError(t *testing.T) {
	sentinel := fmt.Errorf("test sentinel error")
	err := NewHostWithEnv(testEnv()).withTempGitDir(ctx, func(tmpDir string) error {
		return sentinel
	})
	assert.ErrorIs(t, err, sentinel)
}

func TestWithTempGitDir_CleansUp(t *testing.T) {
	var capturedDir string
	err := NewHostWithEnv(testEnv()).withTempGitDir(ctx, func(tmpDir string) error {
		capturedDir = tmpDir
		_, statErr := os.Stat(tmpDir)
		require.NoError(t, statErr)
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, capturedDir)

	_, err = os.Stat(capturedDir)
	assert.True(t, os.IsNotExist(err), "temp dir should be cleaned up after withTempGitDir returns")
}

// ─── runGitApply ─────────────────────────────────────────────────────────────

func TestRunGitApply_ValidPatch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	patch := generatePatch(t, dir, "file.txt", "old content\n", "new content\n")

	err := NewHostWithEnv(testEnv()).runGitApply(ctx, dir, patch)
	assert.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "file.txt")) //nolint:gosec // G304: test file path
	require.NoError(t, err)
	assert.Equal(t, "new content\n", string(content))
}

func TestRunGitApply_InvalidPatch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "dummy.txt", "dummy\n")
	gitAdd(t, dir, "dummy.txt")
	gitCommit(t, dir, "initial")

	err := NewHostWithEnv(testEnv()).runGitApply(ctx, dir, []byte("this is not a valid patch"))
	assert.Error(t, err)
}

// ─── CopyDiff ────────────────────────────────────────────────────────────────

func TestCopyDiff_NoChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)
	out, err := NewHostWithEnv(testEnv()).CopyDiff(ctx, dir, sha, nil, false, false, "")
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestCopyDiff_WithChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)
	writeTestFile(t, dir, "file.txt", "hello world\n")

	out, err := NewHostWithEnv(testEnv()).CopyDiff(ctx, dir, sha, nil, false, false, "")
	require.NoError(t, err)
	assert.Contains(t, out, "hello world")
}

func TestCopyDiff_WithStat(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)
	writeTestFile(t, dir, "file.txt", "hello world\n")

	out, err := NewHostWithEnv(testEnv()).CopyDiff(ctx, dir, sha, nil, true, false, "")
	require.NoError(t, err)
	assert.Contains(t, out, "file.txt")
	assert.Contains(t, out, "1 file changed")
}

func TestCopyDiff_WithPathFilter(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "a.txt", "a\n")
	writeTestFile(t, dir, "b.txt", "b\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)
	writeTestFile(t, dir, "a.txt", "aaa\n")
	writeTestFile(t, dir, "b.txt", "bbb\n")

	out, err := NewHostWithEnv(testEnv()).CopyDiff(ctx, dir, sha, []string{"a.txt"}, false, false, "")
	require.NoError(t, err)
	assert.Contains(t, out, "a.txt")
	assert.NotContains(t, out, "b.txt")
}

func TestCopyDiff_NewUntrackedFile(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "existing.txt", "existing\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)
	writeTestFile(t, dir, "new.txt", "new content\n")

	out, err := NewHostWithEnv(testEnv()).CopyDiff(ctx, dir, sha, nil, false, false, "")
	require.NoError(t, err)
	assert.Contains(t, out, "new.txt")
}

func TestCopyDiff_WithPathPrefix(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)
	writeTestFile(t, dir, "file.txt", "hello world\n")

	prefix := "/abs/path/to/project/"
	out, err := NewHostWithEnv(testEnv()).CopyDiff(ctx, dir, sha, nil, false, false, prefix)
	require.NoError(t, err)
	assert.Contains(t, out, "--- /abs/path/to/project/file.txt")
	assert.Contains(t, out, "+++ /abs/path/to/project/file.txt")
}

// ─── RWDiff ──────────────────────────────────────────────────────────────────

func TestRWDiff_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	out, err := NewHostWithEnv(testEnv()).RWDiff(ctx, dir, nil, false, false, false)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestRWDiff_NoChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	out, err := NewHostWithEnv(testEnv()).RWDiff(ctx, dir, nil, false, false, false)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestRWDiff_WithChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "file.txt", "hello world\n")

	out, err := NewHostWithEnv(testEnv()).RWDiff(ctx, dir, nil, false, false, false)
	require.NoError(t, err)
	assert.Contains(t, out, "hello world")
}

func TestRWDiff_WithStat(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "file.txt", "hello world\n")

	out, err := NewHostWithEnv(testEnv()).RWDiff(ctx, dir, nil, true, false, false)
	require.NoError(t, err)
	assert.Contains(t, out, "file.txt")
}

func TestRWDiff_WithPathFilter(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "a.txt", "a\n")
	writeTestFile(t, dir, "b.txt", "b\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "a.txt", "aaa\n")
	writeTestFile(t, dir, "b.txt", "bbb\n")

	out, err := NewHostWithEnv(testEnv()).RWDiff(ctx, dir, []string{"a.txt"}, false, false, false)
	require.NoError(t, err)
	assert.Contains(t, out, "a.txt")
	assert.NotContains(t, out, "b.txt")
}

// ─── HeadSHA, IsEmptyRepo ────────────────────────────────────────────────────

func TestHeadSHA_ValidRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "content")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha, err := NewHostWithEnv(testEnv()).HeadSHA(ctx, dir)
	require.NoError(t, err)
	assert.Len(t, sha, 40)
}

func TestHeadSHA_NoCommits(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	_, err := NewHostWithEnv(testEnv()).HeadSHA(ctx, dir)
	assert.Error(t, err)
}

func TestHeadSHA_NotGitRepo(t *testing.T) {
	_, err := NewHostWithEnv(testEnv()).HeadSHA(ctx, t.TempDir())
	assert.Error(t, err)
}

// TestRun_ExitOneReturnsExecError verifies that a non-zero git exit returns
// *runtime.ExecError so callers can match exit codes via errors.As. Regression
// guard: copyflow/apply.go treats `git diff --quiet HEAD` exit 1 as "diffs
// present" via errors.As(&runtime.ExecError); a plain string error would silently
// fall through to "real error", failing `yoloai apply` on every changed sandbox.
// The host execer backs the sandbox scope for backends that don't implement
// runtime.GitExecer (Docker, containerd, Seatbelt), so this guards that path too.
func TestRun_ExitOneReturnsExecError(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "f", "v1")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "init")
	writeTestFile(t, dir, "f", "v2")

	_, err := NewHostWithEnv(testEnv()).Run(ctx, dir, "diff", "--quiet", "HEAD")
	require.Error(t, err)
	var execErr *runtime.ExecError
	require.True(t, errors.As(err, &execErr), "Run must return *runtime.ExecError on non-zero exit; got %T: %v", err, err)
	assert.Equal(t, 1, execErr.ExitCode)
}

// ─── IsIndexLocked ───────────────────────────────────────────────────────────

func TestIsIndexLocked_DetectsLockError(t *testing.T) {
	err := fmt.Errorf("git [add -A]: exit status 128: fatal: Unable to create '.git/index.lock': File exists")
	assert.True(t, IsIndexLocked(err))
}

func TestIsIndexLocked_NilIsNotLocked(t *testing.T) {
	assert.False(t, IsIndexLocked(nil))
}

func TestIsIndexLocked_OtherErrorIsNotLocked(t *testing.T) {
	assert.False(t, IsIndexLocked(fmt.Errorf("some other git error")))
}

// ─── Baseline ────────────────────────────────────────────────────────────────

func TestBaseline_CreatesRepoWithCommit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "file.txt", "hello")

	sha, err := NewHostWithEnv(testEnv()).Baseline(ctx, dir)
	require.NoError(t, err)

	assert.Len(t, sha, 40)
	assert.Regexp(t, `^[0-9a-f]{40}$`, sha)

	cmd := sysexec.Command(testutil.GitEnv(), "git", "-C", dir, "log", "--oneline")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "yoloai baseline")
}

func TestBaseline_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	sha, err := NewHostWithEnv(testEnv()).Baseline(ctx, dir)
	require.NoError(t, err)

	assert.Len(t, sha, 40)
	assert.Regexp(t, `^[0-9a-f]{40}$`, sha)
}

func TestBaseline_SetsUserConfig(t *testing.T) {
	dir := t.TempDir()

	_, err := NewHostWithEnv(testEnv()).Baseline(ctx, dir)
	require.NoError(t, err)

	cmd := sysexec.Command(testutil.GitEnv(), "git", "-C", dir, "config", "user.email")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "yoloai@localhost", strings.TrimSpace(string(output)))
}

// ─── BaselineUncommittedChanges ──────────────────────────────────────────────

func TestBaselineUncommittedChanges_DirtyTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "original\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	originalSHA := headSHA(t, dir)

	writeTestFile(t, dir, "file.txt", "modified\n")
	writeTestFile(t, dir, "new.txt", "untracked\n")

	newSHA, err := NewHostWithEnv(testEnv()).BaselineUncommittedChanges(ctx, dir)
	require.NoError(t, err)
	assert.NotEqual(t, originalSHA, newSHA, "should have created a new commit")
	assert.Len(t, newSHA, 40)

	cmd := sysexec.Command(testutil.GitEnv(), "git", "-C", dir, "log", "-1", "--format=%s")
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "yoloai: pre-session state", strings.TrimSpace(string(out)))
}

func TestBaselineUncommittedChanges_CleanTree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "content\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	originalSHA := headSHA(t, dir)

	newSHA, err := NewHostWithEnv(testEnv()).BaselineUncommittedChanges(ctx, dir)
	require.NoError(t, err)
	assert.Equal(t, originalSHA, newSHA, "clean tree should not create a new commit")
}

// ─── StageUntracked ──────────────────────────────────────────────────────────

func TestStageUntracked_NewFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "a.txt", "a")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "b.txt", "b")

	require.NoError(t, NewHostWithEnv(testEnv()).StageUntracked(ctx, dir))

	cmd := sysexec.Command(testutil.GitEnv(), "git", "-C", dir, "diff", "--cached", "--name-only")
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "b.txt")
}

func TestStageUntracked_EmptyRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	assert.NoError(t, NewHostWithEnv(testEnv()).StageUntracked(ctx, dir))
}

// ─── CheckDirtyRepo ──────────────────────────────────────────────────────────

func TestCheckDirtyRepo_CleanRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	warning, err := NewHostWithEnv(testEnv()).CheckDirtyRepo(ctx, dir)
	require.NoError(t, err)
	assert.Empty(t, warning)
}

func TestCheckDirtyRepo_DirtyRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "file.txt", "modified")
	writeTestFile(t, dir, "new.txt", "untracked")

	warning, err := NewHostWithEnv(testEnv()).CheckDirtyRepo(ctx, dir)
	require.NoError(t, err)
	assert.Contains(t, warning, "modified")
	assert.Contains(t, warning, "untracked")
}

func TestCheckDirtyRepo_NotGitRepo(t *testing.T) {
	dir := t.TempDir()

	warning, err := NewHostWithEnv(testEnv()).CheckDirtyRepo(ctx, dir)
	require.NoError(t, err)
	assert.Empty(t, warning)
}

func TestCheckDirtyRepo_GitStatusFailureIsError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, ".git"), 0o750))

	warning, err := NewHostWithEnv(testEnv()).CheckDirtyRepo(ctx, dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "git status")
	assert.Empty(t, warning)
}

func TestCheckDirtyRepo_IgnoresBugreportFiles(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "yoloai-bugreport-20260316-102123.534.md", "bugreport content")
	writeTestFile(t, dir, "yoloai-bugreport-20260316-103627.211.md.tmp", "temp bugreport")

	warning, err := NewHostWithEnv(testEnv()).CheckDirtyRepo(ctx, dir)
	require.NoError(t, err)
	assert.Empty(t, warning, "bugreport files should be ignored")

	writeTestFile(t, dir, "new.txt", "untracked")

	warning, err = NewHostWithEnv(testEnv()).CheckDirtyRepo(ctx, dir)
	require.NoError(t, err)
	assert.Contains(t, warning, "1 untracked")
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func headSHA(t *testing.T, dir string) string {
	t.Helper()
	sha, err := NewHostWithEnv(testEnv()).HeadSHA(ctx, dir)
	require.NoError(t, err)
	return sha
}
