// ABOUTME: Git — a host-git executor that bakes a curated subprocess env once and
// ABOUTME: exposes purpose methods (Baseline, RunCmd, HeadSHA, …) for copy/diff/apply.
package workspace

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sysexec"
)

// Git runs host git with a curated, explicit subprocess environment that is
// constructed once (NewGit) and never handed back to callers — they ask the
// executor to run git rather than building an env and an exec themselves
// (DEV §12). It is the single host-git entry point for workspace copy/diff/apply.
type Git struct {
	env []string
}

// NewGit returns a host-git executor whose subprocess env is the curated git
// env derived from the layout (PATH/HOME/TMPDIR/SUDO_UID — see sysexec.GitEnv).
// The env is an internal detail from here on; callers use the methods.
func NewGit(layout config.Layout) *Git {
	return &Git{env: sysexec.GitEnv(layout.Env)}
}

// NewGitWithEnv builds a host-git executor over an explicit, already-curated
// env. Use it where the env is not layout-derived — chiefly tests, which supply
// a hermetic git env (testutil.GitEnv). Prefer NewGit in production code.
func NewGitWithEnv(env []string) *Git {
	return &Git{env: env}
}

// Cmd builds an *exec.Cmd for git in dir with the executor's curated env and
// hooks disabled. Use for the few call sites that wire stdin/stdout themselves;
// otherwise prefer RunCmd / HeadSHA / the higher-level methods.
func (g *Git) Cmd(dir string, args ...string) *exec.Cmd {
	fullArgs := append([]string{"-c", "core.hooksPath=/dev/null", "-C", dir}, args...)
	return sysexec.Command(g.env, "git", fullArgs...)
}

// HeadSHA returns the HEAD commit SHA for the git repo at dir.
func (g *Git) HeadSHA(dir string) (string, error) {
	output, err := g.Cmd(dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// IsEmptyRepo reports whether dir is a git repository with no commits yet.
func (g *Git) IsEmptyRepo(dir string) bool {
	return g.Cmd(dir, "rev-parse", "--verify", "HEAD").Run() != nil
}

// RunCmd executes a git command in dir, returning a wrapped error with the
// combined output on failure.
func (g *Git) RunCmd(dir string, args ...string) error {
	if output, err := g.Cmd(dir, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(string(output)), err)
	}
	return nil
}

// Baseline creates a fresh git baseline for the work copy.
// Assumes all .git entries have already been removed by RemoveGitDirs.
func (g *Git) Baseline(workDir string) (string, error) {
	cmds := [][]string{
		{"init"},
		{"config", "user.email", "yoloai@localhost"},
		{"config", "user.name", "yoloai"},
		{"add", "-A"},
		{"commit", "-m", "yoloai baseline", "--allow-empty"},
	}
	for _, args := range cmds {
		if err := g.RunCmd(workDir, args...); err != nil {
			return "", err
		}
	}
	if err := chownGitDir(workDir); err != nil {
		return "", err
	}
	return g.HeadSHA(workDir)
}

// BaselineUncommittedChanges commits any pre-existing uncommitted changes in
// workDir as "yoloai: pre-session state".
func (g *Git) BaselineUncommittedChanges(workDir string) (string, error) {
	out, err := g.Cmd(workDir, "status", "--porcelain").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return g.HeadSHA(workDir)
	}
	if err := g.RunCmd(workDir, "add", "-A"); err != nil {
		return "", fmt.Errorf("stage pre-session changes: %w", err)
	}
	if err := g.RunCmd(workDir,
		"-c", "user.email=yoloai@localhost",
		"-c", "user.name=yoloai",
		"commit", "-m", "yoloai: pre-session state",
	); err != nil {
		return "", fmt.Errorf("commit pre-session state: %w", err)
	}
	if err := chownGitDir(workDir); err != nil {
		return "", err
	}
	return g.HeadSHA(workDir)
}

// StageUntracked runs `git add -A` in the work directory to capture files
// created by the agent that are not yet tracked. Retries on index.lock
// contention (the in-container agent's git can briefly hold the lock).
func (g *Git) StageUntracked(workDir string) error {
	var err error
	for range 5 {
		err = g.RunCmd(workDir, "add", "-A")
		if err == nil || !IsIndexLocked(err) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
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

// IsIndexLocked reports whether err is a git index.lock contention error.
func IsIndexLocked(err error) bool {
	return err != nil && strings.Contains(err.Error(), "index.lock")
}

// --- transitional free-function wrappers (DEV §12 git-executor migration) ---
// These keep existing callers compiling while call sites migrate to the Git
// executor methods above. Removed once no caller references them.

func NewGitCmdWithEnv(env []string, dir string, args ...string) *exec.Cmd {
	return NewGitWithEnv(env).Cmd(dir, args...)
}
func HeadSHAWithEnv(env []string, dir string) (string, error) {
	return NewGitWithEnv(env).HeadSHA(dir)
}
func IsEmptyRepo(env []string, dir string) bool { return NewGitWithEnv(env).IsEmptyRepo(dir) }
func RunGitCmdWithEnv(env []string, dir string, args ...string) error {
	return NewGitWithEnv(env).RunCmd(dir, args...)
}
func BaselineWithEnv(env []string, workDir string) (string, error) {
	return NewGitWithEnv(env).Baseline(workDir)
}
func BaselineUncommittedChangesWithEnv(env []string, workDir string) (string, error) {
	return NewGitWithEnv(env).BaselineUncommittedChanges(workDir)
}
func StageUntrackedWithEnv(env []string, workDir string) error {
	return NewGitWithEnv(env).StageUntracked(workDir)
}
