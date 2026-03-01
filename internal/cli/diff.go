package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// reHexPrefix matches strings that look like hex SHA prefixes (4+ hex chars).
var reHexPrefix = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)

// reHexRange matches "hex..hex" range syntax.
var reHexRange = regexp.MustCompile(`^[0-9a-fA-F]{4,40}\.\.[0-9a-fA-F]{4,40}$`)

func newDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff <name> [<ref>] [-- <path>...]",
		Short: "Show changes the agent made",
		Long: `Show changes the agent made in a sandbox.

By default shows the full diff since baseline. With --log, lists
individual agent commits. With a ref argument, shows a specific
commit or range.

Examples:
  yoloai diff mybox                  # full diff
  yoloai diff mybox --log            # list commits
  yoloai diff mybox --log --stat     # list commits with file stats
  yoloai diff mybox abc123           # single commit diff
  yoloai diff mybox abc1..def4       # range diff
  yoloai diff mybox -- src/          # full diff filtered to path`,
		GroupID: groupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, rest, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			stat, _ := cmd.Flags().GetBool("stat")
			logFlag, _ := cmd.Flags().GetBool("log")

			// Skip agent warning in JSON mode
			if !jsonEnabled(cmd) {
				agentRunningWarning(cmd, name)
			}

			// --log: list commits
			if logFlag {
				if jsonEnabled(cmd) {
					return diffLogJSON(cmd, name, stat)
				}
				return diffLog(cmd, name, stat)
			}

			// Parse ref vs paths: split on "--" if present, otherwise
			// try to detect ref from the first positional arg.
			ref, paths := parseDiffArgs(rest, cmd)

			// If ref is set, show that specific commit/range
			if ref != "" {
				return diffRef(cmd, name, ref, stat)
			}

			// Default: monolithic diff
			// Check if sandbox has aux dirs — if so, use multi-dir diff
			meta, metaErr := sandbox.LoadMeta(sandbox.Dir(name))
			if metaErr != nil {
				return metaErr
			}

			if len(meta.Directories) > 0 && len(paths) == 0 {
				if jsonEnabled(cmd) {
					return diffMultiDirJSON(cmd, name, stat)
				}
				return diffMultiDir(cmd, name, stat)
			}

			opts := sandbox.DiffOptions{
				Name:  name,
				Paths: paths,
			}

			if stat {
				result, err := sandbox.GenerateDiffStat(opts)
				if err != nil {
					return err
				}
				if jsonEnabled(cmd) {
					return writeJSON(cmd.OutOrStdout(), result)
				}
				if result.Empty {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes")
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Output)
				return err
			}

			result, err := sandbox.GenerateDiff(opts)
			if err != nil {
				return err
			}
			if jsonEnabled(cmd) {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			if result.Empty {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes")
				return err
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Output)
			return err
		},
	}

	cmd.Flags().Bool("stat", false, "Show summary (files changed, insertions, deletions)")
	cmd.Flags().Bool("log", false, "List agent commits beyond baseline")

	return cmd
}

// parseDiffArgs separates a ref argument from path arguments.
// If "--" is present in the raw args (via cobra's ArgsLenAtDash), everything
// after it is paths and everything before is a potential ref.
// Without "--", the first arg is tried as a commit ref (hex pattern);
// if it doesn't match, all args are treated as paths.
func parseDiffArgs(rest []string, cmd *cobra.Command) (ref string, paths []string) {
	if len(rest) == 0 {
		return "", nil
	}

	// Check for explicit "--" separator in original args
	dashAt := cmd.ArgsLenAtDash()
	if dashAt >= 0 {
		// Args before dash (excluding name which was already consumed)
		// rest was already after name, so dashAt-1 gives us how many
		// of rest are before the dash. But ArgsLenAtDash counts from
		// the full args array. We need to adjust: name consumed 1 arg.
		beforeDash := dashAt - 1 // how many of rest[] are before "--"
		if beforeDash < 0 {
			beforeDash = 0
		}
		if beforeDash > len(rest) {
			beforeDash = len(rest)
		}

		// Everything before dash is the ref (at most 1)
		if beforeDash > 0 {
			ref = rest[0]
		}
		// Everything after dash is paths
		if beforeDash < len(rest) {
			paths = rest[beforeDash:]
		}
		return ref, paths
	}

	// No "--": try to detect if first arg is a ref
	first := rest[0]
	if looksLikeRef(first) {
		return first, rest[1:]
	}

	// All args are paths
	return "", rest
}

// looksLikeRef returns true if s looks like a commit ref (hex SHA or range).
func looksLikeRef(s string) bool {
	return reHexPrefix.MatchString(s) || reHexRange.MatchString(s)
}

// diffLog lists commits beyond baseline.
func diffLog(cmd *cobra.Command, name string, stat bool) error {
	out := cmd.OutOrStdout()

	if stat {
		commits, err := sandbox.ListCommitsWithStats(name)
		if err != nil {
			return err
		}
		if len(commits) == 0 {
			_, err = fmt.Fprintln(out, "No commits beyond baseline")
			return err
		}
		for i, c := range commits {
			fmt.Fprintf(out, "%3d  %.12s  %s\n", i+1, c.SHA, c.Subject) //nolint:errcheck
			if c.Stat != "" {
				for _, line := range strings.Split(c.Stat, "\n") {
					fmt.Fprintf(out, "     %s\n", line) //nolint:errcheck
				}
			}
		}
	} else {
		commits, err := sandbox.ListCommitsBeyondBaseline(name)
		if err != nil {
			return err
		}
		if len(commits) == 0 {
			_, err = fmt.Fprintln(out, "No commits beyond baseline")
			return err
		}
		for i, c := range commits {
			fmt.Fprintf(out, "%3d  %.12s  %s\n", i+1, c.SHA, c.Subject) //nolint:errcheck
		}
	}

	// Check for uncommitted changes
	hasWIP, err := sandbox.HasUncommittedChanges(name)
	if err == nil && hasWIP {
		fmt.Fprintln(out, "  *  (uncommitted changes)") //nolint:errcheck
	}

	return nil
}

// diffRef shows the diff for a specific commit or range.
func diffRef(cmd *cobra.Command, name, ref string, stat bool) error {
	result, err := sandbox.GenerateCommitDiff(sandbox.CommitDiffOptions{
		Name: name,
		Ref:  ref,
		Stat: stat,
	})
	if err != nil {
		return err
	}

	if jsonEnabled(cmd) {
		return writeJSON(cmd.OutOrStdout(), result)
	}

	if result.Empty {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes")
		return err
	}

	if stat {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Output)
		return err
	}

	_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Output)
	return err
}

// agentRunningWarning prints a warning to stderr if the agent is still running.
// Silently skips if Docker is unavailable or inspection fails.
func agentRunningWarning(cmd *cobra.Command, name string) {
	backend := resolveBackendForSandbox(name)
	_ = withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		info, err := sandbox.InspectSandbox(ctx, rt, name)
		if err != nil {
			return nil // silently skip
		}

		if info.Status == sandbox.StatusRunning {
			fmt.Fprintln(cmd.ErrOrStderr(), "Note: agent is still running; diff may be incomplete") //nolint:errcheck // best-effort warning
		}
		return nil
	})
}

// diffMultiDir shows diffs for all diffable directories with per-dir headers.
func diffMultiDir(cmd *cobra.Command, name string, stat bool) error {
	results, err := sandbox.GenerateMultiDiff(name, stat)
	if err != nil {
		return err
	}

	allEmpty := true
	for _, r := range results {
		if !r.Empty {
			allEmpty = false
			break
		}
	}

	if allEmpty {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes")
		return err
	}

	var sb strings.Builder
	for _, r := range results {
		if r.Empty {
			continue
		}
		fmt.Fprintf(&sb, "=== %s (%s) ===\n", r.WorkDir, r.Mode)
		sb.WriteString(r.Output)
		sb.WriteString("\n\n")
	}

	output := strings.TrimRight(sb.String(), "\n") + "\n"
	if stat {
		_, err = fmt.Fprint(cmd.OutOrStdout(), output)
		return err
	}

	_, err = fmt.Fprint(cmd.OutOrStdout(), output)
	return err
}

// diffLogJSON outputs commit log as JSON.
func diffLogJSON(cmd *cobra.Command, name string, stat bool) error {
	var commits any
	if stat {
		c, err := sandbox.ListCommitsWithStats(name)
		if err != nil {
			return err
		}
		if c == nil {
			c = []sandbox.CommitInfoWithStat{}
		}
		commits = c
	} else {
		c, err := sandbox.ListCommitsBeyondBaseline(name)
		if err != nil {
			return err
		}
		if c == nil {
			c = []sandbox.CommitInfo{}
		}
		commits = c
	}

	hasWIP, _ := sandbox.HasUncommittedChanges(name)

	result := struct {
		Commits               any  `json:"commits"`
		HasUncommittedChanges bool `json:"has_uncommitted_changes"`
	}{
		Commits:               commits,
		HasUncommittedChanges: hasWIP,
	}

	return writeJSON(cmd.OutOrStdout(), result)
}

// diffMultiDirJSON outputs multi-directory diffs as JSON.
func diffMultiDirJSON(cmd *cobra.Command, name string, stat bool) error {
	results, err := sandbox.GenerateMultiDiff(name, stat)
	if err != nil {
		return err
	}
	if results == nil {
		results = []*sandbox.DiffResult{}
	}
	return writeJSON(cmd.OutOrStdout(), results)
}
