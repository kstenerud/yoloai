package workspace

import (
	"fmt"
	"os/exec"
	"strings"
)

// NewGitCmd builds an exec.Cmd for git with hooks disabled.
// All internal git operations use this to prevent copied hooks from firing.
func NewGitCmd(dir string, args ...string) *exec.Cmd {
	fullArgs := append([]string{"-c", "core.hooksPath=/dev/null", "-C", dir}, args...)
	return exec.Command("git", fullArgs...) //nolint:gosec // G204: dir is sandbox-controlled path
}

// HeadSHA returns the HEAD commit SHA for the given git repo.
func HeadSHA(dir string) (string, error) {
	cmd := NewGitCmd(dir, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// IsEmptyRepo reports whether dir is a git repository with no commits yet.
func IsEmptyRepo(dir string) bool {
	cmd := NewGitCmd(dir, "rev-parse", "--verify", "HEAD")
	return cmd.Run() != nil
}

// RunGitCmd executes a git command in the given directory.
func RunGitCmd(dir string, args ...string) error {
	cmd := NewGitCmd(dir, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(string(output)), err)
	}
	return nil
}

// Baseline creates a fresh git baseline for the work copy.
// Assumes all .git entries have already been removed by RemoveGitDirs.
func Baseline(workDir string) (string, error) {
	cmds := [][]string{
		{"init"},
		{"config", "user.email", "yoloai@localhost"},
		{"config", "user.name", "yoloai"},
		{"add", "-A"},
		{"commit", "-m", "yoloai baseline", "--allow-empty"},
	}
	for _, args := range cmds {
		if err := RunGitCmd(workDir, args...); err != nil {
			return "", err
		}
	}

	return HeadSHA(workDir)
}

// StageUntracked runs `git add -A` in the work directory to capture
// files created by the agent that are not yet tracked.
func StageUntracked(workDir string) error {
	return RunGitCmd(workDir, "add", "-A")
}
