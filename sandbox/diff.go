package sandbox

import (
	"context"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/workspace"
)

// DiffOptions controls diff generation.
type DiffOptions struct {
	Name  string   // sandbox name
	Stat  bool     // true for --stat summary only
	Paths []string // optional path filter (relative to workdir)
}

// DiffResult is an alias for workspace.DiffResult.
type DiffResult = workspace.DiffResult

// GenerateDiff produces a full diff of agent changes for a sandbox.
// For :copy mode: stages untracked files, then runs git diff --binary
// against the baseline SHA stored in meta.json.
// For :rw mode: runs git diff HEAD on the live host directory.
// Returns an informational DiffResult (not error) for :rw non-git dirs.
func GenerateDiff(opts DiffOptions) (*DiffResult, error) {
	return generateDiff(opts, false)
}

// GenerateDiffStat produces a summary (files changed, insertions,
// deletions) instead of the full diff.
func GenerateDiffStat(opts DiffOptions) (*DiffResult, error) {
	return generateDiff(opts, true)
}

func generateDiff(opts DiffOptions, stat bool) (*DiffResult, error) {
	workDir, baselineSHA, mode, err := loadDiffContext(opts.Name)
	if err != nil {
		return nil, err
	}

	switch mode {
	case "rw":
		return workspace.RWDiff(workDir, opts.Paths, stat)
	case "overlay":
		return &DiffResult{
			Output: "Diff for :overlay directories requires 'yoloai diff' (runs git inside container)",
			Mode:   "overlay",
			Empty:  true,
		}, nil
	default:
		return workspace.CopyDiff(workDir, baselineSHA, opts.Paths, stat)
	}
}

// CommitDiffOptions controls commit-level diff generation.
type CommitDiffOptions struct {
	Name string // sandbox name
	Ref  string // single SHA or "sha..sha" range
	Stat bool   // true for --stat summary only
}

// GenerateCommitDiff produces a diff for a specific commit or range
// within the sandbox work copy. Only works for :copy mode sandboxes.
func GenerateCommitDiff(opts CommitDiffOptions) (*DiffResult, error) {
	workDir, _, mode, err := loadDiffContext(opts.Name)
	if err != nil {
		return nil, err
	}

	if mode == "rw" {
		return nil, fmt.Errorf("commit diff is not available for :rw directories")
	}

	if err := workspace.StageUntracked(workDir); err != nil {
		return nil, err
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

	cmd := workspace.NewGitCmd(workDir, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", opts.Ref, err)
	}

	result := strings.TrimRight(string(output), "\n")
	return &DiffResult{
		Output:  result,
		WorkDir: workDir,
		Mode:    "copy",
		Empty:   len(result) == 0,
	}, nil
}

// CommitInfoWithStat extends CommitInfo with a per-commit stat summary.
type CommitInfoWithStat struct {
	CommitInfo
	Stat string `json:"stat,omitempty"` // output of git diff --stat for this commit
}

// ListCommitsWithStats returns commits beyond baseline with per-commit
// --stat summaries. Returns an empty slice if HEAD == baseline.
func ListCommitsWithStats(name string) ([]CommitInfoWithStat, error) {
	commits, err := ListCommitsBeyondBaseline(name)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return nil, nil
	}

	workDir, _, _, err := loadDiffContext(name)
	if err != nil {
		return nil, err
	}

	result := make([]CommitInfoWithStat, len(commits))
	for i, c := range commits {
		cmd := workspace.NewGitCmd(workDir, "diff", "--stat", c.SHA+"~1", c.SHA)
		output, statErr := cmd.Output()
		if statErr != nil {
			return nil, fmt.Errorf("git diff --stat %s: %w", c.SHA, statErr)
		}
		result[i] = CommitInfoWithStat{
			CommitInfo: c,
			Stat:       strings.TrimRight(string(output), "\n"),
		}
	}

	return result, nil
}

// loadDiffContext loads the metadata and resolves paths needed for diff.
func loadDiffContext(name string) (workDir string, baselineSHA string, mode string, err error) {
	sandboxDir, dirErr := RequireSandboxDir(name)
	if dirErr != nil {
		return "", "", "", dirErr
	}

	meta, loadErr := LoadMeta(sandboxDir)
	if loadErr != nil {
		return "", "", "", loadErr
	}

	mode = meta.Workdir.Mode

	switch mode {
	case "copy":
		workDir = WorkDir(name, meta.Workdir.HostPath)
		baselineSHA = meta.Workdir.BaselineSHA
		if baselineSHA == "" {
			return "", "", "", fmt.Errorf("sandbox has no baseline SHA — was it created before diff support?")
		}
	case "overlay":
		// Container path for exec
		workDir = meta.Workdir.MountPath
		if workDir == "" {
			workDir = meta.Workdir.HostPath // mirror host path
		}
		baselineSHA = meta.Workdir.BaselineSHA // may be empty (deferred)
	case "rw":
		workDir = meta.Workdir.HostPath
		baselineSHA = "HEAD"
	default:
		return "", "", "", fmt.Errorf("unsupported workdir mode: %s", mode)
	}

	return workDir, baselineSHA, mode, nil
}

// DiffContext holds the resolved paths needed for diff/apply on one directory.
type DiffContext struct {
	HostPath    string // original host path (for display)
	WorkDir     string // path to diff against (work copy for :copy, container path for :overlay, host path for :rw)
	BaselineSHA string // baseline SHA for :copy and :overlay dirs
	Mode        string // "copy", "overlay", or "rw"
}

// LoadAllDiffContexts returns diff contexts for workdir + all aux dirs
// that have diffable content (:copy, :overlay, or :rw). Read-only dirs are skipped.
func LoadAllDiffContexts(name string) ([]DiffContext, error) {
	sandboxDir, err := RequireSandboxDir(name)
	if err != nil {
		return nil, err
	}

	meta, err := LoadMeta(sandboxDir)
	if err != nil {
		return nil, err
	}

	var contexts []DiffContext

	// Workdir
	switch meta.Workdir.Mode {
	case "copy":
		contexts = append(contexts, DiffContext{
			HostPath:    meta.Workdir.HostPath,
			WorkDir:     WorkDir(name, meta.Workdir.HostPath),
			BaselineSHA: meta.Workdir.BaselineSHA,
			Mode:        "copy",
		})
	case "overlay":
		mountPath := meta.Workdir.MountPath
		if mountPath == "" {
			mountPath = meta.Workdir.HostPath
		}
		contexts = append(contexts, DiffContext{
			HostPath:    meta.Workdir.HostPath,
			WorkDir:     mountPath,
			BaselineSHA: meta.Workdir.BaselineSHA,
			Mode:        "overlay",
		})
	case "rw":
		contexts = append(contexts, DiffContext{
			HostPath: meta.Workdir.HostPath,
			WorkDir:  meta.Workdir.HostPath,
			Mode:     "rw",
		})
	}

	// Aux dirs
	for _, d := range meta.Directories {
		switch d.Mode {
		case "copy":
			contexts = append(contexts, DiffContext{
				HostPath:    d.HostPath,
				WorkDir:     WorkDir(name, d.HostPath),
				BaselineSHA: d.BaselineSHA,
				Mode:        "copy",
			})
		case "overlay":
			mountPath := d.MountPath
			if mountPath == "" {
				mountPath = d.HostPath
			}
			contexts = append(contexts, DiffContext{
				HostPath:    d.HostPath,
				WorkDir:     mountPath,
				BaselineSHA: d.BaselineSHA,
				Mode:        "overlay",
			})
		case "rw":
			contexts = append(contexts, DiffContext{
				HostPath: d.HostPath,
				WorkDir:  d.HostPath,
				Mode:     "rw",
			})
			// "ro" dirs are skipped
		}
	}

	return contexts, nil
}

// GenerateMultiDiff produces diffs for all diffable directories in the sandbox.
// Returns one DiffResult per directory that has changes.
// NOTE: This does not handle :overlay directories. Use GenerateOverlayDiff for overlay mode.
func GenerateMultiDiff(name string, stat bool) ([]*DiffResult, error) {
	contexts, err := LoadAllDiffContexts(name)
	if err != nil {
		return nil, err
	}

	var results []*DiffResult
	for _, dc := range contexts {
		var result *DiffResult
		switch dc.Mode {
		case "rw":
			result, err = workspace.RWDiff(dc.WorkDir, nil, stat)
		case "overlay":
			// Overlay dirs require container exec; skip here
			result = &DiffResult{
				Output: "Diff for :overlay directories requires 'yoloai diff' (runs git inside container)",
				Mode:   "overlay",
				Empty:  true,
			}
		default:
			result, err = workspace.CopyDiff(dc.WorkDir, dc.BaselineSHA, nil, stat)
		}
		if err != nil {
			return nil, fmt.Errorf("diff %s: %w", dc.HostPath, err)
		}
		result.WorkDir = dc.HostPath // use host path for display
		results = append(results, result)
	}

	return results, nil
}

// ListCommitsBeyondBaselineOverlay returns commits beyond the baseline for
// overlay-mode directories by executing git log inside the running container.
func ListCommitsBeyondBaselineOverlay(ctx context.Context, rt runtime.Runtime, name string) ([]CommitInfo, error) {
	contexts, err := LoadAllDiffContexts(name)
	if err != nil {
		return nil, err
	}

	cname := InstanceName(name)
	var commits []CommitInfo

	for _, dc := range contexts {
		if dc.Mode != "overlay" {
			continue
		}

		baselineSHA := dc.BaselineSHA
		if baselineSHA == "" {
			stdout, execErr := execInContainer(ctx, rt, cname, []string{
				"git", "-C", dc.WorkDir, "rev-parse", "HEAD",
			})
			if execErr != nil {
				return nil, fmt.Errorf("resolve baseline SHA for %s: %w", dc.HostPath, execErr)
			}
			baselineSHA = strings.TrimSpace(stdout)
			if updateErr := updateOverlayBaseline(name, dc.HostPath, baselineSHA); updateErr != nil {
				return nil, updateErr
			}
		}

		stdout, err := execInContainer(ctx, rt, cname, []string{
			"git", "-C", dc.WorkDir, "log", "--reverse", "--format=%H %s", baselineSHA + "..HEAD",
		})
		if err != nil {
			return nil, fmt.Errorf("git log in %s: %w", dc.HostPath, err)
		}

		lines := strings.TrimSpace(stdout)
		if lines == "" {
			continue
		}

		for _, line := range strings.Split(lines, "\n") {
			sha, subject, ok := strings.Cut(line, " ")
			if !ok {
				continue
			}
			commits = append(commits, CommitInfo{SHA: sha, Subject: subject})
		}
	}

	return commits, nil
}

// GenerateOverlayDiff generates a diff for overlay-mode directories by
// executing git commands inside the running container.
func GenerateOverlayDiff(ctx context.Context, rt runtime.Runtime, name string, stat bool) ([]*DiffResult, error) {
	contexts, err := LoadAllDiffContexts(name)
	if err != nil {
		return nil, err
	}

	cname := InstanceName(name)
	var results []*DiffResult

	for _, dc := range contexts {
		if dc.Mode != "overlay" {
			// Non-overlay dirs handled by GenerateMultiDiff
			continue
		}

		// Resolve baseline SHA if deferred
		baselineSHA := dc.BaselineSHA
		if baselineSHA == "" {
			stdout, execErr := execInContainer(ctx, rt, cname, []string{
				"git", "-C", dc.WorkDir, "rev-parse", "HEAD",
			})
			if execErr != nil {
				return nil, fmt.Errorf("resolve baseline SHA for %s: %w", dc.HostPath, execErr)
			}
			baselineSHA = strings.TrimSpace(stdout)
			// Update meta.json with resolved SHA
			if updateErr := updateOverlayBaseline(name, dc.HostPath, baselineSHA); updateErr != nil {
				return nil, updateErr
			}
		}

		// Stage untracked files
		_, err := execInContainer(ctx, rt, cname, []string{
			"git", "-C", dc.WorkDir, "add", "-A",
		})
		if err != nil {
			return nil, fmt.Errorf("stage untracked in %s: %w", dc.HostPath, err)
		}

		// Generate diff
		args := []string{"git", "-c", "core.hooksPath=/dev/null", "-C", dc.WorkDir, "diff"}
		if stat {
			args = append(args, "--stat")
		} else {
			args = append(args, "--binary")
		}
		args = append(args, baselineSHA)

		stdout, err := execInContainer(ctx, rt, cname, args)
		if err != nil {
			return nil, fmt.Errorf("git diff in %s: %w", dc.HostPath, err)
		}

		result := strings.TrimRight(stdout, "\n")
		results = append(results, &DiffResult{
			Output:  result,
			WorkDir: dc.HostPath,
			Mode:    "overlay",
			Empty:   len(result) == 0,
		})
	}

	return results, nil
}
