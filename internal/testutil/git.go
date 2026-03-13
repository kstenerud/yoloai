// Package testutil provides shared test helpers for use across all test packages.
// It is not a _test.go file so it can be imported by test files in other packages.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// InitGitRepo initializes a git repository in dir with a test user identity.
func InitGitRepo(t *testing.T, dir string) {
	t.Helper()
	RunGit(t, dir, "init")
	RunGit(t, dir, "config", "user.email", "test@test.com")
	RunGit(t, dir, "config", "user.name", "Test")
}

// GitAdd stages path in the git repo at dir.
func GitAdd(t *testing.T, dir, path string) {
	t.Helper()
	RunGit(t, dir, "add", path)
}

// GitCommit creates a commit in the git repo at dir.
func GitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	RunGit(t, dir, "commit", "-m", msg)
}

// GitRevParse returns the current HEAD SHA of the git repo at dir.
func GitRevParse(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD") //nolint:gosec // G204: test helper
	out, err := cmd.Output()
	require.NoError(t, err, "git rev-parse HEAD failed")
	return strings.TrimSpace(string(out))
}

// RunGit runs an arbitrary git command in dir.
func RunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // G204: test helper with known command
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, out)
}

// WriteFile writes content to name inside dir.
func WriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600)
	require.NoError(t, err)
}
