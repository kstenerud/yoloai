package workspace

import (
	"fmt"
	"strings"
)

// DiffResult holds the output of a diff operation.
type DiffResult struct {
	Output  string `json:"output"`  // diff text or stat summary
	WorkDir string `json:"workdir"` // work directory that was diffed
	Mode    string `json:"mode"`    // "copy", "overlay", or "rw"
	Empty   bool   `json:"empty"`   // true if no changes detected
}

// CopyDiff generates a diff for a :copy mode work directory against
// a baseline SHA. Stages untracked files first, then runs git diff.
func CopyDiff(workDir, baselineSHA string, paths []string, stat bool) (*DiffResult, error) {
	if err := StageUntracked(workDir); err != nil {
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

	cmd := NewGitCmd(workDir, args...)
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

// RWDiff generates a diff for a :rw mode directory. Returns an
// informational result (not error) if the directory is not a git repo.
func RWDiff(workDir string, paths []string, stat bool) (*DiffResult, error) {
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

	cmd := NewGitCmd(workDir, args...)
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
