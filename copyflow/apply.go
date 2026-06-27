// ABOUTME: Apply operations: patch generation, baseline advancement, overlay apply,
// ABOUTME: selective ref resolution, format-patch, and uncommitted diff for sandbox work directories.

package copyflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// execInSandbox runs cmd inside the sandbox's container and returns
// stdout. Local helper so this subpackage doesn't import its parent.
// hostUID is layout.HostUID, resolved at the boundary.
func execInSandbox(ctx context.Context, rt runtime.Backend, name string, meta *store.Environment, hostUID int, cmd []string) (string, error) {
	result, err := rt.Exec(ctx, store.InstanceName(meta.Principal, name), cmd, store.ContainerUser(meta, hostUID))
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

// AppliedCommit describes one commit replayed onto the host by a series apply:
// its subject, the source SHA in the sandbox, and the host SHA after git am
// rewrote it (empty on a DryRun preview, where nothing is applied yet).
type AppliedCommit struct {
	Subject   string
	SourceSHA string
	HostSHA   string
}

// ApplyResult describes the outcome of applying a sandbox's changes.
type ApplyResult struct {
	// Dir is the host directory that was patched.
	Dir string
	// Stat is the human-readable diff stat summary (net-diff / NoCommit applies).
	Stat string
	// Commits are the commits replayed, in order (series applies); empty for a
	// NoCommit/net-diff apply. On a DryRun preview, HostSHA is empty.
	Commits []AppliedCommit
	// UncommittedApplied is true when uncommitted edits were also applied as unstaged changes.
	UncommittedApplied bool
}

// ApplyAllOptions configures ApplyAll.
type ApplyAllOptions struct {
	IncludeUncommitted bool     // also apply uncommitted edits (baseline → working tree)
	Paths              []string // optional path filter; when non-empty the baseline is NOT advanced
	DryRun             bool     // generate + validate but do not apply or advance baseline
	DirHostPath        string   // "" selects Dirs[0] (workdir)
}

// ApplyAll applies the sandbox's pending workdir changes back to the original
// host path as a single net diff in the working tree (unstaged). Programmatic
// equivalent of 'yoloai apply <name> --no-commit'.
//
// Returns (nil, nil) when there is nothing to apply. Callers branch on
// result == nil rather than a sentinel error. On opts.DryRun the patch is
// generated and validated (so the caller can preview the stat and confirm) but
// not applied — the returned ApplyResult describes what *would* apply.
//
// With aux :copy/:overlay dirs removed the surface is workdir-only — the name
// "ApplyAll" is preserved for stability but the iteration is gone.
//
// layout determines where the per-sandbox lock file lives; callers
// thread their own Layout in (yoloai.Client supplies c.layout).
func ApplyAll(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, opts ApplyAllOptions) (*ApplyResult, error) {
	unlock, err := store.AcquireLock(layout, name)
	if err != nil {
		return nil, err
	}
	defer unlock()

	meta, err := store.LoadEnvironment(layout.SandboxDir(name))
	if err != nil {
		return nil, err
	}
	dir := meta.Dir(opts.DirHostPath)
	if dir == nil || dir.Mode != "copy" {
		// :rw is live; :overlay uses GenerateOverlayPatch. Neither belongs in
		// the squash apply path that funnels through ApplyAll.
		return nil, nil
	}

	patchBytes, stat, err := GeneratePatch(ctx, layout, rt, name, opts.DirHostPath, opts.Paths, opts.IncludeUncommitted)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(patchBytes))) == 0 {
		return nil, nil
	}

	hostPath := dir.HostPath
	isGit := git.IsGitRepo(hostPath)
	hostGit := git.NewHost(layout)
	if err := hostGit.CheckPatch(ctx, patchBytes, hostPath, isGit); err != nil {
		return nil, err
	}
	if opts.DryRun {
		return &ApplyResult{Dir: hostPath, Stat: stat}, nil
	}

	if err := hostGit.ApplyPatch(ctx, patchBytes, hostPath, isGit); err != nil {
		return nil, fmt.Errorf("%s: %w", hostPath, err)
	}

	// Path-filtered applies don't advance the baseline (the remaining
	// unapplied paths still diff against it).
	if len(opts.Paths) == 0 {
		if err := AdvanceBaseline(ctx, layout, rt, name, opts.DirHostPath); err != nil {
			return nil, fmt.Errorf("advance baseline: %w", err)
		}
	}

	return &ApplyResult{Dir: hostPath, Stat: stat}, nil
}

// ApplySeriesOptions configures ApplySeries.
type ApplySeriesOptions struct {
	Refs               []string // apply only these commits/ranges (a subset, selective); empty = all beyond-baseline commits
	IncludeUncommitted bool     // also apply the agent's uncommitted edits as unstaged changes
	Paths              []string // optional path filter; when non-empty the baseline is NOT advanced
	DryRun             bool     // list the commits that would apply, without applying
	DirHostPath        string   // "" selects Dirs[0] (workdir)
}

// ApplySeries replays the sandbox's beyond-baseline commits onto the host
// workdir as a commit series (git format-patch → git am), preserving each
// commit's message/author — the normal apply flow (D26). With opts.Refs it
// replays only that subset (selective apply) and advances the baseline across
// the contiguous applied prefix; otherwise it replays all and advances to HEAD.
//
// Return contract (comply-or-complain, D27):
//   - (nil, nil): nothing to apply (no beyond-baseline commits). Uncommitted-only
//     changes are a NoCommit concern — the caller routes there.
//   - (nil, err): hard failure, nothing landed — a non-git target (*UsageError;
//     you can't git am into a non-repo, so the caller picks NoCommit), or
//     generate / git am failed outright.
//   - (*ApplyResult, nil): full success (or, on DryRun, the preview).
//   - (*ApplyResult, err): the commits landed (result lists them) but a
//     follow-on step had a non-fatal issue — git am left a stash it couldn't
//     reapply, or uncommitted changes failed to apply. The caller reports what
//     landed and surfaces err (typically as a warning).
//
// The library never decides the non-git fallback or prompts; that's policy.
func ApplySeries(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, opts ApplySeriesOptions) (*ApplyResult, error) {
	unlock, err := store.AcquireLock(layout, name)
	if err != nil {
		return nil, err
	}
	defer unlock()

	meta, err := store.LoadEnvironment(layout.SandboxDir(name))
	if err != nil {
		return nil, err
	}
	dir := meta.Dir(opts.DirHostPath)
	if dir == nil || dir.Mode != "copy" {
		return nil, nil
	}

	hostPath := dir.HostPath
	if !git.IsGitRepo(hostPath) {
		return nil, yoerrors.NewUsageError(
			"cannot replay a commit series onto %s: not a git repository — apply with NoCommit to land the net changes instead",
			hostPath)
	}

	commits, err := resolveSeriesCommits(ctx, layout, rt, name, opts.DirHostPath, opts.Refs)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return nil, nil
	}

	if opts.DryRun {
		return seriesResult(hostPath, commits, nil), nil
	}

	patchDir, files, err := generateSeriesPatch(ctx, layout, rt, name, commits, opts)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(patchDir) //nolint:errcheck // best-effort cleanup

	if len(files) == 0 {
		return nil, nil
	}

	hostGit := git.NewHost(layout)
	shaMap, amErr := hostGit.ApplyFormatPatch(ctx, patchDir, files, hostPath)
	if amErr != nil && shaMap == nil {
		// git am failed outright — nothing applied.
		return nil, amErr
	}

	return finishSeriesApply(ctx, layout, rt, name, hostPath, opts, hostGit, seriesResult(hostPath, commits, shaMap), amErr)
}

// finishSeriesApply advances the baseline (unless path-filtered), surfaces a git
// am stash error (commits already landed), and applies uncommitted changes when
// requested. amErr is the non-nil-but-non-fatal error from ApplyFormatPatch (a
// stash it couldn't reapply); the commits in result did land.
func finishSeriesApply(ctx context.Context, layout config.Layout, rt runtime.Backend, name, hostPath string, opts ApplySeriesOptions, hostGit *git.Git, result *ApplyResult, amErr error) (*ApplyResult, error) {
	// Advance the baseline past the applied commits (skip for path-filtered
	// applies — the remaining paths still diff against it).
	if len(opts.Paths) == 0 {
		if err := advanceSeriesBaseline(ctx, layout, rt, name, opts.DirHostPath, opts.Refs, result.Commits); err != nil {
			return result, fmt.Errorf("advance baseline: %w", err)
		}
	}
	// A stash git am couldn't reapply (pre-existing host changes). Surface it;
	// the commits did land. Uncommitted changes are skipped in this state.
	if amErr != nil {
		return result, amErr
	}
	if opts.IncludeUncommitted {
		applied, err := applySeriesUncommitted(ctx, layout, rt, name, opts.DirHostPath, hostPath, hostGit, opts.Paths)
		if err != nil {
			return result, err
		}
		result.UncommittedApplied = applied
	}
	return result, nil
}

// seriesResult builds an ApplyResult from the replayed commits. shaMap (sandbox
// SHA → host SHA, lowercased keys) is nil for a DryRun preview, leaving HostSHA
// empty.
func seriesResult(hostPath string, commits []CommitInfo, shaMap map[string]string) *ApplyResult {
	result := &ApplyResult{Dir: hostPath, Commits: make([]AppliedCommit, 0, len(commits))}
	for _, c := range commits {
		ac := AppliedCommit{Subject: c.Subject, SourceSHA: c.SHA}
		if shaMap != nil {
			ac.HostSHA = shaMap[strings.ToLower(c.SHA)]
		}
		result.Commits = append(result.Commits, ac)
	}
	return result
}

// applySeriesUncommitted applies the agent's uncommitted edits as unstaged changes
// after the commit series has landed. Errors are wrapped to make clear the
// commits already applied (the caller surfaces them as a warning, not a hard failure).
func applySeriesUncommitted(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string, hostPath string, hostGit *git.Git, paths []string) (bool, error) {
	uncommittedPatch, _, err := GenerateUncommittedDiff(ctx, layout, rt, name, dirHostPath, paths)
	if err != nil {
		return false, fmt.Errorf("generate uncommitted diff (commits already applied): %w", err)
	}
	if len(uncommittedPatch) == 0 {
		return false, nil
	}
	if err := hostGit.ApplyPatch(ctx, uncommittedPatch, hostPath, true); err != nil {
		return false, fmt.Errorf("apply uncommitted changes (commits already applied): %w", err)
	}
	return true, nil
}

// resolveSeriesCommits returns the commits to replay: the selected subset when
// refs are given (selective apply), otherwise all beyond-baseline commits.
func resolveSeriesCommits(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string, refs []string) ([]CommitInfo, error) {
	if len(refs) > 0 {
		return ResolveRefs(ctx, layout, rt, name, dirHostPath, refs)
	}
	return ListCommitsBeyondBaseline(ctx, layout, rt, name, dirHostPath)
}

// generateSeriesPatch produces the format-patch series for the commits to apply
// — for the resolved subset when refs are given, otherwise the whole range.
func generateSeriesPatch(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, commits []CommitInfo, opts ApplySeriesOptions) (patchDir string, files []string, err error) {
	if len(opts.Refs) == 0 {
		return GenerateFormatPatch(ctx, layout, rt, name, opts.DirHostPath, opts.Paths)
	}
	shas := make([]string, len(commits))
	for i, c := range commits {
		shas[i] = c.SHA
	}
	return GenerateFormatPatchForRefs(ctx, layout, rt, name, opts.DirHostPath, shas, opts.Paths)
}

// advanceSeriesBaseline moves the diff baseline past the applied commits. A full
// apply advances to HEAD; a selective apply advances only across the contiguous
// prefix of applied commits (commits after a skipped one stay beyond baseline).
func advanceSeriesBaseline(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string, refs []string, applied []AppliedCommit) error {
	if len(refs) == 0 {
		return AdvanceBaseline(ctx, layout, rt, name, dirHostPath)
	}
	all, err := ListCommitsBeyondBaseline(ctx, layout, rt, name, dirHostPath)
	if err != nil {
		return err
	}
	appliedSet := make(map[string]bool, len(applied))
	for _, c := range applied {
		appliedSet[c.SourceSHA] = true
	}
	if prefixEnd := git.ContiguousPrefixEnd(all, appliedSet); prefixEnd >= 0 {
		return AdvanceBaselineTo(layout, name, dirHostPath, all[prefixEnd].SHA)
	}
	return nil
}

// CommitInfo is an alias for git.CommitInfo.
type CommitInfo = git.CommitInfo

// PatchSet is an alias for git.PatchSet.
type PatchSet = git.PatchSet

// GeneratePatch produces a binary patch from the work copy against the
// baseline SHA. Optionally filtered to specific paths.
//
// includeUncommitted=true diffs from baseline to the live working tree,
// including uncommitted edits and untracked files (first stages untracked
// via `git add -A`).
//
// includeUncommitted=false diffs only baseline → HEAD, so uncommitted/untracked
// changes the agent left behind are excluded. The CLI default is false; the
// caller opts in via `yoloai apply --include-uncommitted`.
func GeneratePatch(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string, paths []string, includeUncommitted bool) (patch []byte, stat string, err error) {
	g := git.NewSandbox(layout, rt, name)
	workDir, baselineSHA, mode, err := loadDiffContext(layout, name, dirHostPath)
	if err != nil {
		return nil, "", err
	}

	if mode == "rw" {
		return nil, "", fmt.Errorf("apply is not needed for :rw directories — changes are already live")
	}

	if mode == "overlay" {
		return nil, "", fmt.Errorf("use GenerateOverlayPatch for :overlay directories")
	}

	// Pick the diff endpoint. baselineSHA..HEAD ignores the index and working
	// tree entirely; baselineSHA against the working tree (after `git add -A`)
	// includes them.
	endpoint := "HEAD"
	if includeUncommitted {
		if addErr := gitAddRetry(ctx, g, workDir); addErr != nil {
			return nil, "", fmt.Errorf("git add: %w", addErr)
		}
		endpoint = ""
	}

	patchArgs := []string{"diff", "--binary", baselineSHA}
	if endpoint != "" {
		patchArgs = append(patchArgs, endpoint)
	}
	if len(paths) > 0 {
		patchArgs = append(patchArgs, "--")
		patchArgs = append(patchArgs, paths...)
	}

	patchOut, err := g.Run(ctx, workDir, patchArgs...)
	if err != nil {
		return nil, "", fmt.Errorf("git diff (patch): %w", err)
	}

	statArgs := []string{"diff", "--stat", baselineSHA}
	if endpoint != "" {
		statArgs = append(statArgs, endpoint)
	}
	if len(paths) > 0 {
		statArgs = append(statArgs, "--")
		statArgs = append(statArgs, paths...)
	}

	statOut, err := g.Run(ctx, workDir, statArgs...)
	if err != nil {
		return nil, "", fmt.Errorf("git diff (stat): %w", err)
	}

	return []byte(patchOut), strings.TrimRight(statOut, "\n"), nil
}

// ensureOverlayBaseline resolves or creates a git baseline for an overlay directory.
// If the overlay already has a valid HEAD commit, its SHA is returned. Otherwise
// (e.g. the entrypoint's chown broke git visibility through overlayfs), a fresh
// git repo is initialised inside the container and used as the baseline.
// The resolved SHA is persisted to environment.json so subsequent calls are a no-op.
func ensureOverlayBaseline(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, meta *store.Environment, dc DiffContext) (string, error) {
	if dc.BaselineSHA != "" {
		return dc.BaselineSHA, nil
	}

	// Try to resolve existing HEAD.
	stdout, err := execInSandbox(ctx, rt, name, meta, layout.HostUID, []string{
		"git", "-C", dc.WorkDir, "rev-parse", "HEAD",
	})
	if err == nil {
		sha := strings.TrimSpace(stdout)
		if len(sha) == 40 {
			if updateErr := UpdateOverlayBaseline(layout, name, dc.HostPath, sha); updateErr != nil {
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
	_, initErr := execInSandbox(ctx, rt, name, meta, layout.HostUID, []string{"sh", "-c", initCmd})
	if initErr != nil {
		return "", fmt.Errorf("create overlay baseline for %s: %w (original HEAD error: %w)", dc.HostPath, initErr, err)
	}

	stdout, err = execInSandbox(ctx, rt, name, meta, layout.HostUID, []string{
		"git", "-C", dc.WorkDir, "rev-parse", "HEAD",
	})
	if err != nil {
		return "", fmt.Errorf("resolve baseline SHA after init for %s: %w", dc.HostPath, err)
	}
	sha := strings.TrimSpace(stdout)
	if updateErr := UpdateOverlayBaseline(layout, name, dc.HostPath, sha); updateErr != nil {
		return "", updateErr
	}
	return sha, nil
}

// UpdateOverlayBaseline updates the baseline SHA for an overlay directory in environment.json.
func UpdateOverlayBaseline(layout config.Layout, name, hostPath, sha string) error {
	sandboxDir := layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return err
	}

	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return err
	}

	// Update workdir or aux dir
	found := false
	for i := range meta.Dirs {
		if meta.Dirs[i].HostPath == hostPath {
			meta.Dirs[i].BaselineSHA = sha
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("directory %s not found in sandbox metadata", hostPath)
	}

	return store.SaveEnvironment(sandboxDir, meta)
}

// GenerateOverlayPatch produces a binary patch for overlay-mode directories
// by executing git commands inside the running container.
func GenerateOverlayPatch(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string, paths []string) ([]PatchSet, error) {
	meta, err := store.LoadEnvironment(layout.SandboxDir(name))
	if err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}
	contexts, err := loadAllDiffContexts(layout, name, dirHostPath)
	if err != nil {
		return nil, err
	}

	var patches []PatchSet
	for _, dc := range contexts {
		if dc.Mode != "overlay" {
			continue
		}
		ps, err := generateOverlayPatchForContext(ctx, layout, rt, name, meta, dc, paths)
		if err != nil {
			return nil, err
		}
		if ps != nil {
			patches = append(patches, *ps)
		}
	}

	return patches, nil
}

// generateOverlayPatchForContext produces a PatchSet for a single overlay diff
// context. Returns nil if there are no changes.
func generateOverlayPatchForContext(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, meta *store.Environment, dc DiffContext, paths []string) (*PatchSet, error) {
	baselineSHA, err := ensureOverlayBaseline(ctx, layout, rt, name, meta, dc)
	if err != nil {
		return nil, err
	}

	if _, err := execInSandbox(ctx, rt, name, meta, layout.HostUID, []string{
		"git", "-C", dc.WorkDir, "add", "-A",
	}); err != nil {
		return nil, fmt.Errorf("stage untracked in %s: %w", dc.HostPath, err)
	}

	patchArgs := append([]string{"git", "-c", "core.hooksPath=/dev/null", "-C", dc.WorkDir, "diff", "--binary", baselineSHA}, pathFilterArgs(paths)...)
	stdout, err := execInSandbox(ctx, rt, name, meta, layout.HostUID, patchArgs)
	if err != nil {
		return nil, fmt.Errorf("git diff (patch) in %s: %w", dc.HostPath, err)
	}
	if len(stdout) == 0 {
		return nil, nil
	}

	statArgs := append([]string{"git", "-c", "core.hooksPath=/dev/null", "-C", dc.WorkDir, "diff", "--stat", baselineSHA}, pathFilterArgs(paths)...)
	statOut, err := execInSandbox(ctx, rt, name, meta, layout.HostUID, statArgs)
	if err != nil {
		return nil, fmt.Errorf("git diff (stat) in %s: %w", dc.HostPath, err)
	}

	// ExecInContainer returns strings.TrimSpace'd stdout, which strips
	// the trailing newline. git apply requires a trailing newline to parse
	// the patch correctly — add it back if absent.
	patch := []byte(stdout)
	if len(patch) > 0 && patch[len(patch)-1] != '\n' {
		patch = append(patch, '\n')
	}
	return &PatchSet{
		HostPath: dc.HostPath,
		Mode:     "overlay",
		Patch:    patch,
		Stat:     strings.TrimRight(statOut, "\n"),
	}, nil
}

// pathFilterArgs returns the "--" separator followed by paths when paths is
// non-empty, for appending to a git diff command.
func pathFilterArgs(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	return append([]string{"--"}, paths...)
}

// UpdateOverlayBaselineToHEAD advances the overlay baseline for a directory
// to the current HEAD inside the running container. Called after a successful
// overlay apply to prevent re-applying already-applied changes.
func UpdateOverlayBaselineToHEAD(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string, hostPath string) error {
	meta, err := store.LoadEnvironment(layout.SandboxDir(name))
	if err != nil {
		return fmt.Errorf("load metadata: %w", err)
	}
	contexts, err := loadAllDiffContexts(layout, name, dirHostPath)
	if err != nil {
		return err
	}

	for _, dc := range contexts {
		if dc.Mode != "overlay" || dc.HostPath != hostPath {
			continue
		}
		stdout, err := execInSandbox(ctx, rt, name, meta, layout.HostUID, []string{
			"git", "-C", dc.WorkDir, "rev-parse", "HEAD",
		})
		if err != nil {
			return fmt.Errorf("get HEAD for %s: %w", hostPath, err)
		}
		return UpdateOverlayBaseline(layout, name, hostPath, strings.TrimSpace(stdout))
	}

	return nil
}

// ListCommitsBeyondBaseline returns the commits made in the work copy
// after the baseline commit, in chronological order (oldest first).
// Returns an empty slice if HEAD == baseline.
func ListCommitsBeyondBaseline(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string) ([]CommitInfo, error) {
	g := git.NewSandbox(layout, rt, name)
	workDir, baselineSHA, mode, err := loadDiffContext(layout, name, dirHostPath)
	if err != nil {
		return nil, err
	}

	if mode == "rw" {
		return nil, fmt.Errorf("commit listing is not available for :rw directories")
	}

	output, err := g.Run(ctx, workDir, "log", "--reverse", "--format=%H %s", baselineSHA+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	lines := strings.TrimSpace(output)
	if lines == "" {
		return nil, nil
	}

	var commits []CommitInfo
	for line := range strings.SplitSeq(lines, "\n") {
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
func HasUncommittedChanges(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string) (bool, error) {
	g := git.NewSandbox(layout, rt, name)
	workDir, _, mode, err := loadDiffContext(layout, name, dirHostPath)
	if err != nil {
		return false, err
	}

	if mode == "rw" {
		return false, fmt.Errorf("uncommitted-change check is not available for :rw directories")
	}

	if mode == "overlay" {
		return false, fmt.Errorf("uncommitted-change check for :overlay directories requires container exec")
	}

	// Stage untracked files — retry on index.lock contention from agent activity.
	if err = gitAddRetry(ctx, g, workDir); err != nil {
		return false, fmt.Errorf("git add: %w", err)
	}

	_, err = g.Run(ctx, workDir, "diff", "--quiet", "HEAD")
	if err == nil {
		return false, nil // exit 0 = clean
	}

	// git diff --quiet exits 1 when there are differences.
	// For direct exec.Cmd, check for *exec.ExitError.
	// git diff --quiet exits 1 when there are differences. Two error shapes
	// surface here depending on which backend ran git: exec.ExitError for
	// direct os/exec callers, runtime.ExecError for backends that route via
	// runtime.RunCmdExec (Tart, containerd, etc.). Match both via errors.As.
	var execErr *runtime.ExecError
	if ok := errors.As(err, &execErr); ok && execErr.ExitCode == 1 {
		return true, nil
	}
	var exitErr *exec.ExitError
	if ok := errors.As(err, &exitErr); ok && exitErr.ExitCode() == 1 {
		return true, nil
	}

	return false, fmt.Errorf("git diff --quiet: %w", err)
}

// selectRefRange resolves a "start..end" range ref and marks matching commits
// in the selected set. Returns an error if either endpoint cannot be resolved
// or if start comes after end.
func selectRefRange(before, after string, allCommits []CommitInfo, shaIndex map[string]int,
	resolve func(string) (string, error), selected map[string]bool) error {
	startSHA, err := resolve(before)
	if err != nil {
		return err
	}
	endSHA, err := resolve(after)
	if err != nil {
		return err
	}
	startIdx, endIdx := shaIndex[startSHA], shaIndex[endSHA]
	if startIdx > endIdx {
		return fmt.Errorf("invalid range: %s is after %s", before, after)
	}
	// Range is exclusive of start, inclusive of end (git convention)
	for i := startIdx + 1; i <= endIdx; i++ {
		selected[strings.ToLower(allCommits[i].SHA)] = true
	}
	return nil
}

// ResolveRefs resolves a list of ref strings (short SHAs or "sha..sha" ranges)
// to an ordered list of CommitInfo. For ranges, all commits between the two
// endpoints (inclusive of end, exclusive of start) are included.
// The returned list preserves chronological order within the sandbox.
func ResolveRefs(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string, refs []string) ([]CommitInfo, error) {
	allCommits, err := ListCommitsBeyondBaseline(ctx, layout, rt, name, dirHostPath)
	if err != nil {
		return nil, err
	}

	shaIndex := buildSHAIndex(allCommits)
	selected, err := selectRefs(refs, allCommits, shaIndex)
	if err != nil {
		return nil, err
	}

	var result []CommitInfo
	for _, c := range allCommits {
		if selected[strings.ToLower(c.SHA)] {
			result = append(result, c)
		}
	}
	return result, nil
}

// buildSHAIndex maps full lowercased SHAs to their index in allCommits.
func buildSHAIndex(commits []CommitInfo) map[string]int {
	idx := make(map[string]int, len(commits))
	for i, c := range commits {
		idx[strings.ToLower(c.SHA)] = i
	}
	return idx
}

// resolveShortRef expands a short SHA prefix to the full lowercased SHA.
// Returns an error if the ref is ambiguous or not found.
func resolveShortRef(ref string, allCommits []CommitInfo) (string, error) {
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

// selectRefs builds the set of selected SHAs from a list of ref strings.
func selectRefs(refs []string, allCommits []CommitInfo, shaIndex map[string]int) (map[string]bool, error) {
	resolve := func(ref string) (string, error) { return resolveShortRef(ref, allCommits) }
	selected := make(map[string]bool)
	for _, ref := range refs {
		if before, after, isRange := strings.Cut(ref, ".."); isRange {
			if err := selectRefRange(before, after, allCommits, shaIndex, resolve, selected); err != nil {
				return nil, err
			}
		} else {
			fullSHA, err := resolve(ref)
			if err != nil {
				return nil, err
			}
			selected[fullSHA] = true
		}
	}
	return selected, nil
}

// GenerateFormatPatchForRefs creates .patch files for specific commits (by SHA)
// within the sandbox work copy. When paths is non-empty, patches are filtered
// to only include changes in those paths. Returns the temp directory and sorted
// file list. The caller is responsible for os.RemoveAll(patchDir).
func GenerateFormatPatchForRefs(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string, shas, paths []string) (patchDir string, files []string, err error) {
	g := git.NewSandbox(layout, rt, name)
	workDir, _, mode, loadErr := loadDiffContext(layout, name, dirHostPath)
	if loadErr != nil {
		return "", nil, loadErr
	}

	if mode == "rw" {
		return "", nil, fmt.Errorf("format-patch is not available for :rw directories")
	}

	if mode == "overlay" {
		return "", nil, fmt.Errorf("format-patch for :overlay directories requires container exec")
	}

	patchDir, err = layout.MkdirTemp("yoloai-format-patch-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}

	for i, sha := range shas {
		args := []string{"format-patch", "--stdout", "-1", sha}
		if len(paths) > 0 {
			args = append(args, "--")
			args = append(args, paths...)
		}
		output, runErr := g.Run(ctx, workDir, args...)
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
func AdvanceBaselineTo(layout config.Layout, name string, dirHostPath string, sha string) error {
	return advanceBaselineUnlocked(layout, name, dirHostPath, func(string) (string, error) {
		return sha, nil
	})
}

// AdvanceBaseline updates the sandbox's baseline SHA to the current HEAD
// of its work copy. This should be called after a successful apply so that
// subsequent diff/apply operations don't re-show already-applied commits.
// For :rw mode sandboxes, this is a no-op.
func AdvanceBaseline(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string) error {
	g := git.NewSandbox(layout, rt, name)
	return advanceBaselineUnlocked(layout, name, dirHostPath, func(workDir string) (string, error) {
		sha, err := g.Run(ctx, workDir, "rev-parse", "HEAD")
		if err != nil {
			return "", fmt.Errorf("git rev-parse: %w", err)
		}
		return strings.TrimSpace(sha), nil
	})
}

// GenerateFormatPatch creates .patch files (one per commit) for commits
// beyond the baseline. Returns the temp directory path and sorted list
// of .patch filenames. The caller is responsible for os.RemoveAll(patchDir).
// When paths is non-empty, only commits touching those paths are included.
func GenerateFormatPatch(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string, paths []string) (patchDir string, files []string, err error) {
	g := git.NewSandbox(layout, rt, name)
	workDir, baselineSHA, mode, loadErr := loadDiffContext(layout, name, dirHostPath)
	if loadErr != nil {
		return "", nil, loadErr
	}

	if mode == "rw" {
		return "", nil, fmt.Errorf("format-patch is not available for :rw directories")
	}

	if mode == "overlay" {
		return "", nil, fmt.Errorf("format-patch for :overlay directories requires container exec")
	}

	patchDir, err = layout.MkdirTemp("yoloai-format-patch-*")
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
	revOut, revErr := g.Run(ctx, workDir, revArgs...)
	if revErr != nil {
		os.RemoveAll(patchDir) //nolint:errcheck,gosec // best-effort cleanup
		return "", nil, fmt.Errorf("git rev-list: %w", revErr)
	}

	shas := strings.Fields(strings.TrimSpace(revOut))
	for i, sha := range shas {
		output, runErr := g.Run(ctx, workDir, "format-patch", "--stdout", "-1", sha)
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

// GenerateUncommittedDiff produces a binary patch of uncommitted changes (against
// HEAD, not the baseline). This captures only uncommitted changes that the agent
// hasn't committed. Returns empty patch if no uncommitted changes.
func GenerateUncommittedDiff(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string, paths []string) (patch []byte, stat string, err error) {
	g := git.NewSandbox(layout, rt, name)
	workDir, _, mode, loadErr := loadDiffContext(layout, name, dirHostPath)
	if loadErr != nil {
		return nil, "", loadErr
	}

	if mode == "rw" {
		return nil, "", fmt.Errorf("uncommitted diff is not available for :rw directories")
	}

	if mode == "overlay" {
		return nil, "", fmt.Errorf("uncommitted diff for :overlay directories requires container exec")
	}

	// Stage untracked files
	_, err = g.Run(ctx, workDir, "add", "-A")
	if err != nil {
		return nil, "", fmt.Errorf("git add: %w", err)
	}

	// Generate binary patch against HEAD
	patchArgs := []string{"diff", "--binary", "HEAD"}
	if len(paths) > 0 {
		patchArgs = append(patchArgs, "--")
		patchArgs = append(patchArgs, paths...)
	}

	patchOut, err := g.Run(ctx, workDir, patchArgs...)
	if err != nil {
		return nil, "", fmt.Errorf("git diff (uncommitted patch): %w", err)
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

	statOut, err := g.Run(ctx, workDir, statArgs...)
	if err != nil {
		return nil, "", fmt.Errorf("git diff (uncommitted stat): %w", err)
	}

	return []byte(patchOut), strings.TrimRight(statOut, "\n"), nil
}

// gitAddRetry runs `git add -A` via the runtime, retrying on index.lock
// contention. The agent may hold the lock briefly for status bar updates
// while sharing the work dir via a bind mount.
func gitAddRetry(ctx context.Context, g *git.Git, workDir string) error {
	var err error
	for range 5 {
		_, err = g.Run(ctx, workDir, "add", "-A")
		if err == nil || !git.IsIndexLocked(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return err
}
