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
