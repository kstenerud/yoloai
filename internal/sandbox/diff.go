package sandbox

import (
	"fmt"
	"strings"
)

// DiffOptions controls diff generation.
type DiffOptions struct {
	Name  string   // sandbox name
	Stat  bool     // true for --stat summary only
	Paths []string // optional path filter (relative to workdir)
}

// DiffResult holds the output of a diff operation.
type DiffResult struct {
	Output  string // diff text or stat summary
	WorkDir string // work directory that was diffed
	Mode    string // "copy" or "rw"
	Empty   bool   // true if no changes detected
}

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

	if mode == "rw" {
		return generateRWDiff(workDir, opts.Paths, stat)
	}

	return generateCopyDiff(workDir, baselineSHA, opts.Paths, stat)
}

func generateRWDiff(workDir string, paths []string, stat bool) (*DiffResult, error) {
	if !IsGitRepo(workDir) {
		return &DiffResult{
			Output: "Diff not available: " + workDir + " is not a git repository (live-mounted :rw directory)",
			Mode:   "rw",
			Empty:  true,
		}, nil
	}

	args := []string{"diff"}
	if stat {
		args = append(args, "--stat")
	}
	args = append(args, "HEAD")
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}

	cmd := newGitCmd(workDir, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}

	result := strings.TrimRight(string(output), "\n")
	return &DiffResult{
		Output:  result,
		WorkDir: workDir,
		Mode:    "rw",
		Empty:   len(result) == 0,
	}, nil
}

func generateCopyDiff(workDir, baselineSHA string, paths []string, stat bool) (*DiffResult, error) {
	if err := stageUntracked(workDir); err != nil {
		return nil, err
	}

	args := []string{"diff"}
	if stat {
		args = append(args, "--stat")
	} else {
		args = append(args, "--binary")
	}
	args = append(args, baselineSHA)
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}

	cmd := newGitCmd(workDir, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}

	result := strings.TrimRight(string(output), "\n")
	return &DiffResult{
		Output:  result,
		WorkDir: workDir,
		Mode:    "copy",
		Empty:   len(result) == 0,
	}, nil
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

	if err := stageUntracked(workDir); err != nil {
		return nil, err
	}

	args := []string{"diff"}
	if opts.Stat {
		args = append(args, "--stat")
	} else {
		args = append(args, "--binary")
	}

	// Single SHA → show that commit's diff (sha~1..sha)
	// Range "a..b" → pass directly
	if strings.Contains(opts.Ref, "..") {
		args = append(args, opts.Ref)
	} else {
		args = append(args, opts.Ref+"~1", opts.Ref)
	}

	cmd := newGitCmd(workDir, args...)
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
	Stat string // output of git diff --stat for this commit
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
		cmd := newGitCmd(workDir, "diff", "--stat", c.SHA+"~1", c.SHA)
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

// stageUntracked runs `git add -A` in the work directory to capture
// files created by the agent that are not yet tracked.
func stageUntracked(workDir string) error {
	return runGitCmd(workDir, "add", "-A")
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
	case "rw":
		workDir = meta.Workdir.HostPath
		baselineSHA = "HEAD"
	default:
		return "", "", "", fmt.Errorf("unsupported workdir mode: %s", mode)
	}

	return workDir, baselineSHA, mode, nil
}
