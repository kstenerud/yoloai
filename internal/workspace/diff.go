// ABOUTME: CopyDiff and RWDiff: generate git diffs for :copy / :rw workdirs.
// ABOUTME: Both return (string, error) where empty string means no changes.
package workspace

import (
	"fmt"
	"strings"
)

// CopyDiff generates a diff for a :copy mode work directory against
// a baseline SHA. Stages untracked files first, then runs git diff.
//
// Returns the diff text (empty string if there are no changes).
func CopyDiff(workDir, baselineSHA string, paths []string, stat, nameOnly bool) (string, error) {
	if err := StageUntracked(workDir); err != nil {
		return "", err
	}

	args := []string{"diff"}
	switch {
	case nameOnly:
		args = append(args, "--name-only")
	case stat:
		args = append(args, "--stat")
	default:
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
		return "", fmt.Errorf("git diff: %w", err)
	}

	return strings.TrimRight(string(output), "\n"), nil
}

// RWDiff generates a diff for a :rw mode directory. Returns an empty
// string (no error) when the directory isn't a git repo — :rw can
// point at a non-git tree and the caller should treat "no diff
// available" the same as "no changes" for display purposes.
func RWDiff(workDir string, paths []string, stat, nameOnly bool) (string, error) {
	if !IsGitRepo(workDir) {
		return "", nil
	}

	args := []string{"diff"}
	switch {
	case nameOnly:
		args = append(args, "--name-only")
	case stat:
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
		return "", fmt.Errorf("git diff: %w", err)
	}

	return strings.TrimRight(string(output), "\n"), nil
}
