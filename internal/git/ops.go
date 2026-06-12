// ABOUTME: Git operations ported from workspace: Baseline, HeadSHA, diffs,
// ABOUTME: CheckPatch/ApplyPatch/ApplyFormatPatch, CheckDirtyRepo, and helpers.
package git

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// ─── high-level ops ──────────────────────────────────────────────────────────

// HeadSHA returns the HEAD commit SHA for the git repo at dir.
func (g *Git) HeadSHA(ctx context.Context, dir string) (string, error) {
	out, err := g.Run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// IsEmptyRepo reports whether dir is a git repository with no commits yet.
func (g *Git) IsEmptyRepo(ctx context.Context, dir string) bool {
	_, err := g.Run(ctx, dir, "rev-parse", "--verify", "HEAD")
	return err != nil
}

// Baseline creates a fresh git baseline for the work copy.
// Assumes all .git entries have already been removed by RemoveGitDirs.
func (g *Git) Baseline(ctx context.Context, workDir string) (string, error) {
	cmds := [][]string{
		{"init"},
		{"config", "user.email", "yoloai@localhost"},
		{"config", "user.name", "yoloai"},
		{"add", "-A"},
		{"commit", "-m", "yoloai baseline", "--allow-empty"},
	}
	for _, args := range cmds {
		if err := g.RunCmd(ctx, workDir, args...); err != nil {
			return "", err
		}
	}
	if err := chownGitDir(workDir); err != nil {
		return "", err
	}
	return g.HeadSHA(ctx, workDir)
}

// BaselineUncommittedChanges commits any pre-existing uncommitted changes in
// workDir as "yoloai: pre-session state".
func (g *Git) BaselineUncommittedChanges(ctx context.Context, workDir string) (string, error) {
	out, err := g.Run(ctx, workDir, "status", "--porcelain")
	if err != nil || len(strings.TrimSpace(out)) == 0 {
		return g.HeadSHA(ctx, workDir)
	}
	if err := g.RunCmd(ctx, workDir, "add", "-A"); err != nil {
		return "", fmt.Errorf("stage pre-session changes: %w", err)
	}
	if err := g.RunCmd(ctx, workDir,
		"-c", "user.email=yoloai@localhost",
		"-c", "user.name=yoloai",
		"commit", "-m", "yoloai: pre-session state",
	); err != nil {
		return "", fmt.Errorf("commit pre-session state: %w", err)
	}
	if err := chownGitDir(workDir); err != nil {
		return "", err
	}
	return g.HeadSHA(ctx, workDir)
}

// StageUntracked runs `git add -A` in the work directory to capture files
// created by the agent that are not yet tracked. Retries on index.lock
// contention (the in-container agent's git can briefly hold the lock).
func (g *Git) StageUntracked(ctx context.Context, workDir string) error {
	var err error
	for range 5 {
		err = g.RunCmd(ctx, workDir, "add", "-A")
		if err == nil || !IsIndexLocked(err) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

// ─── diff ops ────────────────────────────────────────────────────────────────

// CopyDiff generates a diff for a :copy mode work directory against a baseline
// SHA. Stages untracked files first, then runs git diff.
// Returns the diff text (empty string if there are no changes).
func (g *Git) CopyDiff(ctx context.Context, workDir, baselineSHA string, paths []string, stat, nameOnly bool, pathPrefix string) (string, error) {
	if err := g.StageUntracked(ctx, workDir); err != nil {
		return "", err
	}

	args := []string{"diff"}
	switch {
	case nameOnly:
		args = append(args, "--name-only")
	case stat:
		args = append(args, "--stat")
	default:
		args = append(args, "--binary")
		if pathPrefix != "" {
			args = append(args, "--src-prefix="+pathPrefix, "--dst-prefix="+pathPrefix)
		}
	}
	args = append(args, baselineSHA)
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}

	out, err := g.Run(ctx, workDir, args...)
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return strings.TrimRight(out, "\n"), nil
}

// RWDiff generates a diff for a :rw mode directory. Returns an empty string
// (no error) when the directory is not a git repo.
func (g *Git) RWDiff(ctx context.Context, workDir string, paths []string, stat, nameOnly bool) (string, error) {
	if !IsGitRepo(workDir) {
		return "", nil
	}

	args := []string{"diff"}
	switch {
	case nameOnly:
		args = append(args, "--name-only")
	case stat:
		args = append(args, "--stat")
	}
	args = append(args, "HEAD")
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}

	out, err := g.Run(ctx, workDir, args...)
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return strings.TrimRight(out, "\n"), nil
}

// ─── apply ops ───────────────────────────────────────────────────────────────

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
func (g *Git) CheckPatch(ctx context.Context, patch []byte, targetDir string, isGit bool) error {
	if isGit {
		if err := g.runGitApply(ctx, targetDir, patch, "--check"); err != nil {
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

	// --unsafe-paths invariant: see ApplyPatch. Patches must originate from
	// our own git diff of the target tree, never an external raw patch.
	return g.withTempGitDir(ctx, func(tmpDir string) error {
		if err := g.runGitApply(ctx, tmpDir, patch, "--check", "--unsafe-paths", "--directory="+realTarget); err != nil {
			return formatApplyError(err, targetDir)
		}
		return nil
	})
}

// ApplyPatch applies the patch to the target directory.
// For git repos: runs `git apply` from within the repo.
// For non-git dirs: runs `git apply --unsafe-paths --directory=<path>`
// from a temporary git-initialized directory.
func (g *Git) ApplyPatch(ctx context.Context, patch []byte, targetDir string, isGit bool) error {
	if isGit {
		if err := g.runGitApply(ctx, targetDir, patch); err != nil {
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

	// --unsafe-paths lets git apply write to a target that isn't a git
	// worktree (these :copy dirs aren't repos) AND disables git's
	// reject-outside-the-tree check. That is only safe because every patch
	// reaching here is produced by our own `git diff`/`format-patch` over
	// the target tree, so it can only name repo-relative paths — never a
	// caller- or agent-supplied raw patch that could carry `../` traversal.
	// Do not route externally-sourced patches through this path without
	// adding containment. git's separate "beyond a symbolic link" check
	// still fires and blocks the create-symlink-then-write-through escape.
	return g.withTempGitDir(ctx, func(tmpDir string) error {
		if err := g.runGitApply(ctx, tmpDir, patch, "--unsafe-paths", "--directory="+realTarget); err != nil {
			return formatApplyError(err, targetDir)
		}
		return nil
	})
}

// ApplyFormatPatch applies .patch files to a target git directory using
// git am --3way. Returns a map of sandbox SHA → host SHA for applied commits,
// which callers can use to re-create tags on the host.
func (g *Git) ApplyFormatPatch(ctx context.Context, patchDir string, files []string, targetDir string) (map[string]string, error) {
	if len(files) == 0 {
		return nil, nil
	}

	// Record HEAD before applying so we can identify new commits afterward.
	// Empty repos have no HEAD yet; preTip="" is handled below when building
	// the git log range after git am completes.
	preTip, err := g.HeadSHA(ctx, targetDir)
	if err != nil {
		// Only tolerate the empty-repo case (no commits → exit 128).
		// Any other failure means the repo is in an unexpected state.
		if !g.IsEmptyRepo(ctx, targetDir) {
			return nil, err
		}
		preTip = ""
	}

	// Extract sandbox SHAs from patch file headers (first line: "From <sha> <date>").
	sandboxSHAs, err := extractSandboxSHAs(patchDir, files)
	if err != nil {
		return nil, err
	}

	// Build full paths.
	fullPaths := make([]string, len(files))
	for i, f := range files {
		fullPaths[i] = filepath.Join(patchDir, f)
	}

	// Stash uncommitted changes so git am starts from a clean tree.
	// git am --autostash requires Git 2.27+ which isn't guaranteed on macOS;
	// manage the stash manually so any git version works.
	var stashed bool
	if preTip != "" {
		stashed, err = g.stashIfDirty(ctx, targetDir)
		if err != nil {
			return nil, fmt.Errorf("stash uncommitted changes: %w", err)
		}
	}

	amArgs := append([]string{"am", "--3way"}, fullPaths...)
	stdout, amErr := g.Run(ctx, targetDir, amArgs...)
	if amErr != nil {
		// am failed — abort cleanly, then restore the stash so the user
		// is back to their original state and can retry.
		_ = g.RunCmd(ctx, targetDir, "am", "--abort")
		if stashed {
			_ = g.RunCmd(ctx, targetDir, "stash", "pop")
		}
		// Collect stderr from ExecError for the human-readable message.
		var stderr string
		var ee *runtime.ExecError
		if errors.As(amErr, &ee) {
			stderr = ee.Stderr
		}
		// Combine stdout and stderr for the error message (matches old CombinedOutput behaviour).
		combined := strings.TrimSpace(stdout + "\n" + stderr)
		return nil, formatAMError([]byte(combined), targetDir)
	}

	// Pair positionally: sandboxSHA[i] → hostSHA[i].
	shaMap := g.buildSHAMap(ctx, targetDir, preTip, sandboxSHAs)

	if stashed {
		if err := g.RunCmd(ctx, targetDir, "stash", "pop"); err != nil {
			// Commits were applied successfully; return shaMap so callers can
			// advance the baseline before surfacing the stash conflict to the user.
			return shaMap, fmt.Errorf("restore stashed changes after apply: %w (your commits were applied; run 'git stash pop' in %s to restore pre-session changes)", err, targetDir)
		}
	}
	return shaMap, nil
}

// ─── safety ops ──────────────────────────────────────────────────────────────

// CheckDirtyRepo checks if the given path is a git repository with
// uncommitted changes. Returns a human-readable warning string if
// dirty, empty string if clean or not a git repo.
func (g *Git) CheckDirtyRepo(ctx context.Context, path string) (string, error) {
	gitDir := filepath.Join(path, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return "", nil //nolint:nilerr // not a git repo; absence of .git is not an error
	}

	out, err := g.Run(ctx, path, "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("git status in %q: %w", path, err)
	}

	if len(out) == 0 {
		return "", nil // clean repo
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	modified := 0
	untracked := 0
	for _, line := range lines {
		// Git status --porcelain format: "XY filename" where XY is a 2-char status code
		if len(line) < 3 {
			continue
		}
		filename := line[3:]

		// Skip yoloai-generated bugreport files (both .md and .md.tmp)
		if isBugreportFile(filepath.Base(filename)) {
			continue
		}

		if strings.HasPrefix(line, "??") {
			untracked++
		} else {
			modified++
		}
	}

	var parts []string
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("%d files modified", modified))
	}
	if untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", untracked))
	}

	return strings.Join(parts, ", "), nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// IsGitRepo checks if a directory is a git repository by looking for .git/.
func IsGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// IsIndexLocked reports whether err is a git index.lock contention error.
func IsIndexLocked(err error) bool {
	return err != nil && strings.Contains(err.Error(), "index.lock")
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

// withTempGitDir creates a temporary git-initialized directory, calls fn
// with its path, and cleans up afterward.
func (g *Git) withTempGitDir(ctx context.Context, fn func(tmpDir string) error) error {
	tmpDir, err := os.MkdirTemp("", "yoloai-apply-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck // best-effort cleanup

	if err := g.RunCmd(ctx, tmpDir, "init"); err != nil {
		return fmt.Errorf("git init temp dir: %w", err)
	}

	return fn(tmpDir)
}

// runGitApply executes `git apply` with the given args, feeding the patch via
// stdin. Returns error with stderr content included on failure.
func (g *Git) runGitApply(ctx context.Context, dir string, patch []byte, args ...string) error {
	applyArgs := append([]string{"apply"}, args...)
	stdout, err := g.RunInput(ctx, dir, patch, applyArgs...)
	if err != nil {
		var ee *runtime.ExecError
		if errors.As(err, &ee) {
			// git apply writes diagnostics to stderr; include it (and any stdout) in the error.
			msg := strings.TrimSpace(ee.Stderr)
			if stdout != "" {
				msg = strings.TrimSpace(stdout + "\n" + ee.Stderr)
			}
			if msg != "" {
				return fmt.Errorf("%s: %w", msg, err)
			}
		}
		return err
	}
	return nil
}

// stashIfDirty runs `git stash push --include-untracked` if the working tree
// has uncommitted changes. Returns true if a stash was created.
func (g *Git) stashIfDirty(ctx context.Context, dir string) (bool, error) {
	out, err := g.Run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	if len(strings.TrimSpace(out)) == 0 {
		return false, nil
	}
	stdout, err := g.Run(ctx, dir, "stash", "push", "--include-untracked", "--message", "yoloai pre-apply")
	if err != nil {
		var ee *runtime.ExecError
		if errors.As(err, &ee) {
			msg := strings.TrimSpace(stdout + "\n" + ee.Stderr)
			if msg != "" {
				return false, fmt.Errorf("%s: %w", msg, err)
			}
		}
		return false, err
	}
	return true, nil
}

// buildSHAMap pairs sandbox SHAs with the host SHAs created after applying patches.
func (g *Git) buildSHAMap(ctx context.Context, targetDir, preTip string, sandboxSHAs []string) map[string]string {
	logArgs := []string{"log", "--reverse", "--format=%H"}
	if preTip != "" {
		logArgs = append(logArgs, preTip+"..HEAD")
	}
	logOut, logErr := g.Run(ctx, targetDir, logArgs...)
	if logErr != nil {
		return nil
	}
	hostSHAs := strings.Fields(strings.TrimSpace(logOut))
	shaMap := make(map[string]string, len(sandboxSHAs))
	for i, sandboxSHA := range sandboxSHAs {
		if i < len(hostSHAs) {
			shaMap[strings.ToLower(sandboxSHA)] = hostSHAs[i]
		}
	}
	return shaMap
}

// extractSandboxSHAs reads the sandbox-side commit SHAs from patch file headers.
func extractSandboxSHAs(patchDir string, files []string) ([]string, error) {
	sandboxSHAs := make([]string, 0, len(files))
	for _, f := range files {
		sha, parseErr := parsePatchSHA(filepath.Join(patchDir, f))
		if parseErr != nil {
			return nil, parseErr
		}
		sandboxSHAs = append(sandboxSHAs, sha)
	}
	return sandboxSHAs, nil
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

// isBugreportFile returns true for yoloai-generated bugreport filenames.
// Defined here (not imported) to avoid a cycle back into workspace.
func isBugreportFile(name string) bool {
	return strings.HasPrefix(name, "yoloai-bugreport-") &&
		(strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".md.tmp"))
}
