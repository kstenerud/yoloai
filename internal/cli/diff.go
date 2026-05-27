// ABOUTME: Cobra "diff" command: shows agent changes as a unified diff, commit
// ABOUTME: log, or per-ref diff; handles :copy, :overlay, and multi-directory sandboxes.
package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
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
		return sandbox.NewPlatformError("ref-based diff is not supported for :overlay sandboxes (commits are not individually addressable from the host)")
	}

	// If ref is set, show that specific commit/range
	if ref != "" {
		return diffRef(cmd, name, ref, stat)
	}

	// Default: monolithic diff
	if overlay {
		return diffOverlay(cmd, name, stat, nameOnly)
	}

	if len(meta.Directories) > 0 && len(paths) == 0 {
		if cliutil.JSONEnabled(cmd) {
			return diffMultiDirJSON(cmd, name, stat)
		}
		return diffMultiDir(cmd, name, stat)
	}

	return diffSingle(cmd, name, paths, stat, nameOnly)
}

// diffSingle runs a diff for a single (non-overlay, non-multi) directory.
func diffSingle(cmd *cobra.Command, name string, paths []string, stat, nameOnly bool) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		result, err := c.DiffSingle(ctx, name, paths, stat, nameOnly)
		if err != nil {
			return err
		}
		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), result)
		}
		if result.Empty {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes")
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Output)
		return err
	})
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
	info, err := c.Inspect(ctx, name)
	if err != nil {
		return fmt.Errorf(":overlay sandbox %s must be running for this operation — use 'yoloai start %s'", name, name)
	}
	if info.Status != sandbox.StatusActive && info.Status != sandbox.StatusIdle {
		return fmt.Errorf(":overlay sandbox %s must be running for this operation — use 'yoloai start %s'", name, name)
	}
	return nil
}

// diffOverlay handles the default diff for sandboxes with overlay dirs.
// Merges overlay results (from container exec) with non-overlay results.
func diffOverlay(cmd *cobra.Command, name string, stat, nameOnly bool) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		if err := requireOverlayRunning(ctx, c, name); err != nil {
			return err
		}

		// Get overlay diffs via container exec
		overlayResults, err := c.DiffOverlay(ctx, name, stat, nameOnly)
		if err != nil {
			return err
		}

		// Get non-overlay diffs (copy/rw) via host
		hostResults, err := c.DiffMultiDir(ctx, name, stat)
		if err != nil {
			return err
		}

		merged := mergeOverlayDiffResults(hostResults, overlayResults)

		if cliutil.JSONEnabled(cmd) {
			if merged == nil {
				merged = []*patch.DiffResult{}
			}
			return cliutil.WriteJSON(cmd.OutOrStdout(), merged)
		}

		return printMergedDiffResults(cmd, merged)
	})
}

// mergeOverlayDiffResults merges overlay results into host results.
func mergeOverlayDiffResults(hostResults, overlayResults []*patch.DiffResult) []*patch.DiffResult {
	var merged []*patch.DiffResult
	for _, r := range hostResults {
		if r.Mode == "overlay" {
			// Find matching overlay result
			for _, or := range overlayResults {
				if or.WorkDir == r.WorkDir {
					merged = append(merged, or)
					break
				}
			}
		} else {
			merged = append(merged, r)
		}
	}
	// Add any overlay results not matched (shouldn't happen, but be safe)
	matchedOverlay := make(map[string]bool)
	for _, r := range hostResults {
		if r.Mode == "overlay" {
			matchedOverlay[r.WorkDir] = true
		}
	}
	for _, or := range overlayResults {
		if !matchedOverlay[or.WorkDir] {
			merged = append(merged, or)
		}
	}
	return merged
}

// printMergedDiffResults prints multiple diff results to stdout.
func printMergedDiffResults(cmd *cobra.Command, merged []*patch.DiffResult) error {
	allEmpty := true
	for _, r := range merged {
		if !r.Empty {
			allEmpty = false
			break
		}
	}
	if allEmpty {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No changes")
		return err
	}

	var sb strings.Builder
	for _, r := range merged {
		if r.Empty {
			continue
		}
		fmt.Fprintf(&sb, "=== %s (%s) ===\n", r.WorkDir, r.Mode)
		sb.WriteString(r.Output)
		sb.WriteString("\n\n")
	}

	output := strings.TrimRight(sb.String(), "\n") + "\n"
	_, err := fmt.Fprint(cmd.OutOrStdout(), output)
	return err
}

// diffLogOverlay lists commits for overlay sandboxes by executing git log inside the container.
func diffLogOverlay(cmd *cobra.Command, name string, stat bool) error {
	if stat {
		return sandbox.NewPlatformError("--log --stat is not supported for :overlay sandboxes")
	}

	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		if err := requireOverlayRunning(ctx, c, name); err != nil {
			return err
		}

		commits, err := c.ListCommitsOverlay(ctx, name)
		if err != nil {
			return err
		}

		if cliutil.JSONEnabled(cmd) {
			if commits == nil {
				commits = []patch.CommitInfo{}
			}
			result := struct {
				Commits               any  `json:"commits"`
				HasUncommittedChanges bool `json:"has_uncommitted_changes"`
			}{
				Commits:               commits,
				HasUncommittedChanges: false, // can't cheaply detect WIP in overlay
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
	tags, _ := sandbox.ListTagsBeyondBaseline(cliutil.Layout(), name)
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

	diffLogWIP(cmd, name, out)
	return nil
}

// diffLogWithStat prints commits with file-change statistics.
func diffLogWithStat(cmd *cobra.Command, name string, out io.Writer, tagsByCommit map[string][]string) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	var commits []patch.CommitInfoWithStat
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var listErr error
		commits, listErr = c.ListCommitsWithStats(ctx, name)
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
	var commits []patch.CommitInfo
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var listErr error
		commits, listErr = c.ListCommits(ctx, name)
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

// diffLogWIP appends an uncommitted-changes indicator if WIP is present (best-effort).
func diffLogWIP(cmd *cobra.Command, name string, out io.Writer) {
	backend := cliutil.ResolveBackendForSandbox(name)
	var hasWIP bool
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var wipErr error
		hasWIP, wipErr = c.HasUncommittedChanges(ctx, name)
		return wipErr
	})
	if err == nil && hasWIP {
		fmt.Fprintln(out, "  *  (uncommitted changes)") //nolint:errcheck
	}
}

// diffRef shows the diff for a specific commit or range. Disk-only; no
// runtime needed, but routed through WithClient for symmetry with the
// other diff handlers.
func diffRef(cmd *cobra.Command, name, ref string, stat bool) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		result, err := c.DiffRef(ctx, name, ref, stat)
		if err != nil {
			return err
		}

		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), result)
		}

		if result.Empty {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes")
			return err
		}

		_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Output)
		return err
	})
}

// agentRunningWarning prints a warning to stderr if the agent is still running.
// Silently skips if Docker is unavailable or inspection fails.
func agentRunningWarning(cmd *cobra.Command, name string) {
	backend := cliutil.ResolveBackendForSandbox(name)
	//nolint:errcheck // intentional: best-effort warning, failure here should not affect the diff command
	_ = cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		info, err := c.Inspect(ctx, name)
		if err != nil {
			return nil //nolint:nilerr // best-effort warning; inspection failure should not affect the diff command
		}

		if info.Status == sandbox.StatusActive || info.Status == sandbox.StatusIdle {
			fmt.Fprintln(cmd.ErrOrStderr(), "Note: agent is still running; diff may be incomplete") //nolint:errcheck // best-effort warning
		}
		return nil
	})
}

// diffMultiDir shows diffs for all diffable directories with per-dir headers.
// Disk-only; no runtime needed, but routed through WithClient for symmetry.
func diffMultiDir(cmd *cobra.Command, name string, stat bool) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		return diffMultiDirInner(cmd, ctx, c, name, stat)
	})
}

// diffMultiDirInner is the body of diffMultiDir factored out so the
// WithClient open-and-close lives at the entry point.
func diffMultiDirInner(cmd *cobra.Command, ctx context.Context, c *yoloai.Client, name string, stat bool) error {
	results, err := c.DiffMultiDir(ctx, name, stat)
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
	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var commits any
		if stat {
			cs, err := c.ListCommitsWithStats(ctx, name)
			if err != nil {
				return err
			}
			if cs == nil {
				cs = []patch.CommitInfoWithStat{}
			}
			commits = cs
		} else {
			cs, err := c.ListCommits(ctx, name)
			if err != nil {
				return err
			}
			if cs == nil {
				cs = []patch.CommitInfo{}
			}
			commits = cs
		}

		hasWIP, _ := c.HasUncommittedChanges(ctx, name)
		tags, _ := sandbox.ListTagsBeyondBaseline(cliutil.Layout(), name)
		if tags == nil {
			tags = []sandbox.TagInfo{}
		}

		result := struct {
			Commits               any               `json:"commits"`
			HasUncommittedChanges bool              `json:"has_uncommitted_changes"`
			Tags                  []sandbox.TagInfo `json:"tags"`
		}{
			Commits:               commits,
			HasUncommittedChanges: hasWIP,
			Tags:                  tags,
		}

		return cliutil.WriteJSON(cmd.OutOrStdout(), result)
	})
}

// diffMultiDirJSON outputs multi-directory diffs as JSON.
// Disk-only, but routed through WithClient for symmetry.
func diffMultiDirJSON(cmd *cobra.Command, name string, stat bool) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		results, err := c.DiffMultiDir(ctx, name, stat)
		if err != nil {
			return err
		}
		if results == nil {
			results = []*patch.DiffResult{}
		}
		return cliutil.WriteJSON(cmd.OutOrStdout(), results)
	})
}
