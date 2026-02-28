package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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

	// Resolve symlinks so git apply doesn't reject the path with
	// "beyond a symbolic link" (e.g. macOS /var → /private/var).
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

// CommitInfo holds a commit SHA and its subject line.
type CommitInfo struct {
	SHA     string
	Subject string
}

// ListCommitsBeyondBaseline returns the commits made in the work copy
// after the baseline commit, in chronological order (oldest first).
// Returns an empty slice if HEAD == baseline.
func ListCommitsBeyondBaseline(name string) ([]CommitInfo, error) {
	workDir, baselineSHA, mode, err := loadDiffContext(name)
	if err != nil {
		return nil, err
	}

	if mode == "rw" {
		return nil, fmt.Errorf("commit listing is not available for :rw directories")
	}

	cmd := newGitCmd(workDir, "log", "--reverse", "--format=%H %s", baselineSHA+"..HEAD")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	lines := strings.TrimSpace(string(output))
	if lines == "" {
		return nil, nil
	}

	var commits []CommitInfo
	for _, line := range strings.Split(lines, "\n") {
		sha, subject, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		commits = append(commits, CommitInfo{SHA: sha, Subject: subject})
	}

	return commits, nil
}

// HasUncommittedChanges checks whether the work copy has uncommitted
// changes (staged or unstaged, including untracked files).
func HasUncommittedChanges(name string) (bool, error) {
	workDir, _, mode, err := loadDiffContext(name)
	if err != nil {
		return false, err
	}

	if mode == "rw" {
		return false, fmt.Errorf("uncommitted-change check is not available for :rw directories")
	}

	if err := stageUntracked(workDir); err != nil {
		return false, err
	}

	cmd := newGitCmd(workDir, "diff", "--quiet", "HEAD")
	err = cmd.Run()
	if err == nil {
		return false, nil // exit 0 = clean
	}

	// git diff --quiet exits 1 when there are differences
	var exitErr *exec.ExitError
	if ok := errorAs(err, &exitErr); ok && exitErr.ExitCode() == 1 {
		return true, nil
	}

	return false, fmt.Errorf("git diff --quiet: %w", err)
}

// errorAs is a helper wrapping errors.As for exec.ExitError.
func errorAs(err error, target **exec.ExitError) bool {
	return errorsAs(err, target)
}

// errorsAs is a package-level variable to allow the errors.As call.
// This exists because importing "errors" directly would shadow the
// fmt.Errorf error wrapping used elsewhere in this file.
var errorsAs = func(err error, target any) bool {
	switch t := target.(type) {
	case **exec.ExitError:
		e, ok := err.(*exec.ExitError) //nolint:errorlint // need concrete type
		if ok {
			*t = e
		}
		return ok
	default:
		return false
	}
}

// ResolveRef resolves a short SHA prefix to a full 40-char SHA among
// commits beyond the baseline. Returns an error if the ref is ambiguous
// (matches multiple commits) or not found.
func ResolveRef(name, ref string) (CommitInfo, error) {
	commits, err := ListCommitsBeyondBaseline(name)
	if err != nil {
		return CommitInfo{}, err
	}

	ref = strings.ToLower(ref)
	var matches []CommitInfo
	for _, c := range commits {
		if strings.HasPrefix(strings.ToLower(c.SHA), ref) {
			matches = append(matches, c)
		}
	}

	switch len(matches) {
	case 0:
		return CommitInfo{}, fmt.Errorf("ref %q not found among sandbox commits", ref)
	case 1:
		return matches[0], nil
	default:
		return CommitInfo{}, fmt.Errorf("ref %q is ambiguous — matches %d commits", ref, len(matches))
	}
}

// ResolveRefs resolves a list of ref strings (short SHAs or "sha..sha" ranges)
// to an ordered list of CommitInfo. For ranges, all commits between the two
// endpoints (inclusive of end, exclusive of start) are included.
// The returned list preserves chronological order within the sandbox.
func ResolveRefs(name string, refs []string) ([]CommitInfo, error) {
	allCommits, err := ListCommitsBeyondBaseline(name)
	if err != nil {
		return nil, err
	}

	// Build SHA index for fast lookup
	shaIndex := make(map[string]int) // full SHA → index in allCommits
	for i, c := range allCommits {
		shaIndex[strings.ToLower(c.SHA)] = i
	}

	// Resolve short SHA to full
	resolve := func(ref string) (string, error) {
		ref = strings.ToLower(ref)
		var found string
		for _, c := range allCommits {
			if strings.HasPrefix(strings.ToLower(c.SHA), ref) {
				if found != "" {
					return "", fmt.Errorf("ref %q is ambiguous — matches multiple commits", ref)
				}
				found = strings.ToLower(c.SHA)
			}
		}
		if found == "" {
			return "", fmt.Errorf("ref %q not found among sandbox commits", ref)
		}
		return found, nil
	}

	selected := make(map[string]bool) // full SHA (lowered) → true
	for _, ref := range refs {
		if before, after, isRange := strings.Cut(ref, ".."); isRange {
			startSHA, err := resolve(before)
			if err != nil {
				return nil, err
			}
			endSHA, err := resolve(after)
			if err != nil {
				return nil, err
			}
			startIdx, endIdx := shaIndex[startSHA], shaIndex[endSHA]
			if startIdx > endIdx {
				return nil, fmt.Errorf("invalid range: %s is after %s", before, after)
			}
			// Range is exclusive of start, inclusive of end (git convention)
			for i := startIdx + 1; i <= endIdx; i++ {
				selected[strings.ToLower(allCommits[i].SHA)] = true
			}
		} else {
			fullSHA, err := resolve(ref)
			if err != nil {
				return nil, err
			}
			selected[fullSHA] = true
		}
	}

	// Return in chronological order
	var result []CommitInfo
	for _, c := range allCommits {
		if selected[strings.ToLower(c.SHA)] {
			result = append(result, c)
		}
	}

	return result, nil
}

// GenerateFormatPatchForRefs creates .patch files for specific commits (by SHA)
// within the sandbox work copy. Returns the temp directory and sorted file list.
// The caller is responsible for os.RemoveAll(patchDir).
func GenerateFormatPatchForRefs(name string, shas []string) (patchDir string, files []string, err error) {
	workDir, _, mode, loadErr := loadDiffContext(name)
	if loadErr != nil {
		return "", nil, loadErr
	}

	if mode == "rw" {
		return "", nil, fmt.Errorf("format-patch is not available for :rw directories")
	}

	patchDir, err = os.MkdirTemp("", "yoloai-format-patch-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}

	for _, sha := range shas {
		cmd := newGitCmd(workDir, "format-patch", "-1", "--output-directory="+patchDir, sha)
		if output, runErr := cmd.CombinedOutput(); runErr != nil {
			os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
			return "", nil, fmt.Errorf("git format-patch -1 %s: %s: %w", sha, strings.TrimSpace(string(output)), runErr)
		}
	}

	// Read and sort patch files
	entries, err := os.ReadDir(patchDir)
	if err != nil {
		os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
		return "", nil, fmt.Errorf("read patch dir: %w", err)
	}

	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".patch") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	return patchDir, files, nil
}

// AdvanceBaselineTo updates the sandbox's baseline SHA to the given commit.
// Unlike AdvanceBaseline (which advances to HEAD), this advances to an
// arbitrary commit — used after selective apply.
func AdvanceBaselineTo(name, sha string) error {
	sandboxDir, err := RequireSandboxDir(name)
	if err != nil {
		return err
	}

	meta, err := LoadMeta(sandboxDir)
	if err != nil {
		return err
	}

	if meta.Workdir.Mode == "rw" {
		return nil
	}

	meta.Workdir.BaselineSHA = sha
	return SaveMeta(sandboxDir, meta)
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

// AdvanceBaseline updates the sandbox's baseline SHA to the current HEAD
// of its work copy. This should be called after a successful apply so that
// subsequent diff/apply operations don't re-show already-applied commits.
// For :rw mode sandboxes, this is a no-op.
func AdvanceBaseline(name string) error {
	sandboxDir, err := RequireSandboxDir(name)
	if err != nil {
		return err
	}

	meta, err := LoadMeta(sandboxDir)
	if err != nil {
		return err
	}

	if meta.Workdir.Mode == "rw" {
		return nil
	}

	workDir := WorkDir(name, meta.Workdir.HostPath)
	sha, err := gitHeadSHA(workDir)
	if err != nil {
		return err
	}

	meta.Workdir.BaselineSHA = sha
	return SaveMeta(sandboxDir, meta)
}

// GenerateFormatPatch creates .patch files (one per commit) for commits
// beyond the baseline. Returns the temp directory path and sorted list
// of .patch filenames. The caller is responsible for os.RemoveAll(patchDir).
// When paths is non-empty, only commits touching those paths are included.
func GenerateFormatPatch(name string, paths []string) (patchDir string, files []string, err error) {
	workDir, baselineSHA, mode, loadErr := loadDiffContext(name)
	if loadErr != nil {
		return "", nil, loadErr
	}

	if mode == "rw" {
		return "", nil, fmt.Errorf("format-patch is not available for :rw directories")
	}

	patchDir, err = os.MkdirTemp("", "yoloai-format-patch-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}

	if len(paths) == 0 {
		// All commits
		cmd := newGitCmd(workDir, "format-patch", "--output-directory="+patchDir, baselineSHA+"..HEAD")
		if output, runErr := cmd.CombinedOutput(); runErr != nil {
			os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
			return "", nil, fmt.Errorf("git format-patch: %s: %w", strings.TrimSpace(string(output)), runErr)
		}
	} else {
		// Only commits touching specified paths
		revArgs := []string{"rev-list", "--reverse", baselineSHA + "..HEAD", "--"}
		revArgs = append(revArgs, paths...)
		revCmd := newGitCmd(workDir, revArgs...)
		revOut, revErr := revCmd.Output()
		if revErr != nil {
			os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
			return "", nil, fmt.Errorf("git rev-list: %w", revErr)
		}

		shas := strings.Fields(strings.TrimSpace(string(revOut)))
		for _, sha := range shas {
			cmd := newGitCmd(workDir, "format-patch", "-1", "--output-directory="+patchDir, sha)
			if output, runErr := cmd.CombinedOutput(); runErr != nil {
				os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
				return "", nil, fmt.Errorf("git format-patch -1 %s: %s: %w", sha, strings.TrimSpace(string(output)), runErr)
			}
		}
	}

	// Read and sort patch files
	entries, err := os.ReadDir(patchDir)
	if err != nil {
		os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
		return "", nil, fmt.Errorf("read patch dir: %w", err)
	}

	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".patch") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	return patchDir, files, nil
}

// GenerateWIPDiff produces a binary patch of uncommitted changes (against
// HEAD, not the baseline). This captures only work-in-progress changes
// that the agent hasn't committed. Returns empty patch if no uncommitted changes.
func GenerateWIPDiff(name string, paths []string) (patch []byte, stat string, err error) {
	workDir, _, mode, loadErr := loadDiffContext(name)
	if loadErr != nil {
		return nil, "", loadErr
	}

	if mode == "rw" {
		return nil, "", fmt.Errorf("WIP diff is not available for :rw directories")
	}

	if err := stageUntracked(workDir); err != nil {
		return nil, "", err
	}

	// Generate binary patch against HEAD
	patchArgs := []string{"diff", "--binary", "HEAD"}
	if len(paths) > 0 {
		patchArgs = append(patchArgs, "--")
		patchArgs = append(patchArgs, paths...)
	}

	patchCmd := newGitCmd(workDir, patchArgs...)
	patchOut, err := patchCmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("git diff (WIP patch): %w", err)
	}

	if len(patchOut) == 0 {
		return nil, "", nil
	}

	// Generate stat summary against HEAD
	statArgs := []string{"diff", "--stat", "HEAD"}
	if len(paths) > 0 {
		statArgs = append(statArgs, "--")
		statArgs = append(statArgs, paths...)
	}

	statCmd := newGitCmd(workDir, statArgs...)
	statOut, err := statCmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("git diff (WIP stat): %w", err)
	}

	return patchOut, strings.TrimRight(string(statOut), "\n"), nil
}

// PatchSet holds patch data for a single directory.
type PatchSet struct {
	HostPath string // original host path (for display)
	Mode     string // "copy"
	Patch    []byte
	Stat     string
}

// GenerateMultiPatch produces patches for all :copy directories.
// :rw dirs are skipped (changes are already live).
func GenerateMultiPatch(name string, paths []string) ([]PatchSet, error) {
	contexts, err := LoadAllDiffContexts(name)
	if err != nil {
		return nil, err
	}

	var patches []PatchSet
	for _, dc := range contexts {
		if dc.Mode != "copy" {
			continue
		}

		if err := stageUntracked(dc.WorkDir); err != nil {
			return nil, fmt.Errorf("stage untracked in %s: %w", dc.HostPath, err)
		}

		patchArgs := []string{"diff", "--binary", dc.BaselineSHA}
		if len(paths) > 0 {
			patchArgs = append(patchArgs, "--")
			patchArgs = append(patchArgs, paths...)
		}
		patchCmd := newGitCmd(dc.WorkDir, patchArgs...)
		patchOut, err := patchCmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git diff (patch) in %s: %w", dc.HostPath, err)
		}

		if len(patchOut) == 0 {
			continue
		}

		statArgs := []string{"diff", "--stat", dc.BaselineSHA}
		if len(paths) > 0 {
			statArgs = append(statArgs, "--")
			statArgs = append(statArgs, paths...)
		}
		statCmd := newGitCmd(dc.WorkDir, statArgs...)
		statOut, err := statCmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git diff (stat) in %s: %w", dc.HostPath, err)
		}

		patches = append(patches, PatchSet{
			HostPath: dc.HostPath,
			Mode:     dc.Mode,
			Patch:    patchOut,
			Stat:     strings.TrimRight(string(statOut), "\n"),
		})
	}

	return patches, nil
}

// ApplyFormatPatch applies .patch files to a target git directory using
// git am --3way. On failure, returns an error with guidance about
// git am --continue/--skip/--abort.
func ApplyFormatPatch(patchDir string, files []string, targetDir string) error {
	if len(files) == 0 {
		return nil
	}

	// Build full paths
	fullPaths := make([]string, len(files))
	for i, f := range files {
		fullPaths[i] = filepath.Join(patchDir, f)
	}

	args := append([]string{"am", "--3way"}, fullPaths...)
	cmd := newGitCmd(targetDir, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return formatAMError(err, output, targetDir)
	}

	return nil
}

// formatAMError wraps a git am failure with actionable guidance.
func formatAMError(_ error, output []byte, targetDir string) error {
	msg := strings.TrimSpace(string(output))
	return fmt.Errorf("git am failed in %s:\n%s\n\nTo resolve:\n"+
		"  cd %s\n"+
		"  # fix conflicts, then: git am --continue\n"+
		"  # or skip this commit: git am --skip\n"+
		"  # or abort:            git am --abort",
		targetDir, msg, targetDir)
}
