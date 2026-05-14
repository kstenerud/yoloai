// ABOUTME: Implements the `yoloai baseline` command with `advance`, `set`, and
// ABOUTME: `log` subcommands for manually correcting stuck sandbox baselines
// ABOUTME: after stash-pop conflicts or non-contiguous selective applies.
package cli

import (
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/workspace"
	"github.com/spf13/cobra"
)

func newBaselineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "baseline <subcommand>",
		Short:   "Manage sandbox baseline SHA",
		GroupID: groupWorkflow,
	}
	cmd.AddCommand(newBaselineAdvanceCmd(), newBaselineSetCmd(), newBaselineLogCmd())
	return cmd
}

// newBaselineAdvanceCmd implements `yoloai baseline advance <name>`.
// It moves the baseline to the current HEAD of the sandbox work copy.
func newBaselineAdvanceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "advance <name>",
		Short: "Move baseline to current HEAD of the sandbox work copy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			meta, workDir, err := loadBaselineContext(name)
			if err != nil {
				return err
			}

			sha, err := workspace.HeadSHA(workDir)
			if err != nil {
				return fmt.Errorf("resolve HEAD: %w", err)
			}
			return advanceBaselineAndPrint(cmd, name, meta.Workdir.BaselineSHA, sha, workDir)
		},
	}
}

// newBaselineSetCmd implements `yoloai baseline set <name> <sha>`.
// It moves the baseline to the given commit (short SHA accepted).
func newBaselineSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name> <sha>",
		Short: "Move baseline to a specific commit SHA",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, shortSHA := args[0], args[1]
			meta, workDir, err := loadBaselineContext(name)
			if err != nil {
				return err
			}

			// Resolve short SHA to full SHA via git.
			out, err := workspace.NewGitCmd(workDir, "rev-parse", shortSHA).Output()
			if err != nil {
				return fmt.Errorf("resolve sha %q: %w", shortSHA, err)
			}
			sha := strings.TrimSpace(string(out))

			return advanceBaselineAndPrint(cmd, name, meta.Workdir.BaselineSHA, sha, workDir)
		},
	}
}

// loadBaselineContext loads the sandbox metadata, validates that the workdir
// mode supports baseline management (:copy only), and returns the work copy
// path on the host. Returns a clear user-facing error for :rw and :overlay.
func loadBaselineContext(name string) (*sandbox.Meta, string, error) {
	meta, err := sandbox.LoadMeta(sandbox.Dir(name))
	if err != nil {
		return nil, "", sandboxErrorHint(name, err)
	}

	switch meta.Workdir.Mode {
	case "rw":
		return nil, "", sandbox.NewUsageError("baseline is not tracked for :rw directories")
	case "overlay":
		return nil, "", sandbox.NewUsageError("use git commands inside the container to manage overlay baselines")
	}

	workDir := sandbox.WorkDir(name, meta.Workdir.HostPath)
	return meta, workDir, nil
}

// advanceBaselineAndPrint updates the baseline to sha and prints a
// confirmation line: "Baseline advanced to <short-sha> (<subject>)".
// If oldSHA is non-empty, it also prints a "Previous baseline" undo hint.
func advanceBaselineAndPrint(cmd *cobra.Command, name, oldSHA, sha, workDir string) error {
	if err := sandbox.AdvanceBaselineTo(name, sha); err != nil {
		return fmt.Errorf("update baseline: %w", err)
	}

	// Fetch the commit subject for the confirmation message.
	out, err := workspace.NewGitCmd(workDir, "log", "--format=%s", "-1", sha).Output()
	if err != nil {
		// Non-fatal: print without subject.
		_, werr := fmt.Fprintf(cmd.OutOrStdout(), "Baseline advanced to %.8s\n", sha)
		return werr
	}
	subject := strings.TrimSpace(string(out))
	shortSHA := sha
	if len(shortSHA) > 8 {
		shortSHA = shortSHA[:8]
	}
	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "Baseline advanced to %s (%s)\n", shortSHA, subject); err != nil {
		return err
	}
	if oldSHA != "" {
		oldShort := oldSHA
		if len(oldShort) > 8 {
			oldShort = oldShort[:8]
		}
		_, werr := fmt.Fprintf(w, "Previous baseline: %s  — to undo: yoloai baseline set %s %s\n", oldShort, name, oldShort)
		return werr
	}
	return nil
}

// newBaselineLogCmd implements `yoloai baseline log <name>`.
// It prints the sandbox work copy's git log, bounded at the baseline commit.
// When a baseline is set, only commits from the baseline onward (inclusive)
// are shown — this avoids dumping the full pre-session project history for
// large repos. Without a baseline, the full log is shown.
func newBaselineLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "log <name>",
		Short: "Show commit log of the sandbox work copy, marking the current baseline",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			meta, workDir, err := loadBaselineContext(name)
			if err != nil {
				return err
			}

			baselineSHA := meta.Workdir.BaselineSHA
			w := cmd.OutOrStdout()

			// When a baseline is set, show only commits beyond it plus the
			// baseline itself. This avoids loading the full pre-session history.
			var logArgs []string
			if baselineSHA != "" {
				logArgs = []string{"log", "--format=%H %s", baselineSHA + "..HEAD"}
			} else {
				logArgs = []string{"log", "--format=%H %s"}
			}

			out, err := workspace.NewGitCmd(workDir, logArgs...).Output()
			if err != nil {
				return fmt.Errorf("git log: %w", err)
			}

			if err := printLogLines(w, string(out), ""); err != nil {
				return err
			}

			// Append the baseline commit itself with the ← baseline marker.
			if baselineSHA != "" {
				baseOut, err := workspace.NewGitCmd(workDir, "log", "--format=%H %s", "-1", baselineSHA).Output()
				if err != nil {
					return fmt.Errorf("git log baseline: %w", err)
				}
				return printLogLines(w, string(baseOut), baselineSHA)
			}
			return nil
		},
	}
}

// printLogLines prints git log output (one "%H %s" line per commit), marking
// the commit whose full SHA matches markerSHA with "  ← baseline".
func printLogLines(w interface{ Write([]byte) (int, error) }, output, markerSHA string) error {
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		idx := strings.IndexByte(line, ' ')
		var fullSHA, subject string
		if idx < 0 {
			fullSHA, subject = line, ""
		} else {
			fullSHA, subject = line[:idx], line[idx+1:]
		}
		shortSHA := fullSHA
		if len(shortSHA) > 8 {
			shortSHA = shortSHA[:8]
		}
		marker := ""
		if markerSHA != "" && fullSHA == markerSHA {
			marker = "  ← baseline"
		}
		if _, err := fmt.Fprintf(w, "%s  %s%s\n", shortSHA, subject, marker); err != nil {
			return err
		}
	}
	return nil
}
