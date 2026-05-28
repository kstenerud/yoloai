// ABOUTME: Workdir is the diff/apply sub-handle off a *Sandbox (F2). It owns the
// ABOUTME: copy-vs-overlay resolution so callers get one Diff verb regardless of
// ABOUTME: the workdir's mount mode.

package yoloai

import (
	"context"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// Workdir is a sub-handle for a sandbox's primary working directory — the
// diff/apply surface. Reached via Sandbox(name).Workdir(); pure namespace
// expansion (no IO, no error). Q-G / F2.
type Workdir struct {
	s *Sandbox
}

// Workdir returns the workdir sub-handle for diff/apply operations.
func (s *Sandbox) Workdir() *Workdir {
	return &Workdir{s: s}
}

// DiffOptions configures Workdir.Diff. All fields are optional; the zero value
// produces the full working diff (committed changes since baseline).
type DiffOptions struct {
	// Paths narrows the diff to specific files (relative to the workdir).
	// Ignored for overlay-mode workdirs and for Ref diffs.
	Paths []string
	// Stat renders a `git diff --stat` summary instead of the full patch.
	Stat bool
	// NameOnly lists changed file names only (`git diff --name-only`).
	NameOnly bool
	// Ref diffs a specific commit or commit range from the sandbox's history
	// instead of the working diff. Disk-only; not supported for overlay-mode
	// workdirs (their commits aren't individually addressable from the host).
	Ref string
}

// Diff returns the workdir diff as text — "" means no changes. It resolves the
// workdir's mount mode internally: copy-mode diffs on disk, overlay-mode diffs
// by running git inside the container (which must be running), and Ref diffs
// read the on-disk commit history. Folds the former Diff / DiffWithOptions /
// DiffRef / DiffOverlay methods into one verb.
func (w *Workdir) Diff(ctx context.Context, opts DiffOptions) (string, error) {
	meta, err := store.LoadMeta(w.s.c.layout.SandboxDir(w.s.name))
	if err != nil {
		return "", err
	}
	overlay := meta.Workdir.Mode == store.DirModeOverlay

	if opts.Ref != "" {
		if overlay {
			return "", sandbox.NewPlatformError("ref-based diff is not supported for :overlay sandboxes (commits are not individually addressable from the host)")
		}
		return patch.GenerateCommitDiff(patch.CommitDiffOptions{
			Name:   w.s.name,
			Layout: w.s.c.layout,
			Ref:    opts.Ref,
			Stat:   opts.Stat,
		})
	}

	if overlay {
		return patch.GenerateOverlayDiff(ctx, w.s.c.rt, patch.DiffOptions{
			Name:     w.s.name,
			Layout:   w.s.c.layout,
			Stat:     opts.Stat,
			NameOnly: opts.NameOnly,
		})
	}

	return patch.GenerateDiff(ctx, patch.DiffOptions{
		Name:     w.s.name,
		Layout:   w.s.c.layout,
		Paths:    opts.Paths,
		Stat:     opts.Stat,
		NameOnly: opts.NameOnly,
		Runtime:  w.s.c.rt,
	})
}

// ApplyResult describes the outcome of an Apply: the host directory patched,
// the replayed Commits (series apply) or a `git diff --stat` (NoCommit), and
// whether WIP was applied. Re-exported (type alias) from internal/sandbox/patch.
type ApplyResult = patch.ApplyResult

// AppliedCommit is one commit replayed onto the host by a series apply — its
// Subject, the SourceSHA in the sandbox, and the HostSHA after git am rewrote
// it. Re-exported (type alias) from internal/sandbox/patch.
type AppliedCommit = patch.AppliedCommit

// ApplyMode selects how Apply lands changes. Required — there is no default,
// because the choice is consequential and mutually exclusive, and a movable
// default would silently change behavior out from under callers (§4: empty
// isn't a free default). The CLI, as the policy layer, picks the mode for the
// user (the commit-preserving default, or NoCommit for --no-commit / a non-git
// target); the library requires it explicitly.
type ApplyMode string

const (
	// ApplyModeCommits replays the sandbox's beyond-baseline commits onto the
	// host as a series (git format-patch → git am), preserving each commit's
	// message/author. Requires a git host target — a non-git target is refused
	// with a *UsageError (the caller picks ApplyModeNoCommit there).
	ApplyModeCommits ApplyMode = "commits"
	// ApplyModeNoCommit lands the changes as a single net diff in the working
	// tree (unstaged), not as commits — the `--no-commit` mode, and the only
	// mode possible against a non-git host target.
	ApplyModeNoCommit ApplyMode = "no-commit"
)

// ApplyOptions configures Workdir.Apply. Mode is required.
type ApplyOptions struct {
	// Mode selects commit-series replay vs. net-diff. Required; the zero value
	// is rejected with a *UsageError.
	Mode ApplyMode
	// IncludeWIP additionally applies the agent's uncommitted (work-in-progress)
	// edits as unstaged modifications on the host. Mirrors `yoloai apply
	// --include-wip`.
	IncludeWIP bool
	// Paths narrows the apply to specific files (relative to the workdir). When
	// set, the diff baseline is NOT advanced (the remaining paths still diff
	// against it).
	Paths []string
	// DryRun previews without applying or advancing the baseline — ApplyModeCommits
	// returns the commits that would apply, ApplyModeNoCommit returns the stat.
	// The library never prompts; the CLI uses this to render confirmation.
	DryRun bool
}

// Apply lands the agent's changes back on the original host workdir, per
// opts.Mode: ApplyModeCommits replays the beyond-baseline commits as a series
// (preserving message/author), ApplyModeNoCommit applies the net diff unstaged.
// Returns (nil, nil) when there's nothing to apply — branch on result == nil
// rather than a sentinel error (Q-P). On success (and unless Paths filters the
// apply) it advances the diff baseline.
//
// Comply-or-complain (§2/§4): Mode is required — an unset mode is a *UsageError,
// not a silent default. ApplyModeCommits refuses a non-git host target with a
// *UsageError rather than degrading to net-diff; that policy call is the
// caller's. A (*ApplyResult, error) pair means the commits landed but a
// follow-on step (git am stash, WIP) had a non-fatal issue (see ApplySeries).
func (w *Workdir) Apply(ctx context.Context, opts ApplyOptions) (*ApplyResult, error) {
	switch opts.Mode {
	case ApplyModeCommits:
		return patch.ApplySeries(ctx, w.s.c.layout, w.s.c.rt, w.s.name, patch.ApplySeriesOptions{
			IncludeWIP: opts.IncludeWIP,
			Paths:      opts.Paths,
			DryRun:     opts.DryRun,
		})
	case ApplyModeNoCommit:
		return patch.ApplyAll(ctx, w.s.c.layout, w.s.c.rt, w.s.name, patch.ApplyAllOptions{
			IncludeWIP: opts.IncludeWIP,
			Paths:      opts.Paths,
			DryRun:     opts.DryRun,
		})
	default:
		return nil, sandbox.NewUsageError("apply mode is required: set ApplyOptions.Mode to yoloai.ApplyModeCommits or yoloai.ApplyModeNoCommit")
	}
}
