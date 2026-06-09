// ABOUTME: Transitional wrappers — keep deferred callers (reset.go, patch/*.go,
// ABOUTME: sandbox/tags.go) compiling while they await the full sandbox migration.
// ABOUTME: All git logic now lives in internal/git; these are thin pass-throughs.
package workspace

import (
	"context"
	"os/exec"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/git"
)

// Git is a thin workspace alias for git.Git, preserved so callers in
// prepare_dirs.go (and tests) that construct *workspace.Git can still compile
// until they migrate to *git.Git directly.
type Git = git.Git

// NewGit returns a host-git executor whose subprocess env is the curated git
// env derived from the layout. Delegates to git.NewHost.
func NewGit(layout config.Layout) *Git {
	return git.NewHost(layout)
}

// NewGitWithEnv builds a host-git executor over an explicit, already-curated
// env. Use in tests (testutil.GitEnv) and other non-layout call sites.
// Delegates to git.NewHostWithEnv.
func NewGitWithEnv(env []string) *Git {
	return git.NewHostWithEnv(env)
}

// IsGitRepo checks if a directory is a git repository.
// Delegates to git.IsGitRepo.
func IsGitRepo(dir string) bool { return git.IsGitRepo(dir) }

// IsIndexLocked reports whether err is a git index.lock contention error.
// Delegates to git.IsIndexLocked.
func IsIndexLocked(err error) bool { return git.IsIndexLocked(err) }

// --- transitional free-function wrappers (DEV §12 git-executor migration) ---
// These keep existing callers compiling while call sites migrate to git.Git.
// Use context.Background() since the deferred callers do not thread ctx yet.

func NewGitCmdWithEnv(env []string, dir string, args ...string) *exec.Cmd {
	return git.NewHostWithEnv(env).Cmd(dir, args...)
}

func HeadSHAWithEnv(env []string, dir string) (string, error) {
	return git.NewHostWithEnv(env).HeadSHA(context.Background(), dir)
}

func IsEmptyRepo(env []string, dir string) bool {
	return git.NewHostWithEnv(env).IsEmptyRepo(context.Background(), dir)
}

func RunGitCmdWithEnv(env []string, dir string, args ...string) error {
	return git.NewHostWithEnv(env).RunCmd(context.Background(), dir, args...)
}

func BaselineWithEnv(env []string, workDir string) (string, error) {
	return git.NewHostWithEnv(env).Baseline(context.Background(), workDir)
}

func BaselineUncommittedChangesWithEnv(env []string, workDir string) (string, error) {
	return git.NewHostWithEnv(env).BaselineUncommittedChanges(context.Background(), workDir)
}

func StageUntrackedWithEnv(env []string, workDir string) error {
	return git.NewHostWithEnv(env).StageUntracked(context.Background(), workDir)
}

func CopyDiff(env []string, workDir, baselineSHA string, paths []string, stat, nameOnly bool) (string, error) {
	return git.NewHostWithEnv(env).CopyDiff(context.Background(), workDir, baselineSHA, paths, stat, nameOnly)
}

func RWDiff(env []string, workDir string, paths []string, stat, nameOnly bool) (string, error) {
	return git.NewHostWithEnv(env).RWDiff(context.Background(), workDir, paths, stat, nameOnly)
}

func CheckPatch(env []string, patch []byte, targetDir string, isGit bool) error {
	return git.NewHostWithEnv(env).CheckPatch(context.Background(), patch, targetDir, isGit)
}

func ApplyPatch(env []string, patch []byte, targetDir string, isGit bool) error {
	return git.NewHostWithEnv(env).ApplyPatch(context.Background(), patch, targetDir, isGit)
}

func ApplyFormatPatch(env []string, patchDir string, files []string, targetDir string) (map[string]string, error) {
	return git.NewHostWithEnv(env).ApplyFormatPatch(context.Background(), patchDir, files, targetDir)
}

func CheckDirtyRepo(env []string, path string) (string, error) {
	return git.NewHostWithEnv(env).CheckDirtyRepo(context.Background(), path)
}
