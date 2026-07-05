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

// GitEnv returns a curated, hermetic environment for running git in tests:
// the host PATH (so the git binary is found) and SUDO_UID, plus an explicit
// identity and disabled global/system config. It deliberately does NOT hand git
// the full ambient environment — that would re-introduce the inheritance DEV §12
// exists to prevent and make tests depend on the developer's ~/.gitconfig. The
// single os.Environ read happens in GetCuratedHostEnv (the test edge), which
// narrows to PATH + SUDO_UID here.
//
// SUDO_UID mirrors production sysexec.GitEnv: under `sudo make integration`, git
// runs as root against a work copy owned by the invoking user, and needs
// SUDO_UID to accept it instead of failing git's dubious-ownership guard. Absent
// off-sudo, so it is a no-op there.
func GitEnv() []string {
	return sysexec.Curated(GetCuratedHostEnv([]string{"PATH", "SUDO_UID"}), []string{"PATH", "SUDO_UID"}, map[string]string{
		"GIT_CONFIG_GLOBAL":   "/dev/null",
		"GIT_CONFIG_SYSTEM":   "/dev/null",
		"GIT_AUTHOR_NAME":     "yoloai-test",
		"GIT_AUTHOR_EMAIL":    "test@yoloai.test",
		"GIT_COMMITTER_NAME":  "yoloai-test",
		"GIT_COMMITTER_EMAIL": "test@yoloai.test",
	})
}

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
	cmd := sysexec.Command(GitEnv(), "git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	require.NoError(t, err, "git rev-parse HEAD failed")
	return strings.TrimSpace(string(out))
}

// RunGit runs an arbitrary git command in dir.
func RunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := sysexec.Command(GitEnv(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, out)
}

// RunGitOutput runs an arbitrary git command in dir and returns its trimmed
// stdout. Use for read-only queries (rev-list, rev-parse) in assertions.
func RunGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := sysexec.Command(GitEnv(), "git", args...)
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
