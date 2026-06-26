// ABOUTME: Diff generation for sandbox workdirs across :copy, :overlay, and :rw modes.
// ABOUTME: Provides context loading, workdir diff, overlay diff, and commit-level diff.

// Package patch generates and applies git-format patches between a sandbox's
// host work directory and its in-sandbox copy. Covers :copy, :overlay, and
// :rw modes; supports format-patch, squash, selective, and export workflows.
package copyflow

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/git"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/store"
)

// DiffOptions controls diff generation.
type DiffOptions struct {
	Name        string          // sandbox name
	Layout      config.Layout   // resolves the sandbox state directory
	Stat        bool            // true for --stat summary only
	NameOnly    bool            // true for --name-only (list changed files)
	Numstat     bool            // true for --numstat machine-readable per-file add/del counts
	Paths       []string        // optional path filter (relative to workdir)
	Runtime     runtime.Backend // runtime backend (required for :copy and :overlay)
	DirHostPath string          // "" selects Dirs[0] (workdir)
	// PathPrefix when set is passed to git as --src-prefix/--dst-prefix for the
	// full diff output (copy mode only; ignored for Stat/NameOnly/overlay/Ref).
	PathPrefix string
}

// FileChange is one file's line-count delta in a workdir diff. Additions and
// Deletions are -1 for binary files (git --numstat prints "-" for those).
type FileChange struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
}

// ParseNumstat parses `git diff --numstat` output ("<add>\t<del>\t<path>" per
// line; binary files show "-\t-\t<path>"). Returns nil for empty input.
func ParseNumstat(text string) []FileChange {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	var result []FileChange
	for line := range strings.SplitSeq(text, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		fc := FileChange{Path: parts[2]}
		if parts[0] == "-" || parts[1] == "-" {
			fc.Binary = true
			fc.Additions = -1
			fc.Deletions = -1
		} else {
			var err error
			if fc.Additions, err = strconv.Atoi(parts[0]); err != nil {
				continue
			}
			if fc.Deletions, err = strconv.Atoi(parts[1]); err != nil {
				continue
			}
		}
		result = append(result, fc)
	}
	return result
}

// GenerateChanges returns the structured per-file change summary for copy/rw
// workdirs (overlay returns ErrOverlayRequiresRuntime; use GenerateOverlayChanges).
func GenerateChanges(ctx context.Context, opts DiffOptions) ([]FileChange, error) {
	opts.Numstat = true
	out, err := GenerateDiff(ctx, opts)
	if err != nil {
		return nil, err
	}
	return ParseNumstat(out), nil
}

// GenerateOverlayChanges returns the structured per-file change summary for an
// :overlay-mode workdir by executing git --numstat inside the running container.
func GenerateOverlayChanges(ctx context.Context, rt runtime.Backend, opts DiffOptions) ([]FileChange, error) {
	opts.Numstat = true
	out, err := GenerateOverlayDiff(ctx, rt, opts)
	if err != nil {
		return nil, err
	}
	return ParseNumstat(out), nil
}

// GenerateDiff produces the workdir diff for a sandbox.
//
// Returns the diff text — empty string means no changes. Q-U
// collapsed diff to the workdir only, so there is no per-directory
// metadata to return; the caller already knows which sandbox/workdir
// they asked about.
//
// Mode dispatch:
//   - :copy: stages untracked files (via opts.Runtime.GitExec), then
//     `git diff` against baseline.
//   - :rw: host-side `git diff HEAD`. Non-git :rw returns "" (no
//     diff available).
//   - :overlay: returns an empty string and ErrOverlayRequiresRuntime —
//     overlay diffs need container exec; route through
//     GenerateOverlayDiff.
func GenerateDiff(ctx context.Context, opts DiffOptions) (string, error) {
	workDir, baselineSHA, mode, err := loadDiffContext(opts.Layout, opts.Name, opts.DirHostPath)
	if err != nil {
		return "", err
	}

	switch mode {
	case "rw":
		return git.NewHost(opts.Layout).RWDiff(ctx, workDir, opts.Paths, opts.Stat, opts.NameOnly, opts.Numstat)

	case "overlay":
		return "", ErrOverlayRequiresRuntime

	default: // "copy"
		g := git.NewSandbox(opts.Layout, opts.Runtime, opts.Name)
		if _, err := g.Run(ctx, workDir, "add", "-A"); err != nil {
			return "", err
		}

		var args []string
		switch {
		case opts.Numstat:
			args = []string{"diff", "--numstat", baselineSHA}
		case opts.Stat:
			args = []string{"diff", "--stat", baselineSHA}
		case opts.NameOnly:
			args = []string{"diff", "--name-only", baselineSHA}
		default:
			args = []string{"diff", "--binary"}
			if opts.PathPrefix != "" {
				args = append(args, "--src-prefix="+opts.PathPrefix, "--dst-prefix="+opts.PathPrefix)
			}
			args = append(args, baselineSHA)
		}
		if len(opts.Paths) > 0 {
			args = append(args, "--")
			args = append(args, opts.Paths...)
		}

		output, err := g.Run(ctx, workDir, args...)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(output, "\n"), nil
	}
}

// ErrOverlayRequiresRuntime is returned by GenerateDiff when called
// on a :overlay-mode sandbox. Overlay diffs run inside the container;
// route through GenerateOverlayDiff instead.
var ErrOverlayRequiresRuntime = fmt.Errorf("overlay diff requires runtime exec; use GenerateOverlayDiff")

// CommitDiffOptions controls commit-level diff generation.
type CommitDiffOptions struct {
	Name        string          // sandbox name
	Layout      config.Layout   // resolves the sandbox state directory
	Runtime     runtime.Backend // dispatches git to the sandbox work copy (in-VM for Tart)
	Ref         string          // single SHA or "sha..sha" range
	Stat        bool            // true for --stat summary only
	DirHostPath string          // "" selects Dirs[0] (workdir)
}

// GenerateCommitDiff produces a diff for a specific commit or range
// within the sandbox work copy. Only works for :copy mode sandboxes.
// Returns the diff text (empty string if there are no changes).
func GenerateCommitDiff(ctx context.Context, opts CommitDiffOptions) (string, error) {
	workDir, _, mode, err := loadDiffContext(opts.Layout, opts.Name, opts.DirHostPath)
	if err != nil {
		return "", err
	}

	if mode == "rw" {
		return "", fmt.Errorf("commit diff is not available for :rw directories")
	}

	// The work copy lives where the sandbox runs it — on the host for
	// bind-mount backends, inside the VM for Tart — so dispatch git through the
	// runtime, exactly like GenerateDiff's :copy branch. NewSandbox falls back
	// to host exec for non-GitExecer backends (docker/podman/containerd).
	g := git.NewSandbox(opts.Layout, opts.Runtime, opts.Name)
	if err := g.StageUntracked(ctx, workDir); err != nil {
		return "", err
	}

	args := []string{"diff"}
	if opts.Stat {
		args = append(args, "--stat")
	} else {
		args = append(args, "--binary")
	}

	// Single SHA -> show that commit's diff (sha~1..sha)
	// Range "a..b" -> pass directly
	if strings.Contains(opts.Ref, "..") {
		args = append(args, opts.Ref)
	} else {
		args = append(args, opts.Ref+"~1", opts.Ref)
	}

	output, err := g.Run(ctx, workDir, args...)
	if err != nil {
		return "", fmt.Errorf("git diff %s: %w", opts.Ref, err)
	}
	return strings.TrimRight(output, "\n"), nil
}

// CommitInfoWithStat extends CommitInfo with a per-commit stat summary.
type CommitInfoWithStat struct {
	CommitInfo
	Stat string `json:"stat,omitempty"` // output of git diff --stat for this commit
}

// ListCommitsWithStats returns commits beyond baseline with per-commit
// --stat summaries. Returns an empty slice if HEAD == baseline.
func ListCommitsWithStats(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string) ([]CommitInfoWithStat, error) {
	commits, err := ListCommitsBeyondBaseline(ctx, layout, rt, name, dirHostPath)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return nil, nil
	}

	workDir, _, _, err := loadDiffContext(layout, name, dirHostPath)
	if err != nil {
		return nil, err
	}

	// Stat each commit in the same scope ListCommitsBeyondBaseline found them
	// (sandbox work copy — in-VM for Tart). Using a host runner here statted
	// VM-side SHAs on the host and broke commit listing on Tart.
	g := git.NewSandbox(layout, rt, name)
	result := make([]CommitInfoWithStat, len(commits))
	for i, c := range commits {
		output, statErr := g.Run(ctx, workDir, "diff", "--stat", c.SHA+"~1", c.SHA)
		if statErr != nil {
			return nil, fmt.Errorf("git diff --stat %s: %w", c.SHA, statErr)
		}
		result[i] = CommitInfoWithStat{
			CommitInfo: c,
			Stat:       strings.TrimRight(output, "\n"),
		}
	}

	return result, nil
}

// loadDiffContext loads the metadata and resolves paths needed for diff.
func loadDiffContext(layout config.Layout, name string, dirHostPath string) (workDir string, baselineSHA string, mode store.DirMode, err error) {
	sandboxDir := layout.SandboxDir(name)
	if dirErr := store.RequireSandboxDir(sandboxDir); dirErr != nil {
		return "", "", "", dirErr
	}

	meta, loadErr := store.LoadEnvironment(sandboxDir)
	if loadErr != nil {
		return "", "", "", loadErr
	}

	dir := meta.Dir(dirHostPath)
	if dir == nil {
		return "", "", "", fmt.Errorf("directory %q not found in sandbox %q", dirHostPath, name)
	}

	mode = dir.Mode

	switch mode {
	case store.DirModeCopy:
		workDir = copyGitWorkDir(sandboxDir, dir.HostPath, dir.MountPath)
		baselineSHA = dir.BaselineSHA
		if baselineSHA == "" {
			return "", "", "", fmt.Errorf("sandbox has no baseline SHA — was it created before diff support?")
		}
	case store.DirModeOverlay:
		// Container path for exec
		workDir = dir.MountPath
		if workDir == "" {
			workDir = dir.HostPath // mirror host path
		}
		baselineSHA = dir.BaselineSHA // may be empty (deferred)
	case store.DirModeRW:
		workDir = dir.HostPath
		baselineSHA = "HEAD"
	case store.DirModeRO:
		return "", "", "", fmt.Errorf("workdir cannot be read-only (mode %s)", mode)
	default:
		return "", "", "", fmt.Errorf("unsupported workdir mode: %s", mode)
	}

	return workDir, baselineSHA, mode, nil
}

// DiffContext holds the resolved paths needed for diff/apply on the workdir.
type DiffContext struct {
	HostPath    string        // original host path (for display)
	WorkDir     string        // path to diff against (work copy for :copy, container path for :overlay, host path for :rw)
	BaselineSHA string        // baseline SHA for :copy and :overlay dirs
	Mode        store.DirMode // "copy", "overlay", or "rw"
}

// LoadAllDiffContexts returns the diff context for the sandbox's
// workdir. After Q-U (2026-05-25) the diff/apply surface is
// workdir-only; aux dirs only support :rw / :ro and aren't
// diffable. The slice return shape is preserved so the existing
// overlay loop callers (GenerateOverlayPatch,
// UpdateOverlayBaselineToHEAD, ListCommitsBeyondBaselineOverlay)
// don't need their loop bodies rewritten.
func LoadAllDiffContexts(layout config.Layout, name string, dirHostPath string) ([]DiffContext, error) {
	sandboxDir := layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, err
	}

	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return nil, err
	}

	dir := meta.Dir(dirHostPath)
	if dir == nil {
		return nil, nil
	}

	switch dir.Mode {
	case store.DirModeCopy:
		return []DiffContext{{
			HostPath:    dir.HostPath,
			WorkDir:     copyGitWorkDir(sandboxDir, dir.HostPath, dir.MountPath),
			BaselineSHA: dir.BaselineSHA,
			Mode:        store.DirModeCopy,
		}}, nil
	case store.DirModeOverlay:
		mountPath := dir.MountPath
		if mountPath == "" {
			mountPath = dir.HostPath
		}
		return []DiffContext{{
			HostPath:    dir.HostPath,
			WorkDir:     mountPath,
			BaselineSHA: dir.BaselineSHA,
			Mode:        store.DirModeOverlay,
		}}, nil
	case store.DirModeRW:
		return []DiffContext{{
			HostPath: dir.HostPath,
			WorkDir:  dir.HostPath,
			Mode:     store.DirModeRW,
		}}, nil
	case store.DirModeRO, "":
		// not diffable
	}
	return nil, nil
}

// copyGitWorkDir returns the path where git should run for a copy-mode directory.
// For VM backends (e.g. Tart), the work copy is copied to a VM-local path stored in
// mountPath, which differs from hostPath. For host-based backends (Docker, Seatbelt),
// mountPath equals hostPath (Docker) or equals the host staging copy (Seatbelt), so
// mountPath being distinct from hostPath reliably identifies a VM backend.
func copyGitWorkDir(sandboxDir, hostPath, mountPath string) string {
	if mountPath != "" && mountPath != hostPath {
		return mountPath
	}
	return store.WorkDir(sandboxDir, hostPath)
}

// ListCommitsBeyondBaselineOverlay returns commits beyond the baseline for
// an overlay-mode workdir by executing git log inside the running container.
func ListCommitsBeyondBaselineOverlay(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, dirHostPath string) ([]CommitInfo, error) {
	meta, err := store.LoadEnvironment(layout.SandboxDir(name))
	if err != nil {
		return nil, fmt.Errorf("load metadata: %w", err)
	}
	dir := meta.Dir(dirHostPath)
	if dir == nil || dir.Mode != "overlay" {
		return nil, nil
	}

	dc, err := overlayDiffContext(layout, name, dirHostPath)
	if err != nil {
		return nil, err
	}

	baselineSHA, err := ensureOverlayBaseline(ctx, layout, rt, name, meta, dc)
	if err != nil {
		return nil, err
	}

	stdout, err := execInSandbox(ctx, rt, name, meta, layout.HostUID, []string{
		"git", "-C", dc.WorkDir, "log", "--reverse", "--format=%H %s", baselineSHA + "..HEAD",
	})
	if err != nil {
		return nil, fmt.Errorf("git log in %s: %w", dc.HostPath, err)
	}

	var commits []CommitInfo
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
		sha, subject, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		commits = append(commits, CommitInfo{SHA: sha, Subject: subject})
	}
	return commits, nil
}

// GenerateOverlayDiff produces the workdir diff for an :overlay-mode
// sandbox by executing git commands inside the running container.
// Use opts.Stat for a summary, opts.NameOnly for a file list only.
// Returns the diff text (empty string if there are no changes).
func GenerateOverlayDiff(ctx context.Context, rt runtime.Backend, opts DiffOptions) (string, error) {
	meta, err := store.LoadEnvironment(opts.Layout.SandboxDir(opts.Name))
	if err != nil {
		return "", fmt.Errorf("load metadata: %w", err)
	}
	dir := meta.Dir(opts.DirHostPath)
	if dir == nil || dir.Mode != "overlay" {
		return "", nil
	}

	dc, err := overlayDiffContext(opts.Layout, opts.Name, opts.DirHostPath)
	if err != nil {
		return "", err
	}

	baselineSHA, err := ensureOverlayBaseline(ctx, opts.Layout, rt, opts.Name, meta, dc)
	if err != nil {
		return "", err
	}

	// Stage untracked files
	if _, err := execInSandbox(ctx, rt, opts.Name, meta, opts.Layout.HostUID, []string{
		"git", "-C", dc.WorkDir, "add", "-A",
	}); err != nil {
		return "", fmt.Errorf("stage untracked in %s: %w", dc.HostPath, err)
	}

	args := []string{"git", "-c", "core.hooksPath=/dev/null", "-C", dc.WorkDir, "diff"}
	switch {
	case opts.Numstat:
		args = append(args, "--numstat")
	case opts.NameOnly:
		args = append(args, "--name-only")
	case opts.Stat:
		args = append(args, "--stat")
	default:
		args = append(args, "--binary")
	}
	args = append(args, baselineSHA)

	stdout, err := execInSandbox(ctx, rt, opts.Name, meta, opts.Layout.HostUID, args)
	if err != nil {
		return "", fmt.Errorf("git diff in %s: %w", dc.HostPath, err)
	}
	return strings.TrimRight(stdout, "\n"), nil
}

// overlayDiffContext returns the workdir's DiffContext, asserting the
// mode is overlay. Used by GenerateOverlayDiff /
// ListCommitsBeyondBaselineOverlay as a small typed accessor.
func overlayDiffContext(layout config.Layout, name string, dirHostPath string) (DiffContext, error) {
	contexts, err := LoadAllDiffContexts(layout, name, dirHostPath)
	if err != nil {
		return DiffContext{}, err
	}
	if len(contexts) == 0 || contexts[0].Mode != "overlay" {
		return DiffContext{}, fmt.Errorf("sandbox %s is not in overlay mode", name)
	}
	return contexts[0], nil
}
