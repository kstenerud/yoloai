package workspace

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CommitInfo holds a commit SHA and its subject line.
type CommitInfo struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

// PatchSet holds patch data for a single directory.
type PatchSet struct {
	HostPath string // original host path (for display)
	Mode     string // "copy" or "overlay"
	Patch    []byte
	Stat     string
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

	// Resolve symlinks so git apply doesn't reject the path with
	// "beyond a symbolic link" (e.g. macOS /var -> /private/var).
	realTarget, err := filepath.EvalSymlinks(targetDir)
	if err != nil {
		return fmt.Errorf("resolve target dir: %w", err)
	}

	return withTempGitDir(func(tmpDir string) error {
		if err := runGitApply(tmpDir, patch, "--check", "--unsafe-paths", "--directory="+realTarget); err != nil {
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

	// Resolve symlinks so git apply doesn't reject the path with
	// "beyond a symbolic link" (e.g. macOS /var -> /private/var).
	realTarget, err := filepath.EvalSymlinks(targetDir)
	if err != nil {
		return fmt.Errorf("resolve target dir: %w", err)
	}

	return withTempGitDir(func(tmpDir string) error {
		if err := runGitApply(tmpDir, patch, "--unsafe-paths", "--directory="+realTarget); err != nil {
			return formatApplyError(err, targetDir)
		}
		return nil
	})
}

// ApplyFormatPatch applies .patch files to a target git directory using
// git am --3way. Returns a map of sandbox SHA → host SHA for applied commits,
// which callers can use to re-create tags on the host. On failure, returns an
// error with guidance about git am --continue/--skip/--abort.
func ApplyFormatPatch(patchDir string, files []string, targetDir string) (map[string]string, error) {
	if len(files) == 0 {
		return nil, nil
	}

	// Record HEAD before applying so we can identify new commits afterward.
	preTip, err := HeadSHA(targetDir)
	if err != nil {
		return nil, err
	}

	// Extract sandbox SHAs from patch file headers (first line: "From <sha> <date>").
	sandboxSHAs := make([]string, 0, len(files))
	for _, f := range files {
		sha, parseErr := parsePatchSHA(filepath.Join(patchDir, f))
		if parseErr != nil {
			return nil, parseErr
		}
		sandboxSHAs = append(sandboxSHAs, sha)
	}

	// Build full paths
	fullPaths := make([]string, len(files))
	for i, f := range files {
		fullPaths[i] = filepath.Join(patchDir, f)
	}

	args := append([]string{"am", "--3way"}, fullPaths...)
	cmd := NewGitCmd(targetDir, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, formatAMError(output, targetDir)
	}

	// Collect new host SHAs in chronological order.
	logCmd := NewGitCmd(targetDir, "log", "--reverse", "--format=%H", preTip+"..HEAD")
	logOut, logErr := logCmd.Output()
	if logErr != nil {
		// Commits are applied; SHA map is a best-effort bonus.
		return nil, nil
	}
	hostSHAs := strings.Fields(strings.TrimSpace(string(logOut)))

	// Pair positionally: sandboxSHA[i] → hostSHA[i].
	shaMap := make(map[string]string, len(sandboxSHAs))
	for i, sandboxSHA := range sandboxSHAs {
		if i < len(hostSHAs) {
			shaMap[strings.ToLower(sandboxSHA)] = hostSHAs[i]
		}
	}
	return shaMap, nil
}

// parsePatchSHA extracts the original commit SHA from a format-patch file.
// The first line of a format-patch file is: "From <sha> <date>"
func parsePatchSHA(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is within sandbox-controlled temp dir
	if err != nil {
		return "", fmt.Errorf("open patch %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "From ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[1], nil
			}
		}
	}
	return "", fmt.Errorf("could not parse SHA from patch file %s", path)
}

// IsGitRepo checks if a directory is a git repository by looking for .git/.
func IsGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// ContiguousPrefixEnd finds how far the baseline can safely advance after
// a selective apply. Given the full ordered list of commits beyond baseline
// and the set of applied SHAs, it returns the index (in allCommits) of the
// last commit in the contiguous prefix starting from index 0.
// Returns -1 if no contiguous prefix exists (first commit wasn't applied).
func ContiguousPrefixEnd(allCommits []CommitInfo, appliedSHAs map[string]bool) int {
	end := -1
	for i, c := range allCommits {
		if appliedSHAs[c.SHA] {
			end = i
		} else {
			break
		}
	}
	return end
}

// withTempGitDir creates a temporary git-initialized directory, calls fn
// with its path, and cleans up afterward.
func withTempGitDir(fn func(tmpDir string) error) error {
	tmpDir, err := os.MkdirTemp("", "yoloai-apply-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck // best-effort cleanup

	if err := RunGitCmd(tmpDir, "init"); err != nil {
		return fmt.Errorf("git init temp dir: %w", err)
	}

	return fn(tmpDir)
}

// runGitApply executes `git apply` with the given args, feeding the
// patch via stdin. Returns the combined output on error.
func runGitApply(dir string, patch []byte, args ...string) error {
	applyArgs := append([]string{"apply"}, args...)
	cmd := NewGitCmd(dir, applyArgs...)
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

// formatAMError wraps a git am failure with actionable guidance.
func formatAMError(output []byte, targetDir string) error {
	msg := strings.TrimSpace(string(output))
	return fmt.Errorf("git am failed in %s:\n%s\n\nTo resolve:\n"+
		"  cd %s\n"+
		"  # fix conflicts, then: git am --continue\n"+
		"  # or skip this commit: git am --skip\n"+
		"  # or abort:            git am --abort",
		targetDir, msg, targetDir)
}
