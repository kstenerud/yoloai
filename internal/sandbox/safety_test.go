package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsDangerousDir_Home(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.True(t, IsDangerousDir(home))
}

func TestIsDangerousDir_Root(t *testing.T) {
	assert.True(t, IsDangerousDir("/"))
}

func TestIsDangerousDir_SystemDirs(t *testing.T) {
	systemDirs := []string{
		"/usr", "/etc", "/var", "/boot", "/bin", "/sbin", "/lib",
		"/System", "/Library", "/Applications",
	}
	for _, dir := range systemDirs {
		assert.True(t, IsDangerousDir(dir), "expected %s to be dangerous", dir)
	}
}

func TestIsDangerousDir_SafeDir(t *testing.T) {
	assert.False(t, IsDangerousDir("/tmp/myproject"))
}

func TestCheckPathOverlap_NoOverlap(t *testing.T) {
	err := CheckPathOverlap([]string{"/a", "/b", "/c"})
	assert.NoError(t, err)
}

func TestCheckPathOverlap_ParentChild(t *testing.T) {
	err := CheckPathOverlap([]string{"/a", "/a/b"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/a")
	assert.Contains(t, err.Error(), "/a/b")
}

func TestCheckPathOverlap_Identical(t *testing.T) {
	err := CheckPathOverlap([]string{"/a", "/a"})
	assert.Error(t, err)
}

func TestCheckPathOverlap_DisjointSimilarNames(t *testing.T) {
	err := CheckPathOverlap([]string{"/abc", "/ab"})
	assert.NoError(t, err, "/ab is not a parent of /abc")
}

func TestCheckDirtyRepo_CleanRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Create and commit a file
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	warning, err := CheckDirtyRepo(dir)
	require.NoError(t, err)
	assert.Empty(t, warning)
}

func TestCheckDirtyRepo_DirtyRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Create and commit a file
	writeTestFile(t, dir, "file.txt", "hello")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	// Modify the file and add an untracked file
	writeTestFile(t, dir, "file.txt", "modified")
	writeTestFile(t, dir, "new.txt", "untracked")

	warning, err := CheckDirtyRepo(dir)
	require.NoError(t, err)
	assert.Contains(t, warning, "modified")
	assert.Contains(t, warning, "untracked")
}

func TestCheckDirtyRepo_NotGitRepo(t *testing.T) {
	dir := t.TempDir()

	warning, err := CheckDirtyRepo(dir)
	require.NoError(t, err)
	assert.Empty(t, warning)
}

// Test helpers

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
}

func gitAdd(t *testing.T, dir, path string) {
	t.Helper()
	runGit(t, dir, "add", path)
}

func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	runGit(t, dir, "commit", "-m", msg)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // G204: test helper with known command
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, out)
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600)
	require.NoError(t, err)
}
