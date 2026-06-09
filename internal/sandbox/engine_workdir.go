// ABOUTME: Engine-level workdir verbs — diff/apply/export/commit-history/baseline
// ABOUTME: and tag operations, so the Workdir sub-handle never threads layout/runtime.

package sandbox

import (
	"context"

	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/sysexec"
)

// LoadEnvironment reads a sandbox's environment.json after confirming the
// sandbox directory exists. Shared by the Workdir and Network sub-handles so
// they resolve meta through the Engine rather than reaching e.layout themselves.
func (e *Engine) LoadEnvironment(name string) (*store.Environment, error) {
	sandboxDir := e.layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}
	return store.LoadEnvironment(sandboxDir)
}

// GenerateWorkingDiff returns the copy-mode working diff (committed changes
// since baseline). Best-effort backend open: a nil runtime falls back to the
// host-git path.
func (e *Engine) GenerateWorkingDiff(ctx context.Context, name string, paths []string, stat, nameOnly bool) (string, error) {
	e.TryEnsure(ctx)
	return patch.GenerateDiff(ctx, patch.DiffOptions{
		Name:     name,
		Layout:   e.layout,
		Paths:    paths,
		Stat:     stat,
		NameOnly: nameOnly,
		Runtime:  e.runtime,
	})
}

// GenerateOverlayDiff returns the overlay-mode diff, which runs git inside the
// running container (requires the runtime).
func (e *Engine) GenerateOverlayDiff(ctx context.Context, name string, stat, nameOnly bool) (string, error) {
	e.TryEnsure(ctx)
	return patch.GenerateOverlayDiff(ctx, e.runtime, patch.DiffOptions{
		Name:     name,
		Layout:   e.layout,
		Stat:     stat,
		NameOnly: nameOnly,
	})
}

// GenerateCommitDiff returns the diff for a specific commit or commit range from
// the sandbox's on-disk history (copy-mode only; no runtime needed).
func (e *Engine) GenerateCommitDiff(name, ref string, stat bool) (string, error) {
	return patch.GenerateCommitDiff(patch.CommitDiffOptions{
		Name:   name,
		Layout: e.layout,
		Ref:    ref,
		Stat:   stat,
	})
}

// ExportPatches writes the sandbox's changes as patch files (the apply
// --patches flow). Best-effort backend open; overlay export needs the container.
func (e *Engine) ExportPatches(ctx context.Context, name string, opts patch.ExportOptions) (*patch.ExportResult, error) {
	e.TryEnsure(ctx)
	return patch.Export(ctx, e.layout, e.runtime, name, opts)
}

// ApplySeries replays the sandbox's beyond-baseline commits onto the host.
func (e *Engine) ApplySeries(ctx context.Context, name string, opts patch.ApplySeriesOptions) (*patch.ApplyResult, error) {
	e.TryEnsure(ctx)
	return patch.ApplySeries(ctx, e.layout, e.runtime, name, opts)
}

// ApplyOverlay lands an overlay sandbox's upper-layer changes onto the host.
func (e *Engine) ApplyOverlay(ctx context.Context, name string, opts patch.ApplyOverlayOptions) (*patch.ApplyResult, error) {
	e.TryEnsure(ctx)
	return patch.ApplyOverlay(ctx, e.layout, e.runtime, name, opts)
}

// ApplyAll lands the net working diff (copy-mode) onto the host unstaged.
func (e *Engine) ApplyAll(ctx context.Context, name string, opts patch.ApplyAllOptions) (*patch.ApplyResult, error) {
	e.TryEnsure(ctx)
	return patch.ApplyAll(ctx, e.layout, e.runtime, name, opts)
}

// ListCommitsOverlay returns the overlay sandbox's beyond-baseline commits (git
// log inside the running container).
func (e *Engine) ListCommitsOverlay(ctx context.Context, name string) ([]patch.CommitInfo, error) {
	e.TryEnsure(ctx)
	return patch.ListCommitsBeyondBaselineOverlay(ctx, e.layout, e.runtime, name)
}

// ListCommitsWithStats returns the copy-mode beyond-baseline commits, each with
// a per-commit diff stat.
func (e *Engine) ListCommitsWithStats(ctx context.Context, name string) ([]patch.CommitInfoWithStat, error) {
	e.TryEnsure(ctx)
	return patch.ListCommitsWithStats(ctx, e.layout, e.runtime, name)
}

// ListCommits returns the copy-mode beyond-baseline commits.
func (e *Engine) ListCommits(ctx context.Context, name string) ([]patch.CommitInfo, error) {
	e.TryEnsure(ctx)
	return patch.ListCommitsBeyondBaseline(ctx, e.layout, e.runtime, name)
}

// HasUncommittedChanges reports whether the workdir has edits beyond its last
// commit.
func (e *Engine) HasUncommittedChanges(ctx context.Context, name string) (bool, error) {
	e.TryEnsure(ctx)
	return patch.HasUncommittedChanges(ctx, e.layout, e.runtime, name)
}

// AdvanceBaseline moves the diff baseline to HEAD via compare-and-swap against
// expectedCurrentSHA.
func (e *Engine) AdvanceBaseline(ctx context.Context, name, expectedCurrentSHA string) (*patch.BaselineChange, error) {
	e.TryEnsure(ctx)
	return patch.AdvanceBaselineCAS(ctx, e.layout, e.runtime, name, expectedCurrentSHA)
}

// SetBaseline moves the diff baseline to ref via compare-and-swap against
// expectedCurrentSHA.
func (e *Engine) SetBaseline(ctx context.Context, name, expectedCurrentSHA, ref string) (*patch.BaselineChange, error) {
	e.TryEnsure(ctx)
	return patch.SetBaselineCAS(ctx, e.layout, e.runtime, name, expectedCurrentSHA, ref)
}

// BaselineLog returns the workdir's commit history from inception to HEAD,
// marking the current baseline.
func (e *Engine) BaselineLog(ctx context.Context, name string) ([]patch.BaselineLogEntry, error) {
	e.TryEnsure(ctx)
	return patch.BaselineLog(ctx, e.layout, e.runtime, name)
}

// WorkdirTags returns the sandbox's checkpoint tags with their annotated
// messages populated. With unappliedOnly, returns only tags not yet on the host.
// Tagging is copy-mode only — :rw and :overlay workdirs yield an empty list.
func (e *Engine) WorkdirTags(ctx context.Context, name string, unappliedOnly bool) ([]TagInfo, error) {
	// Open the backend best-effort so Tart reads run inside the VM; a nil
	// runtime falls back to host git (correct for Docker/Podman/Seatbelt).
	e.TryEnsure(ctx)

	var (
		tags []TagInfo
		err  error
	)
	if unappliedOnly {
		tags, err = ListUnappliedTags(ctx, e.layout, e.runtime, name)
	} else {
		tags, err = ListTagsBeyondBaseline(ctx, e.layout, e.runtime, name)
	}
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return []TagInfo{}, nil
	}

	meta, err := store.LoadEnvironment(e.layout.SandboxDir(name))
	if err != nil {
		return nil, err
	}
	workDir := store.WorkDir(e.layout.SandboxDir(name), meta.Workdir.HostPath)
	gitEnv := sysexec.GitEnv(e.layout.Env)
	git := sandboxGitRunner(ctx, gitEnv, e.runtime, name, workDir)
	for i := range tags {
		tags[i].Message = getTagMessage(git, tags[i].Name)
	}
	return tags, nil
}

// TransferWorkdirTags re-creates the sandbox's tags on the host target repo,
// pointing each at the host commit its sandbox commit landed on.
func (e *Engine) TransferWorkdirTags(ctx context.Context, name string, tags []TagInfo, shaMap map[string]string) (*TransferTagsResult, error) {
	// Best-effort backend open so commit-matching reads the Tart VM work copy;
	// a nil runtime falls back to host git.
	e.TryEnsure(ctx)
	return TransferTags(ctx, e.layout, e.runtime, name, tags, shaMap)
}

// TargetIsGitRepo reports whether the sandbox's original host workdir is a git
// repository (the apply target).
func (e *Engine) TargetIsGitRepo(name string) (bool, error) {
	return TargetIsGitRepo(e.layout, name)
}
