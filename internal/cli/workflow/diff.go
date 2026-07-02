// ABOUTME: Cobra "diff" command: shows agent changes as a unified diff, commit
// ABOUTME: log, or per-ref diff; handles :copy and multi-directory sandboxes.
package workflow

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// reHexPrefix matches strings that look like hex SHA prefixes (4+ hex chars).
var reHexPrefix = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)

// reHexRange matches "hex..hex" range syntax.
var reHexRange = regexp.MustCompile(`^[0-9a-fA-F]{4,40}\.\.[0-9a-fA-F]{4,40}$`)

func NewDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff <name> [<dir> | --all] [<ref>] [-- <path>...]",
		Short: "Show changes the agent made",
		Long: `Show changes the agent made in a sandbox.

By default shows the full diff since baseline. With --log, lists
individual agent commits. With a ref argument, shows a specific
commit or range.

<dir> is required when the sandbox tracks 2+ directories; it may be a
basename, a path suffix, an exact host path, or an exact mount path.

Examples:
  yoloai diff mybox                  # full diff
  yoloai diff mybox --log            # list commits
  yoloai diff mybox --log --stat     # list commits with file stats
  yoloai diff mybox abc123           # single commit diff
  yoloai diff mybox abc1..def4       # range diff
  yoloai diff mybox -- src/          # full diff filtered to path
  yoloai diff mybox web              # diff of "web" dir (multi-dir sandbox)
  yoloai diff mybox web abc123       # single commit diff in "web" dir
  yoloai diff mybox --all                # diff of all tracked dirs`,
		GroupID: cliutil.GroupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE:    runDiffCmd,
	}

	cmd.Flags().Bool("stat", false, "Show summary (files changed, insertions, deletions)")
	cmd.Flags().Bool("name-only", false, "List changed files without content")
	cmd.Flags().Bool("log", false, "List agent commits beyond baseline")
	cmd.Flags().Bool("all", false, "operate on all tracked directories")

	cmd.MarkFlagsMutuallyExclusive("stat", "name-only")

	return cmd
}

func runDiffCmd(cmd *cobra.Command, args []string) error {
	name, rest, err := cliutil.ResolveName(cmd, args)
	if err != nil {
		return err
	}
	defer cliutil.OpenCLIJSONLSink(name, cmd)()

	stat, _ := cmd.Flags().GetBool("stat")
	nameOnly, _ := cmd.Flags().GetBool("name-only")
	logFlag, _ := cmd.Flags().GetBool("log")
	allFlag, _ := cmd.Flags().GetBool("all")

	// Load meta early to select the target dir.
	env, metaErr := cliutil.SandboxMetadata(cmd, name)
	if metaErr != nil {
		return metaErr
	}
	// argsConsumedBeforeRest tracks how many positional args from the original
	// args slice have been consumed before rest: 1 for name, +1 if SelectTrackedDir
	// consumed a dir specifier. parseDiffArgs needs this to adjust ArgsLenAtDash.
	argsConsumedBeforeRest := 1

	if allFlag {
		return diffAll(cmd, name, rest, logFlag, stat, nameOnly)
	}

	hostPath, selectedDir, rest, err := cliutil.SelectTrackedDir(env, rest)
	if err != nil {
		return err
	}
	if hostPath != "" {
		argsConsumedBeforeRest = 2
	}
	slog.Debug("generating diff", "event", "sandbox.diff", "sandbox", name, "workdir_mode", selectedDir.Mode)

	// Skip agent warning in JSON mode
	if !cliutil.JSONEnabled(cmd) {
		agentRunningWarning(cmd, name)
	}

	// --log: list commits
	if logFlag {
		if cliutil.JSONEnabled(cmd) {
			return diffLogJSON(cmd, name, hostPath, stat)
		}
		return diffLog(cmd, name, hostPath, stat)
	}

	// Parse ref vs paths: split on "--" if present, otherwise
	// try to detect ref from the first positional arg.
	ref, paths := parseDiffArgs(rest, cmd, argsConsumedBeforeRest)

	// If ref is set, show that specific commit/range
	if ref != "" {
		return diffRef(cmd, name, hostPath, ref, stat)
	}

	return diffSingle(cmd, name, hostPath, paths, stat, nameOnly)
}

// trackedDirHandle returns the Workdir handle for hostPath ("" = primary workdir).
func trackedDirHandle(sb *yoloai.Sandbox, hostPath string) (*yoloai.Workdir, error) {
	if hostPath == "" {
		return sb.Workdir(), nil
	}
	return sb.TrackedDir(hostPath)
}

// diffSingle runs a diff for the sandbox's selected workdir.
func diffSingle(cmd *cobra.Command, name, hostPath string, paths []string, stat, nameOnly bool) error {
	return cliutil.WithTrackedDir(cmd, name, hostPath, func(ctx context.Context, wd *yoloai.Workdir) error {
		out, err := wd.Diff(ctx, yoloai.WorkdirDiffOptions{Paths: paths, Stat: stat, NameOnly: nameOnly})
		if err != nil {
			return err
		}
		// For a full diff in JSON mode, enrich the output with structured per-file
		// change counts. Stat and NameOnly remain plain (they're already structured
		// summaries and the caller picked them explicitly).
		if cliutil.JSONEnabled(cmd) && !stat && !nameOnly {
			changes, changesErr := wd.Changes(ctx)
			if changesErr == nil {
				return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
					"diff":      out,
					"files":     changes.Files,
					"additions": changes.Additions,
					"deletions": changes.Deletions,
				})
			}
			// Fall back to the plain diff envelope if changes fetch fails.
		}
		return writeDiffOutput(cmd, out)
	})
}

// writeDiffOutput emits a diff string to stdout, normalizing the
// "no changes" case (empty string → "No changes" in human mode, an
// empty JSON object in --json mode) so every diff entry point handles
// it the same way.
func writeDiffOutput(cmd *cobra.Command, out string) error {
	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{"diff": out})
	}
	if out == "" {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No changes")
		return err
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), out)
	return err
}

// parseDiffArgs separates a ref argument from path arguments.
// If "--" is present in the raw args (via cobra's ArgsLenAtDash), everything
// after it is paths and everything before is a potential ref.
// Without "--", the first arg is tried as a commit ref (hex pattern);
// if it doesn't match, all args are treated as paths.
// argsConsumed is the number of positional args consumed from the original
// args slice before rest (1 for name-only, 2 for name+dir-specifier).
func parseDiffArgs(rest []string, cmd *cobra.Command, argsConsumed int) (ref string, paths []string) {
	if len(rest) == 0 {
		return "", nil
	}

	// Check for explicit "--" separator in original args
	dashAt := cmd.ArgsLenAtDash()
	if dashAt >= 0 {
		// Args before dash (excluding args already consumed).
		beforeDash := min(
			max(dashAt-argsConsumed, 0), len(rest))

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
func diffLog(cmd *cobra.Command, name, hostPath string, stat bool) error {
	out := cmd.OutOrStdout()

	// Fetch tags for inline display (best-effort).
	var tags []yoloai.TagInfo

	_ = cliutil.WithTrackedDir(cmd, name, hostPath, func(ctx context.Context, wd *yoloai.Workdir) error {
		tags, _ = wd.Tags(ctx, yoloai.WorkdirTagsOptions{})
		return nil
	})
	tagsByCommit := buildTagsByCommit(tags)

	if stat {
		if err := diffLogWithStat(cmd, name, hostPath, out, tagsByCommit); err != nil {
			return err
		}
	} else {
		if err := diffLogBasic(cmd, name, hostPath, out, tagsByCommit); err != nil {
			return err
		}
	}

	diffLogUncommitted(cmd, name, hostPath, out)
	return nil
}

// diffLogWithStat prints commits with file-change statistics.
func diffLogWithStat(cmd *cobra.Command, name, hostPath string, out io.Writer, tagsByCommit map[string][]string) error {
	var commits []yoloai.CommitInfo
	err := cliutil.WithTrackedDir(cmd, name, hostPath, func(ctx context.Context, wd *yoloai.Workdir) error {
		var listErr error
		commits, listErr = wd.Commits(ctx, yoloai.WorkdirCommitsOptions{Stat: true})
		return listErr
	})
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		_, err = fmt.Fprintln(out, "No commits beyond baseline")
		return err
	}
	for i, c := range commits {
		line := formatCommitLine(i+1, c.SHA, c.Subject, tagsByCommit)
		fmt.Fprintln(out, line) //nolint:errcheck
		if c.Stat != "" {
			for statLine := range strings.SplitSeq(c.Stat, "\n") {
				fmt.Fprintf(out, "     %s\n", statLine) //nolint:errcheck
			}
		}
	}
	return nil
}

// diffLogBasic prints commits without statistics.
func diffLogBasic(cmd *cobra.Command, name, hostPath string, out io.Writer, tagsByCommit map[string][]string) error {
	var commits []yoloai.CommitInfo
	err := cliutil.WithTrackedDir(cmd, name, hostPath, func(ctx context.Context, wd *yoloai.Workdir) error {
		var listErr error
		commits, listErr = wd.Commits(ctx, yoloai.WorkdirCommitsOptions{})
		return listErr
	})
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		_, err = fmt.Fprintln(out, "No commits beyond baseline")
		return err
	}
	for i, c := range commits {
		fmt.Fprintln(out, formatCommitLine(i+1, c.SHA, c.Subject, tagsByCommit)) //nolint:errcheck
	}
	return nil
}

// formatCommitLine formats a single commit log line with optional tag annotation.
func formatCommitLine(n int, sha, subject string, tagsByCommit map[string][]string) string {
	line := fmt.Sprintf("%3d  %.12s  %s", n, sha, subject)
	if names := tagsByCommit[strings.ToLower(sha)]; len(names) > 0 {
		line += "  [tag: " + strings.Join(names, ", ") + "]"
	}
	return line
}

// diffLogUncommitted appends an uncommitted-changes indicator when present (best-effort).
func diffLogUncommitted(cmd *cobra.Command, name, hostPath string, out io.Writer) {
	var hasUncommitted bool
	err := cliutil.WithTrackedDir(cmd, name, hostPath, func(ctx context.Context, wd *yoloai.Workdir) error {
		var uncommittedErr error
		hasUncommitted, uncommittedErr = wd.HasUncommittedChanges(ctx)
		return uncommittedErr
	})
	if err == nil && hasUncommitted {
		fmt.Fprintln(out, "  *  (uncommitted changes)") //nolint:errcheck
	}
}

// diffRef shows the diff for a specific commit or range. Disk-only; no
// runtime needed, but routed through WithClient for symmetry with the
// other diff handlers.
func diffRef(cmd *cobra.Command, name, hostPath, ref string, stat bool) error {
	return cliutil.WithTrackedDir(cmd, name, hostPath, func(ctx context.Context, wd *yoloai.Workdir) error {
		out, err := wd.Diff(ctx, yoloai.WorkdirDiffOptions{Ref: ref, Stat: stat})
		if err != nil {
			return err
		}
		return writeDiffOutput(cmd, out)
	})
}

// agentRunningWarning prints a warning to stderr if the agent is still running.
// Silently skips if Docker is unavailable or inspection fails.
func agentRunningWarning(cmd *cobra.Command, name string) {

	_ = cliutil.WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		info, err := sb.Inspect(ctx)
		if err != nil {
			return nil //nolint:nilerr // best-effort warning; inspection failure should not affect the diff command
		}

		if info.Status == yoloai.StatusActive || info.Status == yoloai.StatusIdle {
			fmt.Fprintln(cmd.ErrOrStderr(), "Note: agent is still running; diff may be incomplete") //nolint:errcheck // best-effort warning
		}
		return nil
	})
}

// diffLogJSON outputs commit log as JSON.
func diffLogJSON(cmd *cobra.Command, name, hostPath string, stat bool) error {
	return cliutil.WithTrackedDir(cmd, name, hostPath, func(ctx context.Context, wd *yoloai.Workdir) error {
		cs, err := wd.Commits(ctx, yoloai.WorkdirCommitsOptions{Stat: stat})
		if err != nil {
			return err
		}
		if cs == nil {
			cs = []yoloai.CommitInfo{}
		}

		hasUncommitted, _ := wd.HasUncommittedChanges(ctx)
		tags, _ := wd.Tags(ctx, yoloai.WorkdirTagsOptions{})
		if tags == nil {
			tags = []yoloai.TagInfo{}
		}

		result := struct {
			Commits               []yoloai.CommitInfo `json:"commits"`
			HasUncommittedChanges bool                `json:"has_uncommitted_changes"`
			Tags                  []yoloai.TagInfo    `json:"tags"`
		}{
			Commits:               cs,
			HasUncommittedChanges: hasUncommitted,
			Tags:                  tags,
		}

		return cliutil.WriteJSON(cmd.OutOrStdout(), result)
	})
}

// diffAll generates a diff across all tracked directories.
// For full diff (not stat/nameOnly): copy-mode dirs use PathPrefix for absolute
// paths (no banner). For stat/nameOnly: all dirs get a banner.
func diffAll(cmd *cobra.Command, name string, rest []string, logFlag, stat, nameOnly bool) error {
	if logFlag {
		return yoerrors.NewUsageError("--log is per-commit; use a single dir specifier with --all")
	}
	if len(rest) > 0 {
		return yoerrors.NewUsageError("--all does not accept additional arguments; use a single dir specifier for refs or path filters")
	}
	env, err := cliutil.SandboxMetadata(cmd, name)
	if err != nil {
		return err
	}
	tracked := env.TrackedDirs()
	if len(tracked) == 0 {
		return writeDiffOutput(cmd, "")
	}

	var combined strings.Builder
	return cliutil.WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		cliutil.ReconcileInjectorBestEffort(ctx, sb) // D106: revive a crashed injector
		for _, d := range tracked {
			if appendErr := diffOneDirAll(ctx, sb, d, stat, nameOnly, &combined); appendErr != nil {
				return appendErr
			}
		}
		return writeDiffOutput(cmd, strings.TrimRight(combined.String(), "\n"))
	})
}

// diffOneDirAll diffs a single tracked directory and appends its output to
// combined. Copy-mode dirs use PathPrefix for absolute paths;
// stat/nameOnly output gets a banner header.
func diffOneDirAll(ctx context.Context, sb *yoloai.Sandbox, d yoloai.DirInfo, stat, nameOnly bool, combined *strings.Builder) error {
	wd, wdErr := sb.TrackedDir(d.HostPath)
	if wdErr != nil {
		return wdErr
	}
	diffOpts := yoloai.WorkdirDiffOptions{Stat: stat, NameOnly: nameOnly}
	if !stat && !nameOnly {
		diffOpts.PathPrefix = d.HostPath + "/"
	}
	out, diffErr := wd.Diff(ctx, diffOpts)
	if diffErr != nil {
		return diffErr
	}
	if out == "" {
		return nil
	}
	if stat || nameOnly {
		fmt.Fprintf(combined, "=== %s ===\n", d.HostPath)
	}
	combined.WriteString(out)
	combined.WriteByte('\n')
	return nil
}
