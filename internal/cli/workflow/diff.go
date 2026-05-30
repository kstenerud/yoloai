// ABOUTME: Cobra "diff" command: shows agent changes as a unified diff, commit
// ABOUTME: log, or per-ref diff; handles :copy, :overlay, and multi-directory sandboxes.
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
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// reHexPrefix matches strings that look like hex SHA prefixes (4+ hex chars).
var reHexPrefix = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)

// reHexRange matches "hex..hex" range syntax.
var reHexRange = regexp.MustCompile(`^[0-9a-fA-F]{4,40}\.\.[0-9a-fA-F]{4,40}$`)

func NewDiffCmd() *cobra.Command {
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
		GroupID: cliutil.GroupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE:    runDiffCmd,
	}

	cmd.Flags().Bool("stat", false, "Show summary (files changed, insertions, deletions)")
	cmd.Flags().Bool("name-only", false, "List changed files without content")
	cmd.Flags().Bool("log", false, "List agent commits beyond baseline")

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

	// Load meta early to detect overlay dirs
	meta, metaErr := store.LoadMeta(cliutil.Layout().SandboxDir(name))
	if metaErr != nil {
		return cliutil.SandboxErrorHint(name, metaErr)
	}
	overlay := hasOverlayDirs(meta)
	slog.Debug("generating diff", "event", "sandbox.diff", "sandbox", name, "workdir_mode", meta.Workdir.Mode) //nolint:gosec // G706: name is validated by ValidateName

	// Skip agent warning in JSON mode
	if !cliutil.JSONEnabled(cmd) {
		agentRunningWarning(cmd, name)
	}

	// --log: list commits
	if logFlag {
		if overlay {
			return diffLogOverlay(cmd, name, stat)
		}
		if cliutil.JSONEnabled(cmd) {
			return diffLogJSON(cmd, name, stat)
		}
		return diffLog(cmd, name, stat)
	}

	// Parse ref vs paths: split on "--" if present, otherwise
	// try to detect ref from the first positional arg.
	ref, paths := parseDiffArgs(rest, cmd)

	// Ref-based diff not supported for overlay
	if ref != "" && overlay {
		return yoerrors.NewPlatformError("ref-based diff is not supported for :overlay sandboxes (commits are not individually addressable from the host)")
	}

	// If ref is set, show that specific commit/range
	if ref != "" {
		return diffRef(cmd, name, ref, stat)
	}

	// Q-U: diff is workdir-only — overlay routes through container
	// exec; everything else goes through the single workdir helper.
	if overlay {
		return diffOverlay(cmd, name, stat, nameOnly)
	}
	return diffSingle(cmd, name, paths, stat, nameOnly)
}

// diffSingle runs a diff for the sandbox's workdir.
func diffSingle(cmd *cobra.Command, name string, paths []string, stat, nameOnly bool) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, err := c.Sandbox(name)
		if err != nil {
			return err
		}
		out, err := sb.Workdir().Diff(ctx, yoloai.DiffOptions{Paths: paths, Stat: stat, NameOnly: nameOnly})
		if err != nil {
			return err
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

// hasOverlayDirs returns true if any directory in the sandbox uses overlay mode.
func hasOverlayDirs(meta *store.Meta) bool {
	if meta.Workdir.Mode == "overlay" {
		return true
	}
	for _, d := range meta.Directories {
		if d.Mode == "overlay" {
			return true
		}
	}
	return false
}

// requireOverlayRunning verifies the sandbox container is running (required for overlay ops).
func requireOverlayRunning(ctx context.Context, c *yoloai.Client, name string) error {
	sb, err := c.Sandbox(name)
	if err != nil {
		return fmt.Errorf(":overlay sandbox %s must be running for this operation — use 'yoloai start %s'", name, name)
	}
	info, err := sb.Inspect(ctx)
	if err != nil {
		return fmt.Errorf(":overlay sandbox %s must be running for this operation — use 'yoloai start %s'", name, name)
	}
	if info.Status != yoloai.StatusActive && info.Status != yoloai.StatusIdle {
		return fmt.Errorf(":overlay sandbox %s must be running for this operation — use 'yoloai start %s'", name, name)
	}
	return nil
}

// diffOverlay runs the diff for an :overlay-mode workdir. Routes
// through container exec since git lives inside the container.
func diffOverlay(cmd *cobra.Command, name string, stat, nameOnly bool) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		if err := requireOverlayRunning(ctx, c, name); err != nil {
			return err
		}
		sb, err := c.Sandbox(name)
		if err != nil {
			return err
		}
		out, err := sb.Workdir().Diff(ctx, yoloai.DiffOptions{Stat: stat, NameOnly: nameOnly})
		if err != nil {
			return err
		}
		return writeDiffOutput(cmd, out)
	})
}

// diffLogOverlay lists commits for overlay sandboxes by executing git log inside the container.
func diffLogOverlay(cmd *cobra.Command, name string, stat bool) error {
	if stat {
		return yoerrors.NewPlatformError("--log --stat is not supported for :overlay sandboxes")
	}

	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		if err := requireOverlayRunning(ctx, c, name); err != nil {
			return err
		}

		sb, err := c.Sandbox(name)
		if err != nil {
			return err
		}
		commits, err := sb.Workdir().Commits(ctx, yoloai.CommitsOptions{})
		if err != nil {
			return err
		}

		if cliutil.JSONEnabled(cmd) {
			if commits == nil {
				commits = []yoloai.CommitInfo{}
			}
			result := struct {
				Commits               any  `json:"commits"`
				HasUncommittedChanges bool `json:"has_uncommitted_changes"`
			}{
				Commits:               commits,
				HasUncommittedChanges: false, // can't cheaply detect uncommitted changes in overlay
			}
			return cliutil.WriteJSON(cmd.OutOrStdout(), result)
		}

		out := cmd.OutOrStdout()
		if len(commits) == 0 {
			_, err = fmt.Fprintln(out, "No commits beyond baseline")
			return err
		}
		for i, c := range commits {
			fmt.Fprintf(out, "%3d  %.12s  %s\n", i+1, c.SHA, c.Subject) //nolint:errcheck
		}
		return nil
	})
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
		beforeDash := min(
			// how many of rest[] are before "--"
			max(

				dashAt-1, 0), len(rest))

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

	// Fetch tags for inline display (best-effort).
	var tags []yoloai.TagInfo
	backend := cliutil.ResolveBackendForSandbox(name)
	//nolint:errcheck // best-effort: tag annotations are decorative
	_ = cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, sbErr := c.Sandbox(name)
		if sbErr != nil {
			return sbErr
		}
		tags, _ = sb.Workdir().Tags(ctx, yoloai.TagsOptions{})
		return nil
	})
	tagsByCommit := buildTagsByCommit(tags)

	if stat {
		if err := diffLogWithStat(cmd, name, out, tagsByCommit); err != nil {
			return err
		}
	} else {
		if err := diffLogBasic(cmd, name, out, tagsByCommit); err != nil {
			return err
		}
	}

	diffLogUncommitted(cmd, name, out)
	return nil
}

// diffLogWithStat prints commits with file-change statistics.
func diffLogWithStat(cmd *cobra.Command, name string, out io.Writer, tagsByCommit map[string][]string) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	var commits []yoloai.CommitInfo
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, sbErr := c.Sandbox(name)
		if sbErr != nil {
			return sbErr
		}
		var listErr error
		commits, listErr = sb.Workdir().Commits(ctx, yoloai.CommitsOptions{Stat: true})
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
func diffLogBasic(cmd *cobra.Command, name string, out io.Writer, tagsByCommit map[string][]string) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	var commits []yoloai.CommitInfo
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, sbErr := c.Sandbox(name)
		if sbErr != nil {
			return sbErr
		}
		var listErr error
		commits, listErr = sb.Workdir().Commits(ctx, yoloai.CommitsOptions{})
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
func diffLogUncommitted(cmd *cobra.Command, name string, out io.Writer) {
	backend := cliutil.ResolveBackendForSandbox(name)
	var hasUncommitted bool
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, sbErr := c.Sandbox(name)
		if sbErr != nil {
			return sbErr
		}
		var uncommittedErr error
		hasUncommitted, uncommittedErr = sb.Workdir().HasUncommittedChanges(ctx)
		return uncommittedErr
	})
	if err == nil && hasUncommitted {
		fmt.Fprintln(out, "  *  (uncommitted changes)") //nolint:errcheck
	}
}

// diffRef shows the diff for a specific commit or range. Disk-only; no
// runtime needed, but routed through WithClient for symmetry with the
// other diff handlers.
func diffRef(cmd *cobra.Command, name, ref string, stat bool) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, err := c.Sandbox(name)
		if err != nil {
			return err
		}
		out, err := sb.Workdir().Diff(ctx, yoloai.DiffOptions{Ref: ref, Stat: stat})
		if err != nil {
			return err
		}
		return writeDiffOutput(cmd, out)
	})
}

// agentRunningWarning prints a warning to stderr if the agent is still running.
// Silently skips if Docker is unavailable or inspection fails.
func agentRunningWarning(cmd *cobra.Command, name string) {
	backend := cliutil.ResolveBackendForSandbox(name)
	//nolint:errcheck // intentional: best-effort warning, failure here should not affect the diff command
	_ = cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, err := c.Sandbox(name)
		if err != nil {
			return nil //nolint:nilerr // best-effort warning; inspection failure should not affect the diff command
		}
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
func diffLogJSON(cmd *cobra.Command, name string, stat bool) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, err := c.Sandbox(name)
		if err != nil {
			return err
		}
		cs, err := sb.Workdir().Commits(ctx, yoloai.CommitsOptions{Stat: stat})
		if err != nil {
			return err
		}
		if cs == nil {
			cs = []yoloai.CommitInfo{}
		}

		hasUncommitted, _ := sb.Workdir().HasUncommittedChanges(ctx)
		tags, _ := sb.Workdir().Tags(ctx, yoloai.TagsOptions{})
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
