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

// ApplyOptions configures Workdir.Apply.
type ApplyOptions struct {
	// IncludeWIP additionally applies the agent's uncommitted (work-in-progress)
	// edits as unstaged modifications on the host. Mirrors `yoloai apply
	// --include-wip`.
	IncludeWIP bool
	// NoCommit lands the changes as a single net diff in the working tree
	// (unstaged), instead of replaying the commit series — the `--no-commit`
	// mode. It's the only mode possible against a non-git host target; the
	// default (series replay) refuses such a target with a *UsageError, leaving
	// the non-git→NoCommit decision to the caller (D26/D27).
	NoCommit bool
	// Paths narrows the apply to specific files (relative to the workdir). When
	// set, the diff baseline is NOT advanced (the remaining paths still diff
	// against it).
	Paths []string
	// DryRun previews without applying or advancing the baseline — the series
	// path returns the commits that would apply, the NoCommit path returns the
	// stat. The library never prompts; the CLI uses this to render confirmation.
	DryRun bool
}

// Apply lands the agent's changes back on the original host workdir. By default
// it replays the sandbox's beyond-baseline commits as a series (git format-patch
// → git am), preserving each commit's message/author; with opts.NoCommit it
// instead applies the net diff unstaged into the working tree. Returns
// (nil, nil) when there's nothing to apply — branch on result == nil rather than
// a sentinel error (Q-P). On success (and unless Paths filters the apply) it
// advances the diff baseline.
//
// Comply-or-complain (§2): the series default refuses a non-git host target with
// a *UsageError rather than silently degrading to NoCommit — that policy call is
// the caller's. A (*ApplyResult, error) pair means the commits landed but a
// follow-on step (git am stash, WIP) had a non-fatal issue (see ApplySeries).
func (w *Workdir) Apply(ctx context.Context, opts ApplyOptions) (*ApplyResult, error) {
	if opts.NoCommit {
		return patch.ApplyAll(ctx, w.s.c.layout, w.s.c.rt, w.s.name, patch.ApplyAllOptions{
			IncludeWIP: opts.IncludeWIP,
			Paths:      opts.Paths,
			DryRun:     opts.DryRun,
		})
	}
	return patch.ApplySeries(ctx, w.s.c.layout, w.s.c.rt, w.s.name, patch.ApplySeriesOptions{
		IncludeWIP: opts.IncludeWIP,
		Paths:      opts.Paths,
		DryRun:     opts.DryRun,
	})
}
