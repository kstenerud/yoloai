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
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
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

// parseNumstat parses `git diff --numstat` output ("<add>\t<del>\t<path>" per
// line; binary files show "-\t-\t<path>"). Returns nil for empty input.
func parseNumstat(text string) []FileChange {
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
// workdirs.
func GenerateChanges(ctx context.Context, opts DiffOptions) ([]FileChange, error) {
	opts.Numstat = true
	out, err := GenerateDiff(ctx, opts)
	if err != nil {
		return nil, err
	}
	return parseNumstat(out), nil
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
func GenerateDiff(ctx context.Context, opts DiffOptions) (string, error) {
	workDir, baselineSHA, mode, err := loadDiffContext(opts.Layout, opts.Name, opts.DirHostPath)
	if err != nil {
		return "", err
	}

	switch mode {
	case "rw":
		return git.NewHost(opts.Layout).RWDiff(ctx, workDir, opts.Paths, opts.Stat, opts.NameOnly, opts.Numstat)

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
	WorkDir     string        // path to diff against (work copy for :copy, host path for :rw)
	BaselineSHA string        // baseline SHA for :copy dirs
	Mode        store.DirMode // "copy" or "rw"
}

// loadAllDiffContexts returns the diff context for the sandbox's
// workdir. After Q-U (2026-05-25) the diff/apply surface is
// workdir-only; aux dirs only support :rw / :ro and aren't diffable.
func loadAllDiffContexts(layout config.Layout, name string, dirHostPath string) ([]DiffContext, error) {
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
	case store.DirModeRW:
		return []DiffContext{{
			HostPath: dir.HostPath,
			WorkDir:  dir.HostPath,
			Mode:     store.DirModeRW,
		}}, nil
	case store.DirModeRO, store.DirModeOverlay, "":
		// not diffable (a retired overlay dir shouldn't reach here — the
		// migration gate forces conversion to copy first)
	}
	return nil, nil
}

// copyGitWorkDir returns the host work-copy path for a copy-mode directory. The
// sandbox-scoped git runner (git.NewSandbox) takes it from here: for backends
// that run git in confinement (Tart, docker/podman/containerd) it maps this host
// path to the in-sandbox path itself (see git's confinementWorkPath); for
// host-side backends git runs against it directly. mountPath is no longer
// consulted here — the locality decision lives behind git.NewSandbox.
func copyGitWorkDir(sandboxDir, hostPath, _ string) string {
	return store.WorkDir(sandboxDir, hostPath)
}
