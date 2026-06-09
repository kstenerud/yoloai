// ABOUTME: Git helpers (InitGitRepo, GitAdd, GitCommit, WriteFile) for setting up
// ABOUTME: throwaway repositories used by sandbox and workspace integration tests.
// Package testutil provides shared test helpers for use across all test packages.
// It is not a _test.go file so it can be imported by test files in other packages.
package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/sysexec"
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
	// Test helpers use os.Environ() — testutil is excluded from the forbidigo ban (§12).
	cmd := sysexec.Command(os.Environ(), "git", "-C", dir, "rev-parse", "HEAD") //nolint:forbidigo // §12: test helper; testutil/ is excluded
	out, err := cmd.Output()
	require.NoError(t, err, "git rev-parse HEAD failed")
	return strings.TrimSpace(string(out))
}

// RunGit runs an arbitrary git command in dir.
func RunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := sysexec.Command(os.Environ(), "git", args...) //nolint:forbidigo // §12: test helper; testutil/ is excluded
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, out)
}

// RunGitOutput runs an arbitrary git command in dir and returns its trimmed
// stdout. Use for read-only queries (rev-list, rev-parse) in assertions.
func RunGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := sysexec.Command(os.Environ(), "git", args...) //nolint:forbidigo // §12: test helper; testutil/ is excluded
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err, "git %v failed", args)
	return strings.TrimSpace(string(out))
}

// WriteFile writes content to name inside dir.
func WriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600)
	require.NoError(t, err)
}
