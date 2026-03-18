package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/workspace"
)

// ApplyResult describes the outcome of applying a sandbox's changes.
type ApplyResult struct {
	// Dir is the host directory that was patched.
	Dir string
	// FilesChanged is the number of files modified.
	FilesChanged int
	// Stat is the human-readable diff stat summary.
	Stat string
}

// ApplyAll applies all pending changes from the sandbox's :copy directories
// back to their original host paths. It is the programmatic equivalent of
// 'yoloai apply <name>'.
//
// Returns ErrNoChanges if there are no patches to apply.
// Returns an ApplyResult for each directory patched.
func ApplyAll(_ context.Context, name string) ([]*ApplyResult, error) {
	patches, err := GenerateMultiPatch(name, nil)
	if err != nil {
		return nil, err
	}
	if len(patches) == 0 {
		return nil, ErrNoChanges
	}

	var results []*ApplyResult
	for _, ps := range patches {
		isGit := workspace.IsGitRepo(ps.HostPath)
		if err := workspace.ApplyPatch(ps.Patch, ps.HostPath, isGit); err != nil {
			return nil, fmt.Errorf("%s: %w", ps.HostPath, err)
		}
		results = append(results, &ApplyResult{
			Dir:  ps.HostPath,
			Stat: ps.Stat,
		})
	}

	if err := AdvanceBaseline(name); err != nil {
		return nil, fmt.Errorf("advance baseline: %w", err)
	}

	return results, nil
}

// CommitInfo is an alias for workspace.CommitInfo.
type CommitInfo = workspace.CommitInfo

// PatchSet is an alias for workspace.PatchSet.
type PatchSet = workspace.PatchSet

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

	if mode == "overlay" {
		return nil, "", fmt.Errorf("use GenerateOverlayPatch for :overlay directories")
	}

	if err := workspace.StageUntracked(workDir); err != nil {
		return nil, "", err
	}

	// Generate binary patch
	patchArgs := []string{"diff", "--binary", baselineSHA}
	if len(paths) > 0 {
		patchArgs = append(patchArgs, "--")
		patchArgs = append(patchArgs, paths...)
	}

	patchCmd := workspace.NewGitCmd(workDir, patchArgs...)
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

	statCmd := workspace.NewGitCmd(workDir, statArgs...)
	statOut, err := statCmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("git diff (stat): %w", err)
	}

	return patchOut, strings.TrimRight(string(statOut), "\n"), nil
}

// updateOverlayBaseline updates the baseline SHA for an overlay directory in meta.json.
func updateOverlayBaseline(name, hostPath, sha string) error {
	sandboxDir, err := RequireSandboxDir(name)
	if err != nil {
		return err
	}

	meta, err := LoadMeta(sandboxDir)
	if err != nil {
		return err
	}

	// Update workdir or aux dir
	if meta.Workdir.HostPath == hostPath {
		meta.Workdir.BaselineSHA = sha
	} else {
		found := false
		for i := range meta.Directories {
			if meta.Directories[i].HostPath == hostPath {
				meta.Directories[i].BaselineSHA = sha
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("directory %s not found in sandbox metadata", hostPath)
		}
	}

	return SaveMeta(sandboxDir, meta)
}

// GenerateOverlayPatch produces a binary patch for overlay-mode directories
// by executing git commands inside the running container.
func GenerateOverlayPatch(ctx context.Context, rt runtime.Runtime, name string, paths []string) ([]PatchSet, error) {
	meta, err := LoadMeta(Dir(name))
	if err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}
	contexts, err := LoadAllDiffContexts(name)
	if err != nil {
		return nil, err
	}

	var patches []PatchSet

	for _, dc := range contexts {
		if dc.Mode != "overlay" {
			continue
		}

		// Resolve baseline SHA if deferred
		baselineSHA := dc.BaselineSHA
		if baselineSHA == "" {
			stdout, err := execInContainer(ctx, rt, name, meta, []string{
				"git", "-C", dc.WorkDir, "rev-parse", "HEAD",
			})
			if err != nil {
				return nil, fmt.Errorf("resolve baseline SHA for %s: %w", dc.HostPath, err)
			}
			baselineSHA = strings.TrimSpace(stdout)
			if updateErr := updateOverlayBaseline(name, dc.HostPath, baselineSHA); updateErr != nil {
				return nil, updateErr
			}
		}

		// Stage untracked files
		_, err := execInContainer(ctx, rt, name, meta, []string{
			"git", "-C", dc.WorkDir, "add", "-A",
		})
		if err != nil {
			return nil, fmt.Errorf("stage untracked in %s: %w", dc.HostPath, err)
		}

		// Generate binary patch
		patchArgs := []string{"git", "-c", "core.hooksPath=/dev/null", "-C", dc.WorkDir, "diff", "--binary", baselineSHA}
		if len(paths) > 0 {
			patchArgs = append(patchArgs, "--")
			patchArgs = append(patchArgs, paths...)
		}
		stdout, err := execInContainer(ctx, rt, name, meta, patchArgs)
		if err != nil {
			return nil, fmt.Errorf("git diff (patch) in %s: %w", dc.HostPath, err)
		}

		if len(stdout) == 0 {
			continue
		}

		// Generate stat summary
		statArgs := []string{"git", "-c", "core.hooksPath=/dev/null", "-C", dc.WorkDir, "diff", "--stat", baselineSHA}
		if len(paths) > 0 {
			statArgs = append(statArgs, "--")
			statArgs = append(statArgs, paths...)
		}
		statOut, err := execInContainer(ctx, rt, name, meta, statArgs)
		if err != nil {
			return nil, fmt.Errorf("git diff (stat) in %s: %w", dc.HostPath, err)
		}

		patches = append(patches, PatchSet{
			HostPath: dc.HostPath,
			Mode:     "overlay",
			Patch:    []byte(stdout),
			Stat:     strings.TrimRight(statOut, "\n"),
		})
	}

	return patches, nil
}

// UpdateOverlayBaselineToHEAD advances the overlay baseline for a directory
// to the current HEAD inside the running container. Called after a successful
// overlay apply to prevent re-applying already-applied changes.
func UpdateOverlayBaselineToHEAD(ctx context.Context, rt runtime.Runtime, name, hostPath string) error {
	meta, err := LoadMeta(Dir(name))
	if err != nil {
		return fmt.Errorf("load metadata: %w", err)
	}
	contexts, err := LoadAllDiffContexts(name)
	if err != nil {
		return err
	}

	for _, dc := range contexts {
		if dc.Mode != "overlay" || dc.HostPath != hostPath {
			continue
		}
		stdout, err := execInContainer(ctx, rt, name, meta, []string{
			"git", "-C", dc.WorkDir, "rev-parse", "HEAD",
		})
		if err != nil {
			return fmt.Errorf("get HEAD for %s: %w", hostPath, err)
		}
		return updateOverlayBaseline(name, hostPath, strings.TrimSpace(stdout))
	}

	return nil
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

	if mode == "overlay" {
		return nil, fmt.Errorf("commit listing for :overlay directories requires container exec")
	}

	cmd := workspace.NewGitCmd(workDir, "log", "--reverse", "--format=%H %s", baselineSHA+"..HEAD")
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

	if mode == "overlay" {
		return false, fmt.Errorf("uncommitted-change check for :overlay directories requires container exec")
	}

	if err := workspace.StageUntracked(workDir); err != nil {
		return false, err
	}

	cmd := workspace.NewGitCmd(workDir, "diff", "--quiet", "HEAD")
	err = cmd.Run()
	if err == nil {
		return false, nil // exit 0 = clean
	}

	// git diff --quiet exits 1 when there are differences
	var exitErr *exec.ExitError
	if ok := errors.As(err, &exitErr); ok && exitErr.ExitCode() == 1 {
		return true, nil
	}

	return false, fmt.Errorf("git diff --quiet: %w", err)
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
	shaIndex := make(map[string]int) // full SHA -> index in allCommits
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

	selected := make(map[string]bool) // full SHA (lowered) -> true
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
// within the sandbox work copy. When paths is non-empty, patches are filtered
// to only include changes in those paths. Returns the temp directory and sorted
// file list. The caller is responsible for os.RemoveAll(patchDir).
func GenerateFormatPatchForRefs(name string, shas, paths []string) (patchDir string, files []string, err error) {
	workDir, _, mode, loadErr := loadDiffContext(name)
	if loadErr != nil {
		return "", nil, loadErr
	}

	if mode == "rw" {
		return "", nil, fmt.Errorf("format-patch is not available for :rw directories")
	}

	if mode == "overlay" {
		return "", nil, fmt.Errorf("format-patch for :overlay directories requires container exec")
	}

	patchDir, err = os.MkdirTemp("", "yoloai-format-patch-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}

	for _, sha := range shas {
		args := []string{"format-patch", "-1", "--output-directory=" + patchDir, sha}
		if len(paths) > 0 {
			args = append(args, "--")
			args = append(args, paths...)
		}
		cmd := workspace.NewGitCmd(workDir, args...)
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
// arbitrary commit -- used after selective apply.
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

	if meta.Workdir.Mode == "overlay" {
		return nil // baseline managed via updateOverlayBaseline
	}

	meta.Workdir.BaselineSHA = sha
	return SaveMeta(sandboxDir, meta)
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

	if meta.Workdir.Mode == "overlay" {
		return nil // baseline managed via updateOverlayBaseline
	}

	workDir := WorkDir(name, meta.Workdir.HostPath)
	sha, err := workspace.HeadSHA(workDir)
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

	if mode == "overlay" {
		return "", nil, fmt.Errorf("format-patch for :overlay directories requires container exec")
	}

	patchDir, err = os.MkdirTemp("", "yoloai-format-patch-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}

	if len(paths) == 0 {
		// All commits
		cmd := workspace.NewGitCmd(workDir, "format-patch", "--output-directory="+patchDir, baselineSHA+"..HEAD")
		if output, runErr := cmd.CombinedOutput(); runErr != nil {
			os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
			return "", nil, fmt.Errorf("git format-patch: %s: %w", strings.TrimSpace(string(output)), runErr)
		}
	} else {
		// Only commits touching specified paths
		revArgs := []string{"rev-list", "--reverse", baselineSHA + "..HEAD", "--"}
		revArgs = append(revArgs, paths...)
		revCmd := workspace.NewGitCmd(workDir, revArgs...)
		revOut, revErr := revCmd.Output()
		if revErr != nil {
			os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
			return "", nil, fmt.Errorf("git rev-list: %w", revErr)
		}

		shas := strings.Fields(strings.TrimSpace(string(revOut)))
		for _, sha := range shas {
			cmd := workspace.NewGitCmd(workDir, "format-patch", "-1", "--output-directory="+patchDir, sha)
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

	if mode == "overlay" {
		return nil, "", fmt.Errorf("WIP diff for :overlay directories requires container exec")
	}

	if err := workspace.StageUntracked(workDir); err != nil {
		return nil, "", err
	}

	// Generate binary patch against HEAD
	patchArgs := []string{"diff", "--binary", "HEAD"}
	if len(paths) > 0 {
		patchArgs = append(patchArgs, "--")
		patchArgs = append(patchArgs, paths...)
	}

	patchCmd := workspace.NewGitCmd(workDir, patchArgs...)
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

	statCmd := workspace.NewGitCmd(workDir, statArgs...)
	statOut, err := statCmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("git diff (WIP stat): %w", err)
	}

	return patchOut, strings.TrimRight(string(statOut), "\n"), nil
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

		if err := workspace.StageUntracked(dc.WorkDir); err != nil {
			return nil, fmt.Errorf("stage untracked in %s: %w", dc.HostPath, err)
		}

		patchArgs := []string{"diff", "--binary", dc.BaselineSHA}
		if len(paths) > 0 {
			patchArgs = append(patchArgs, "--")
			patchArgs = append(patchArgs, paths...)
		}
		patchCmd := workspace.NewGitCmd(dc.WorkDir, patchArgs...)
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
		statCmd := workspace.NewGitCmd(dc.WorkDir, statArgs...)
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
