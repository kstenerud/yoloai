// ABOUTME: NewGitCmdWithEnv, HeadSHAWithEnv, IsEmptyRepo, RunGitCmdWithEnv — low-level
// ABOUTME: git wrappers with explicit env, shared by copy/diff/apply operations in workspace/.
package workspace

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sysexec"
)

// NewGitCmdWithEnv builds an exec.Cmd for git with an explicit environment
// and hooks disabled. Callers must supply a layout-derived env (DEV §12).
func NewGitCmdWithEnv(env []string, dir string, args ...string) *exec.Cmd {
	fullArgs := append([]string{"-c", "core.hooksPath=/dev/null", "-C", dir}, args...)
	return sysexec.Command(env, "git", fullArgs...)
}

// HeadSHAWithEnv returns the HEAD commit SHA for the given git repo using
// an explicit subprocess env (DEV §12).
func HeadSHAWithEnv(env []string, dir string) (string, error) {
	cmd := NewGitCmdWithEnv(env, dir, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// IsEmptyRepo reports whether dir is a git repository with no commits yet.
func IsEmptyRepo(env []string, dir string) bool {
	cmd := NewGitCmdWithEnv(env, dir, "rev-parse", "--verify", "HEAD")
	return cmd.Run() != nil
}

// RunGitCmdWithEnv executes a git command in the given directory with an
// explicit subprocess env (DEV §12).
func RunGitCmdWithEnv(env []string, dir string, args ...string) error {
	cmd := NewGitCmdWithEnv(env, dir, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(string(output)), err)
	}
	return nil
}

// BaselineWithEnv creates a fresh git baseline for the work copy using an
// explicit subprocess env (DEV §12).
// Assumes all .git entries have already been removed by RemoveGitDirs.
func BaselineWithEnv(env []string, workDir string) (string, error) {
	cmds := [][]string{
		{"init"},
		{"config", "user.email", "yoloai@localhost"},
		{"config", "user.name", "yoloai"},
		{"add", "-A"},
		{"commit", "-m", "yoloai baseline", "--allow-empty"},
	}
	for _, args := range cmds {
		if err := RunGitCmdWithEnv(env, workDir, args...); err != nil {
			return "", err
		}
	}
	if err := chownGitDir(workDir); err != nil {
		return "", err
	}
	return HeadSHAWithEnv(env, workDir)
}

// chownGitDir hands the .git tree back to the invoking user when yoloai runs
// under sudo. git ran as root, so every object it wrote is root-owned; without
// this the user cannot remove or repair the sandbox without sudo. No-op when
// not running under sudo.
func chownGitDir(workDir string) error {
	if err := fileutil.ChownRecursiveIfSudo(filepath.Join(workDir, ".git")); err != nil {
		return fmt.Errorf("fix .git ownership: %w", err)
	}
	return nil
}

// BaselineUncommittedChangesWithEnv commits any pre-existing uncommitted changes
// in workDir as "yoloai: pre-session state", using an explicit subprocess env (DEV §12).
func BaselineUncommittedChangesWithEnv(env []string, workDir string) (string, error) {
	out, err := NewGitCmdWithEnv(env, workDir, "status", "--porcelain").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return HeadSHAWithEnv(env, workDir)
	}
	if err := RunGitCmdWithEnv(env, workDir, "add", "-A"); err != nil {
		return "", fmt.Errorf("stage pre-session changes: %w", err)
	}
	if err := RunGitCmdWithEnv(env, workDir,
		"-c", "user.email=yoloai@localhost",
		"-c", "user.name=yoloai",
		"commit", "-m", "yoloai: pre-session state",
	); err != nil {
		return "", fmt.Errorf("commit pre-session state: %w", err)
	}
	if err := chownGitDir(workDir); err != nil {
		return "", err
	}
	return HeadSHAWithEnv(env, workDir)
}

// IsIndexLocked reports whether err is a git index.lock contention error.
// When the agent is running inside a container on a bind-mounted work dir,
// its internal git operations (e.g. status bar updates) can briefly hold
// the lock. Callers that need to run git add -A concurrently should retry
// on this error rather than failing immediately.
func IsIndexLocked(err error) bool {
	return err != nil && strings.Contains(err.Error(), "index.lock")
}

// StageUntrackedWithEnv runs `git add -A` in the work directory to capture
// files created by the agent that are not yet tracked, using an explicit
// subprocess env (DEV §12). Retries on index.lock contention.
func StageUntrackedWithEnv(env []string, workDir string) error {
	var err error
	for range 5 {
		err = RunGitCmdWithEnv(env, workDir, "add", "-A")
		if err == nil || !IsIndexLocked(err) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}
