package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GeneratePatch produces a binary patch from the work copy against
// the baseline SHA. Optionally filtered to specific paths.
// Returns the patch bytes and a stat summary string.
func GeneratePatch(name string, paths []string) (patch []byte, stat string, err error) {
	workDir, baselineSHA, mode, err := loadDiffContext(name)
	if err != nil {
		return nil, "", err
	}

	if mode == "rw" {
		return nil, "", fmt.Errorf("apply is not needed for :rw directories — changes are already live")
	}

	if err := stageUntracked(workDir); err != nil {
		return nil, "", err
	}

	// Generate binary patch
	patchArgs := []string{"diff", "--binary", baselineSHA}
	if len(paths) > 0 {
		patchArgs = append(patchArgs, "--")
		patchArgs = append(patchArgs, paths...)
	}

	patchCmd := newGitCmd(workDir, patchArgs...)
	patchOut, err := patchCmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("git diff (patch): %w", err)
	}

	// Generate stat summary
	statArgs := []string{"diff", "--stat", baselineSHA}
	if len(paths) > 0 {
		statArgs = append(statArgs, "--")
		statArgs = append(statArgs, paths...)
	}

	statCmd := newGitCmd(workDir, statArgs...)
	statOut, err := statCmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("git diff (stat): %w", err)
	}

	return patchOut, strings.TrimRight(string(statOut), "\n"), nil
}

// CheckPatch verifies the patch applies cleanly to the target directory
// via `git apply --check`. Returns nil if clean, error with context if not.
func CheckPatch(patch []byte, targetDir string, isGit bool) error {
	if isGit {
		if err := runGitApply(targetDir, patch, "--check"); err != nil {
			return formatApplyError(err, targetDir)
		}
		return nil
	}

	return withTempGitDir(func(tmpDir string) error {
		if err := runGitApply(tmpDir, patch, "--check", "--unsafe-paths", "--directory="+targetDir); err != nil {
			return formatApplyError(err, targetDir)
		}
		return nil
	})
}

// ApplyPatch applies the patch to the target directory.
// For git repos: runs `git apply` from within the repo.
// For non-git dirs: runs `git apply --unsafe-paths --directory=<path>`
// from a temporary git-initialized directory.
func ApplyPatch(patch []byte, targetDir string, isGit bool) error {
	if isGit {
		if err := runGitApply(targetDir, patch); err != nil {
			return formatApplyError(err, targetDir)
		}
		return nil
	}

	return withTempGitDir(func(tmpDir string) error {
		if err := runGitApply(tmpDir, patch, "--unsafe-paths", "--directory="+targetDir); err != nil {
			return formatApplyError(err, targetDir)
		}
		return nil
	})
}

// IsGitRepo checks if a directory is a git repository by looking for .git/.
func IsGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// withTempGitDir creates a temporary git-initialized directory, calls fn
// with its path, and cleans up afterward.
func withTempGitDir(fn func(tmpDir string) error) error {
	tmpDir, err := os.MkdirTemp("", "yoloai-apply-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck // best-effort cleanup

	if err := runGitCmd(tmpDir, "init"); err != nil {
		return fmt.Errorf("git init temp dir: %w", err)
	}

	return fn(tmpDir)
}

// runGitApply executes `git apply` with the given args, feeding the
// patch via stdin. Returns the combined output on error.
func runGitApply(dir string, patch []byte, args ...string) error {
	applyArgs := append([]string{"apply"}, args...)
	cmd := newGitCmd(dir, applyArgs...)
	cmd.Stdin = bytes.NewReader(patch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// Regex patterns for common git apply errors.
var (
	rePatchFailed   = regexp.MustCompile(`error: patch failed: ([^:]+):(\d+)`)
	reDoesNotExist  = regexp.MustCompile(`error: ([^:]+): does not exist in working directory`)
	reAlreadyExists = regexp.MustCompile(`error: ([^:]+): already exists in working directory`)
)

// formatApplyError wraps a cryptic git apply error with human-readable
// context. Parses the error output to identify conflicting files and
// provides actionable guidance.
func formatApplyError(gitErr error, targetDir string) error {
	msg := gitErr.Error()

	if m := rePatchFailed.FindStringSubmatch(msg); m != nil {
		return fmt.Errorf("changes to %s conflict with your working directory — "+
			"the patch expected different content at line %s. "+
			"This typically means the original file was edited after the sandbox was created",
			m[1], m[2])
	}

	if m := reDoesNotExist.FindStringSubmatch(msg); m != nil {
		return fmt.Errorf("cannot apply deletion to %s — the file no longer exists in %s",
			m[1], targetDir)
	}

	if m := reAlreadyExists.FindStringSubmatch(msg); m != nil {
		return fmt.Errorf("cannot create %s — it already exists in %s with different content",
			m[1], targetDir)
	}

	return fmt.Errorf("git apply failed in %s: %w", targetDir, gitErr)
}
