package sandbox

import (
	"fmt"
	"os/exec"
	"strings"
)

// newGitCmd builds an exec.Cmd for git with hooks disabled.
// All internal git operations use this to prevent copied hooks from firing.
func newGitCmd(dir string, args ...string) *exec.Cmd {
	fullArgs := append([]string{"-c", "core.hooksPath=/dev/null", "-C", dir}, args...)
	return exec.Command("git", fullArgs...) //nolint:gosec // G204: dir is sandbox-controlled path
}

// gitHeadSHA returns the HEAD commit SHA for the given git repo.
func gitHeadSHA(dir string) (string, error) {
	cmd := newGitCmd(dir, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// runGitCmd executes a git command in the given directory.
func runGitCmd(dir string, args ...string) error {
	cmd := newGitCmd(dir, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(string(output)), err)
	}
	return nil
}

// gitBaseline creates a fresh git baseline for the work copy.
// Assumes all .git entries have already been removed by removeGitDirs.
func gitBaseline(workDir string) (string, error) {
	cmds := [][]string{
		{"init"},
		{"config", "user.email", "yoloai@localhost"},
		{"config", "user.name", "yoloai"},
		{"add", "-A"},
		{"commit", "-m", "yoloai baseline", "--allow-empty"},
	}
	for _, args := range cmds {
		if err := runGitCmd(workDir, args...); err != nil {
			return "", err
		}
	}

	return gitHeadSHA(workDir)
}

// stageUntracked runs `git add -A` in the work directory to capture
// files created by the agent that are not yet tracked.
func stageUntracked(workDir string) error {
	return runGitCmd(workDir, "add", "-A")
}
