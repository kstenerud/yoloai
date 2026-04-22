package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"
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
func ApplyAll(ctx context.Context, rt runtime.Runtime, name string) ([]*ApplyResult, error) {
	unlock, err := acquireLock(name)
	if err != nil {
		return nil, err
	}
	defer unlock()

	patches, err := GenerateMultiPatch(ctx, rt, name, nil)
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

	if err := AdvanceBaseline(ctx, rt, name); err != nil {
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
func GeneratePatch(ctx context.Context, rt runtime.Runtime, name string, paths []string) (patch []byte, stat string, err error) {
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

	// Stage untracked files
	_, err = rt.GitExec(ctx, name, workDir, "add", "-A")
	if err != nil {
		return nil, "", fmt.Errorf("git add: %w", err)
	}

	// Generate binary patch
	patchArgs := []string{"diff", "--binary", baselineSHA}
	if len(paths) > 0 {
		patchArgs = append(patchArgs, "--")
		patchArgs = append(patchArgs, paths...)
	}

	patchOut, err := rt.GitExec(ctx, name, workDir, patchArgs...)
	if err != nil {
		return nil, "", fmt.Errorf("git diff (patch): %w", err)
	}

	// Generate stat summary
	statArgs := []string{"diff", "--stat", baselineSHA}
	if len(paths) > 0 {
		statArgs = append(statArgs, "--")
		statArgs = append(statArgs, paths...)
	}

	statOut, err := rt.GitExec(ctx, name, workDir, statArgs...)
	if err != nil {
		return nil, "", fmt.Errorf("git diff (stat): %w", err)
	}

	return []byte(patchOut), strings.TrimRight(statOut, "\n"), nil
}

// ensureOverlayBaseline resolves or creates a git baseline for an overlay directory.
// If the overlay already has a valid HEAD commit, its SHA is returned. Otherwise
// (e.g. the entrypoint's chown broke git visibility through overlayfs), a fresh
// git repo is initialised inside the container and used as the baseline.
// The resolved SHA is persisted to meta.json so subsequent calls are a no-op.
func ensureOverlayBaseline(ctx context.Context, rt runtime.Runtime, name string, meta *Meta, dc DiffContext) (string, error) {
	if dc.BaselineSHA != "" {
		return dc.BaselineSHA, nil
	}

	// Try to resolve existing HEAD.
	stdout, err := execInContainer(ctx, rt, name, meta, []string{
		"git", "-C", dc.WorkDir, "rev-parse", "HEAD",
	})
	if err == nil {
		sha := strings.TrimSpace(stdout)
		if len(sha) == 40 {
			if updateErr := updateOverlayBaseline(name, dc.HostPath, sha); updateErr != nil {
				return "", updateErr
			}
			return sha, nil
		}
	}

	// HEAD resolution failed — likely the entrypoint's chown broke git visibility
	// through overlayfs. Create a fresh baseline from the current working tree.
	initCmd := fmt.Sprintf(
		"cd %s && git init -b main && git config user.email yoloai@localhost && git config user.name yoloai && git add -A && git commit -q -m baseline",
		dc.WorkDir,
	)
	_, initErr := execInContainer(ctx, rt, name, meta, []string{"sh", "-c", initCmd})
	if initErr != nil {
		return "", fmt.Errorf("create overlay baseline for %s: %w (original HEAD error: %w)", dc.HostPath, initErr, err)
	}

	stdout, err = execInContainer(ctx, rt, name, meta, []string{
		"git", "-C", dc.WorkDir, "rev-parse", "HEAD",
	})
	if err != nil {
		return "", fmt.Errorf("resolve baseline SHA after init for %s: %w", dc.HostPath, err)
	}
	sha := strings.TrimSpace(stdout)
	if updateErr := updateOverlayBaseline(name, dc.HostPath, sha); updateErr != nil {
		return "", updateErr
	}
	return sha, nil
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

		// Resolve baseline SHA if deferred (creates fresh baseline if git is broken)
		baselineSHA, baselineErr := ensureOverlayBaseline(ctx, rt, name, meta, dc)
		if baselineErr != nil {
			return nil, baselineErr
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

		// execInContainer returns strings.TrimSpace'd stdout, which strips
		// the trailing newline. git apply requires a trailing newline to parse
		// the patch correctly — add it back if absent.
		patch := []byte(stdout)
		if len(patch) > 0 && patch[len(patch)-1] != '\n' {
			patch = append(patch, '\n')
		}
		patches = append(patches, PatchSet{
			HostPath: dc.HostPath,
			Mode:     "overlay",
			Patch:    patch,
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
func ListCommitsBeyondBaseline(ctx context.Context, rt runtime.Runtime, name string) ([]CommitInfo, error) {
	workDir, baselineSHA, mode, err := loadDiffContext(name)
	if err != nil {
		return nil, err
	}

	if mode == "rw" {
		return nil, fmt.Errorf("commit listing is not available for :rw directories")
	}

	output, err := rt.GitExec(ctx, name, workDir, "log", "--reverse", "--format=%H %s", baselineSHA+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	lines := strings.TrimSpace(output)
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
func HasUncommittedChanges(ctx context.Context, rt runtime.Runtime, name string) (bool, error) {
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

	// Stage untracked files
	_, err = rt.GitExec(ctx, name, workDir, "add", "-A")
	if err != nil {
		return false, fmt.Errorf("git add: %w", err)
	}

	_, err = rt.GitExec(ctx, name, workDir, "diff", "--quiet", "HEAD")
	if err == nil {
		return false, nil // exit 0 = clean
	}

	// git diff --quiet exits 1 when there are differences.
	// For direct exec.Cmd, check for *exec.ExitError.
	var exitErr *exec.ExitError
	if ok := errors.As(err, &exitErr); ok && exitErr.ExitCode() == 1 {
		return true, nil
	}

	// For runtime.RunCmdExec (used by Tart and other backends),
	// check for the "exec exited with code 1" error message.
	if strings.Contains(err.Error(), "exec exited with code 1") {
		return true, nil
	}

	return false, fmt.Errorf("git diff --quiet: %w", err)
}

// ResolveRef resolves a short SHA prefix to a full 40-char SHA among
// commits beyond the baseline. Returns an error if the ref is ambiguous
// (matches multiple commits) or not found.
func ResolveRef(ctx context.Context, rt runtime.Runtime, name, ref string) (CommitInfo, error) {
	commits, err := ListCommitsBeyondBaseline(ctx, rt, name)
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
func ResolveRefs(ctx context.Context, rt runtime.Runtime, name string, refs []string) ([]CommitInfo, error) {
	allCommits, err := ListCommitsBeyondBaseline(ctx, rt, name)
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
func GenerateFormatPatchForRefs(ctx context.Context, rt runtime.Runtime, name string, shas, paths []string) (patchDir string, files []string, err error) {
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

	for i, sha := range shas {
		args := []string{"format-patch", "--stdout", "-1", sha}
		if len(paths) > 0 {
			args = append(args, "--")
			args = append(args, paths...)
		}
		output, runErr := rt.GitExec(ctx, name, workDir, args...)
		if runErr != nil {
			os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
			return "", nil, fmt.Errorf("git format-patch -1 %s: %w", sha, runErr)
		}
		if strings.TrimSpace(output) == "" {
			continue
		}
		fname := fmt.Sprintf("%04d-%s.patch", i+1, sha[:min(12, len(sha))])
		if writeErr := fileutil.WriteFile(filepath.Join(patchDir, fname), []byte(output), 0600); writeErr != nil {
			os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
			return "", nil, fmt.Errorf("write patch %s: %w", fname, writeErr)
		}
		files = append(files, fname)
	}

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
func AdvanceBaseline(ctx context.Context, rt runtime.Runtime, name string) error {
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
	sha, err := rt.GitExec(ctx, name, workDir, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("git rev-parse: %w", err)
	}

	meta.Workdir.BaselineSHA = strings.TrimSpace(sha)
	return SaveMeta(sandboxDir, meta)
}

// GenerateFormatPatch creates .patch files (one per commit) for commits
// beyond the baseline. Returns the temp directory path and sorted list
// of .patch filenames. The caller is responsible for os.RemoveAll(patchDir).
// When paths is non-empty, only commits touching those paths are included.
func GenerateFormatPatch(ctx context.Context, rt runtime.Runtime, name string, paths []string) (patchDir string, files []string, err error) {
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

	// Get the list of commits to generate patches for.
	// Use --stdout to capture patch content via rt.GitExec return value,
	// which works for all backends (Docker runs git on host, Tart runs git
	// in VM but returns stdout to host).
	revArgs := []string{"rev-list", "--reverse", baselineSHA + "..HEAD"}
	if len(paths) > 0 {
		revArgs = append(revArgs, "--")
		revArgs = append(revArgs, paths...)
	}
	revOut, revErr := rt.GitExec(ctx, name, workDir, revArgs...)
	if revErr != nil {
		os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
		return "", nil, fmt.Errorf("git rev-list: %w", revErr)
	}

	shas := strings.Fields(strings.TrimSpace(revOut))
	for i, sha := range shas {
		output, runErr := rt.GitExec(ctx, name, workDir, "format-patch", "--stdout", "-1", sha)
		if runErr != nil {
			os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
			return "", nil, fmt.Errorf("git format-patch -1 %s: %w", sha, runErr)
		}
		if strings.TrimSpace(output) == "" {
			continue
		}
		fname := fmt.Sprintf("%04d-%s.patch", i+1, sha[:min(12, len(sha))])
		if writeErr := fileutil.WriteFile(filepath.Join(patchDir, fname), []byte(output), 0600); writeErr != nil {
			os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
			return "", nil, fmt.Errorf("write patch %s: %w", fname, writeErr)
		}
		files = append(files, fname)
	}

	return patchDir, files, nil
}

// GenerateWIPDiff produces a binary patch of uncommitted changes (against
// HEAD, not the baseline). This captures only work-in-progress changes
// that the agent hasn't committed. Returns empty patch if no uncommitted changes.
func GenerateWIPDiff(ctx context.Context, rt runtime.Runtime, name string, paths []string) (patch []byte, stat string, err error) {
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

	// Stage untracked files
	_, err = rt.GitExec(ctx, name, workDir, "add", "-A")
	if err != nil {
		return nil, "", fmt.Errorf("git add: %w", err)
	}

	// Generate binary patch against HEAD
	patchArgs := []string{"diff", "--binary", "HEAD"}
	if len(paths) > 0 {
		patchArgs = append(patchArgs, "--")
		patchArgs = append(patchArgs, paths...)
	}

	patchOut, err := rt.GitExec(ctx, name, workDir, patchArgs...)
	if err != nil {
		return nil, "", fmt.Errorf("git diff (WIP patch): %w", err)
	}

	if patchOut == "" {
		return nil, "", nil
	}

	// Generate stat summary against HEAD
	statArgs := []string{"diff", "--stat", "HEAD"}
	if len(paths) > 0 {
		statArgs = append(statArgs, "--")
		statArgs = append(statArgs, paths...)
	}

	statOut, err := rt.GitExec(ctx, name, workDir, statArgs...)
	if err != nil {
		return nil, "", fmt.Errorf("git diff (WIP stat): %w", err)
	}

	return []byte(patchOut), strings.TrimRight(statOut, "\n"), nil
}

// GenerateMultiPatch produces patches for all :copy directories.
// :rw dirs are skipped (changes are already live).
// Uses rt.GitExec to run git commands (works on both Docker and VM backends).
func GenerateMultiPatch(ctx context.Context, rt runtime.Runtime, name string, paths []string) ([]PatchSet, error) {
	contexts, err := LoadAllDiffContexts(name)
	if err != nil {
		return nil, err
	}

	var patches []PatchSet
	for _, dc := range contexts {
		if dc.Mode != "copy" {
			continue
		}

		// Stage untracked files
		_, _ = rt.GitExec(ctx, name, dc.WorkDir, "add", "-A")

		// Generate binary patch
		patchArgs := []string{"diff", "--binary", dc.BaselineSHA}
		if len(paths) > 0 {
			patchArgs = append(patchArgs, "--")
			patchArgs = append(patchArgs, paths...)
		}
		patchOut, patchErr := rt.GitExec(ctx, name, dc.WorkDir, patchArgs...)
		if patchErr != nil {
			return nil, fmt.Errorf("git diff (patch) in %s: %w", dc.HostPath, patchErr)
		}

		if len(strings.TrimSpace(patchOut)) == 0 {
			continue
		}

		// Generate stat
		statArgs := []string{"diff", "--stat", dc.BaselineSHA}
		if len(paths) > 0 {
			statArgs = append(statArgs, "--")
			statArgs = append(statArgs, paths...)
		}
		statOut, _ := rt.GitExec(ctx, name, dc.WorkDir, statArgs...)

		patches = append(patches, PatchSet{
			HostPath: dc.HostPath,
			Mode:     dc.Mode,
			Patch:    []byte(patchOut),
			Stat:     strings.TrimRight(statOut, "\n"),
		})
	}

	return patches, nil
}
