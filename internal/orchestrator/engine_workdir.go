// ABOUTME: Engine-level workdir verbs — diff/apply/export/commit-history/baseline
// ABOUTME: and tag operations, so the Workdir sub-handle never threads layout/runtime.

package orchestrator

import (
	"context"
	"fmt"

	"github.com/kstenerud/yoloai/copyflow"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/store"
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

// LoadAgentConfig reads a sandbox's agent.json — the inside-process config
// (agent type + model) that Q104 splits out of the substrate environment record.
// It confirms the sandbox directory exists, then reads the sibling doc. A sandbox
// with no agent.json yields a zero-value config (agentcfg.Load is soft on a
// missing file), so callers must tolerate empty fields for records not yet
// carrying it.
func (e *Engine) LoadAgentConfig(name string) (*agentcfg.AgentConfig, error) {
	sandboxDir := e.layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}
	return agentcfg.Load(sandboxDir)
}

// GenerateWorkingDiff returns the copy-mode working diff (committed changes
// since baseline). Best-effort backend open: a nil runtime falls back to the
// host-git path.
func (e *Engine) GenerateWorkingDiff(ctx context.Context, name string, dirHostPath string, paths []string, stat, nameOnly bool, pathPrefix string) (string, error) {
	e.TryEnsure(ctx)
	return copyflow.GenerateDiff(ctx, copyflow.DiffOptions{
		Name:        name,
		Layout:      e.layout,
		Paths:       paths,
		Stat:        stat,
		NameOnly:    nameOnly,
		Runtime:     e.runtime,
		DirHostPath: dirHostPath,
		PathPrefix:  pathPrefix,
	})
}

// GenerateOverlayDiff returns the overlay-mode diff, which runs git inside the
// running container (requires the runtime).
func (e *Engine) GenerateOverlayDiff(ctx context.Context, name string, dirHostPath string, stat, nameOnly bool) (string, error) {
	e.TryEnsure(ctx)
	return copyflow.GenerateOverlayDiff(ctx, e.runtime, copyflow.DiffOptions{
		Name:        name,
		Layout:      e.layout,
		Stat:        stat,
		NameOnly:    nameOnly,
		DirHostPath: dirHostPath,
	})
}

// GenerateWorkingChanges returns the structured per-file change summary for
// copy/rw workdirs. Best-effort backend open: a nil runtime falls back to the
// host-git path.
func (e *Engine) GenerateWorkingChanges(ctx context.Context, name string, dirHostPath string, paths []string) ([]copyflow.FileChange, error) {
	e.TryEnsure(ctx)
	return copyflow.GenerateChanges(ctx, copyflow.DiffOptions{
		Name:        name,
		Layout:      e.layout,
		Paths:       paths,
		Runtime:     e.runtime,
		DirHostPath: dirHostPath,
	})
}

// GenerateOverlayChanges returns the structured per-file change summary for an
// :overlay-mode workdir (requires the running container).
func (e *Engine) GenerateOverlayChanges(ctx context.Context, name string, dirHostPath string) ([]copyflow.FileChange, error) {
	e.TryEnsure(ctx)
	return copyflow.GenerateOverlayChanges(ctx, e.runtime, copyflow.DiffOptions{
		Name:        name,
		Layout:      e.layout,
		DirHostPath: dirHostPath,
	})
}

// GenerateCommitDiff returns the diff for a specific commit or commit range from
// the sandbox work copy (copy-mode only). The runtime dispatches git to where
// the work copy lives — on the host for bind-mount backends, in-VM for Tart.
func (e *Engine) GenerateCommitDiff(ctx context.Context, name string, dirHostPath string, ref string, stat bool) (string, error) {
	// Best-effort backend open so the work copy is read where it lives — in the
	// VM for Tart; a nil runtime falls back to host git (correct for bind-mount
	// backends). Without this, e.runtime stays nil and Tart commit diffs run
	// host git on a VM-local path.
	e.TryEnsure(ctx)
	return copyflow.GenerateCommitDiff(ctx, copyflow.CommitDiffOptions{
		Name:        name,
		Layout:      e.layout,
		Runtime:     e.runtime,
		Ref:         ref,
		Stat:        stat,
		DirHostPath: dirHostPath,
	})
}

// ExportPatches writes the sandbox's changes as patch files (the apply
// --patches flow). Best-effort backend open; overlay export needs the container.
func (e *Engine) ExportPatches(ctx context.Context, name string, opts copyflow.ExportOptions) (*copyflow.ExportResult, error) {
	e.TryEnsure(ctx)
	return copyflow.Export(ctx, e.layout, e.runtime, name, opts)
}

// ApplySeries replays the sandbox's beyond-baseline commits onto the host.
func (e *Engine) ApplySeries(ctx context.Context, name string, opts copyflow.ApplySeriesOptions) (*copyflow.ApplyResult, error) {
	e.TryEnsure(ctx)
	return copyflow.ApplySeries(ctx, e.layout, e.runtime, name, opts)
}

// ApplyOverlay lands an overlay sandbox's upper-layer changes onto the host.
func (e *Engine) ApplyOverlay(ctx context.Context, name string, opts copyflow.ApplyOverlayOptions) (*copyflow.ApplyResult, error) {
	e.TryEnsure(ctx)
	return copyflow.ApplyOverlay(ctx, e.layout, e.runtime, name, opts)
}

// ApplyAll lands the net working diff (copy-mode) onto the host unstaged.
func (e *Engine) ApplyAll(ctx context.Context, name string, opts copyflow.ApplyAllOptions) (*copyflow.ApplyResult, error) {
	e.TryEnsure(ctx)
	return copyflow.ApplyAll(ctx, e.layout, e.runtime, name, opts)
}

// ListCommitsOverlay returns the overlay sandbox's beyond-baseline commits (git
// log inside the running container).
func (e *Engine) ListCommitsOverlay(ctx context.Context, name string, dirHostPath string) ([]copyflow.CommitInfo, error) {
	e.TryEnsure(ctx)
	return copyflow.ListCommitsBeyondBaselineOverlay(ctx, e.layout, e.runtime, name, dirHostPath)
}

// ListCommitsWithStats returns the copy-mode beyond-baseline commits, each with
// a per-commit diff stat.
func (e *Engine) ListCommitsWithStats(ctx context.Context, name string, dirHostPath string) ([]copyflow.CommitInfoWithStat, error) {
	e.TryEnsure(ctx)
	return copyflow.ListCommitsWithStats(ctx, e.layout, e.runtime, name, dirHostPath)
}

// ListCommits returns the copy-mode beyond-baseline commits.
func (e *Engine) ListCommits(ctx context.Context, name string, dirHostPath string) ([]copyflow.CommitInfo, error) {
	e.TryEnsure(ctx)
	return copyflow.ListCommitsBeyondBaseline(ctx, e.layout, e.runtime, name, dirHostPath)
}

// HasUncommittedChanges reports whether the workdir has edits beyond its last
// commit.
func (e *Engine) HasUncommittedChanges(ctx context.Context, name string, dirHostPath string) (bool, error) {
	e.TryEnsure(ctx)
	return copyflow.HasUncommittedChanges(ctx, e.layout, e.runtime, name, dirHostPath)
}

// AdvanceBaseline moves the diff baseline to HEAD via compare-and-swap against
// expectedCurrentSHA.
func (e *Engine) AdvanceBaseline(ctx context.Context, name string, dirHostPath string, expectedCurrentSHA string) (*copyflow.BaselineChange, error) {
	e.TryEnsure(ctx)
	return copyflow.AdvanceBaselineCAS(ctx, e.layout, e.runtime, name, dirHostPath, expectedCurrentSHA)
}

// SetBaseline moves the diff baseline to ref via compare-and-swap against
// expectedCurrentSHA.
func (e *Engine) SetBaseline(ctx context.Context, name string, dirHostPath string, expectedCurrentSHA, ref string) (*copyflow.BaselineChange, error) {
	e.TryEnsure(ctx)
	return copyflow.SetBaselineCAS(ctx, e.layout, e.runtime, name, dirHostPath, expectedCurrentSHA, ref)
}

// BaselineLog returns the workdir's commit history from inception to HEAD,
// marking the current baseline.
func (e *Engine) BaselineLog(ctx context.Context, name string, dirHostPath string) ([]copyflow.BaselineLogEntry, error) {
	e.TryEnsure(ctx)
	return copyflow.BaselineLog(ctx, e.layout, e.runtime, name, dirHostPath)
}

// WorkdirTags returns the sandbox's checkpoint tags with their annotated
// messages populated. With unappliedOnly, returns only tags not yet on the host.
// Tagging is copy-mode only — :rw and :overlay workdirs yield an empty list.
func (e *Engine) WorkdirTags(ctx context.Context, name string, dirHostPath string, unappliedOnly bool) ([]TagInfo, error) {
	// Open the backend best-effort so Tart reads run inside the VM; a nil
	// runtime falls back to host git (correct for Docker/Podman/Seatbelt).
	e.TryEnsure(ctx)

	var (
		tags []TagInfo
		err  error
	)
	if unappliedOnly {
		tags, err = ListUnappliedTags(ctx, e.layout, e.runtime, name, dirHostPath)
	} else {
		tags, err = ListTagsBeyondBaseline(ctx, e.layout, e.runtime, name, dirHostPath)
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
	dir := meta.Dir(dirHostPath)
	if dir == nil {
		return nil, fmt.Errorf("directory %q not found in sandbox %q", dirHostPath, name)
	}
	workDir := store.WorkDir(e.layout.SandboxDir(name), dir.HostPath)
	g := git.NewSandbox(e.layout, e.runtime, name)
	for i := range tags {
		tags[i].Message = getTagMessage(ctx, g, workDir, tags[i].Name)
	}
	return tags, nil
}

// TransferWorkdirTags re-creates the sandbox's tags on the host target repo,
// pointing each at the host commit its sandbox commit landed on.
func (e *Engine) TransferWorkdirTags(ctx context.Context, name string, dirHostPath string, tags []TagInfo, shaMap map[string]string) (*TransferTagsResult, error) {
	// Best-effort backend open so commit-matching reads the Tart VM work copy;
	// a nil runtime falls back to host git.
	e.TryEnsure(ctx)
	return TransferTags(ctx, e.layout, e.runtime, name, dirHostPath, tags, shaMap)
}

// TargetIsGitRepo reports whether the sandbox's original host workdir is a git
// repository (the apply target).
func (e *Engine) TargetIsGitRepo(name string, dirHostPath string) (bool, error) {
	return TargetIsGitRepo(e.layout, name, dirHostPath)
}
