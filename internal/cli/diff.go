package cli

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
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
			defer openCLIJSONLSink(name, cmd)()

			stat, _ := cmd.Flags().GetBool("stat")
			nameOnly, _ := cmd.Flags().GetBool("name-only")
			logFlag, _ := cmd.Flags().GetBool("log")

			// Load meta early to detect overlay dirs
			meta, metaErr := sandbox.LoadMeta(sandbox.Dir(name))
			if metaErr != nil {
				return sandboxErrorHint(name, metaErr)
			}
			overlay := hasOverlayDirs(meta)
			slog.Debug("generating diff", "event", "sandbox.diff", "sandbox", name, "workdir_mode", meta.Workdir.Mode) //nolint:gosec // G706: name is validated by ValidateName

			// Skip agent warning in JSON mode
			if !jsonEnabled(cmd) {
				agentRunningWarning(cmd, name)
			}

			// --log: list commits
			if logFlag {
				if overlay {
					return diffLogOverlay(cmd, name, stat)
				}
				if jsonEnabled(cmd) {
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
				if jsonEnabled(cmd) {
					return diffMultiDirJSON(cmd, name, stat)
				}
				return diffMultiDir(cmd, name, stat)
			}

			backend := resolveBackendForSandbox(name)
			var finalErr error
			_ = withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error { //nolint:errcheck // error handled via finalErr
				opts := sandbox.DiffOptions{
					Name:     name,
					Paths:    paths,
					NameOnly: nameOnly,
					Runtime:  rt,
				}

				if nameOnly {
					result, err := sandbox.GenerateDiff(ctx, opts)
					if err != nil {
						finalErr = err
						return err
					}
					if jsonEnabled(cmd) {
						finalErr = writeJSON(cmd.OutOrStdout(), result)
						return finalErr
					}
					if result.Empty {
						_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes")
						finalErr = err
						return err
					}
					_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Output)
					finalErr = err
					return err
				}

				opts.Stat = stat
				result, err := sandbox.GenerateDiff(ctx, opts)
				if err != nil {
					finalErr = err
					return err
				}
				if jsonEnabled(cmd) {
					finalErr = writeJSON(cmd.OutOrStdout(), result)
					return finalErr
				}
				if result.Empty {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes")
					finalErr = err
					return err
				}

				_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Output)
				finalErr = err
				return err
			})
			return finalErr
		},
	}

	cmd.Flags().Bool("stat", false, "Show summary (files changed, insertions, deletions)")
	cmd.Flags().Bool("name-only", false, "List changed files without content")
	cmd.Flags().Bool("log", false, "List agent commits beyond baseline")

	cmd.MarkFlagsMutuallyExclusive("stat", "name-only")

	return cmd
}

// hasOverlayDirs returns true if any directory in the sandbox uses overlay mode.
func hasOverlayDirs(meta *sandbox.Meta) bool {
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
func requireOverlayRunning(ctx context.Context, rt runtime.Runtime, name string) error {
	info, err := rt.Inspect(ctx, sandbox.InstanceName(name))
	if err != nil {
		return fmt.Errorf("overlay sandbox %s must be running for this operation — use 'yoloai start %s'", name, name)
	}
	if !info.Running {
		return fmt.Errorf("overlay sandbox %s must be running for this operation — use 'yoloai start %s'", name, name)
	}
	return nil
}

// diffOverlay handles the default diff for sandboxes with overlay dirs.
// Merges overlay results (from container exec) with non-overlay results.
func diffOverlay(cmd *cobra.Command, name string, stat, nameOnly bool) error {
	backend := resolveBackendForSandbox(name)
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		if err := requireOverlayRunning(ctx, rt, name); err != nil {
			return err
		}

		// Get overlay diffs via container exec
		overlayResults, err := sandbox.GenerateOverlayDiff(ctx, rt, sandbox.DiffOptions{Name: name, Stat: stat, NameOnly: nameOnly})
		if err != nil {
			return err
		}

		// Get non-overlay diffs (copy/rw) via host
		hostResults, err := sandbox.GenerateMultiDiff(sandbox.DiffOptions{Name: name, Stat: stat})
		if err != nil {
			return err
		}

		// Merge: replace overlay placeholder entries from hostResults with actual overlay results
		var merged []*sandbox.DiffResult
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

		if jsonEnabled(cmd) {
			if merged == nil {
				merged = []*sandbox.DiffResult{}
			}
			return writeJSON(cmd.OutOrStdout(), merged)
		}

		allEmpty := true
		for _, r := range merged {
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
		for _, r := range merged {
			if r.Empty {
				continue
			}
			fmt.Fprintf(&sb, "=== %s (%s) ===\n", r.WorkDir, r.Mode)
			sb.WriteString(r.Output)
			sb.WriteString("\n\n")
		}

		output := strings.TrimRight(sb.String(), "\n") + "\n"
		_, err = fmt.Fprint(cmd.OutOrStdout(), output)
		return err
	})
}

// diffLogOverlay lists commits for overlay sandboxes by executing git log inside the container.
func diffLogOverlay(cmd *cobra.Command, name string, stat bool) error {
	if stat {
		return sandbox.NewPlatformError("--log --stat is not supported for :overlay sandboxes")
	}

	backend := resolveBackendForSandbox(name)
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		if err := requireOverlayRunning(ctx, rt, name); err != nil {
			return err
		}

		commits, err := sandbox.ListCommitsBeyondBaselineOverlay(ctx, rt, name)
		if err != nil {
			return err
		}

		if jsonEnabled(cmd) {
			if commits == nil {
				commits = []sandbox.CommitInfo{}
			}
			result := struct {
				Commits               any  `json:"commits"`
				HasUncommittedChanges bool `json:"has_uncommitted_changes"`
			}{
				Commits:               commits,
				HasUncommittedChanges: false, // can't cheaply detect WIP in overlay
			}
			return writeJSON(cmd.OutOrStdout(), result)
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

	// Fetch tags for inline display (best-effort).
	tags, _ := sandbox.ListTagsBeyondBaseline(name)
	tagsByCommit := buildTagsByCommit(tags)

	if stat {
		backend := resolveBackendForSandbox(name)
		var commits []sandbox.CommitInfoWithStat
		err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
			var listErr error
			commits, listErr = sandbox.ListCommitsWithStats(ctx, rt, name)
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
			line := fmt.Sprintf("%3d  %.12s  %s", i+1, c.SHA, c.Subject)
			if names := tagsByCommit[strings.ToLower(c.SHA)]; len(names) > 0 {
				line += "  [tag: " + strings.Join(names, ", ") + "]"
			}
			fmt.Fprintln(out, line) //nolint:errcheck
			if c.Stat != "" {
				for _, statLine := range strings.Split(c.Stat, "\n") {
					fmt.Fprintf(out, "     %s\n", statLine) //nolint:errcheck
				}
			}
		}
	} else {
		backend := resolveBackendForSandbox(name)
		var commits []sandbox.CommitInfo
		err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
			var listErr error
			commits, listErr = sandbox.ListCommitsBeyondBaseline(ctx, rt, name)
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
			line := fmt.Sprintf("%3d  %.12s  %s", i+1, c.SHA, c.Subject)
			if names := tagsByCommit[strings.ToLower(c.SHA)]; len(names) > 0 {
				line += "  [tag: " + strings.Join(names, ", ") + "]"
			}
			fmt.Fprintln(out, line) //nolint:errcheck
		}
	}

	// Check for uncommitted changes
	backend := resolveBackendForSandbox(name)
	var hasWIP bool
	err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		var wipErr error
		hasWIP, wipErr = sandbox.HasUncommittedChanges(ctx, rt, name)
		return wipErr
	})
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
	//nolint:errcheck // intentional: best-effort warning, failure here should not affect the diff command
	_ = withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		info, err := sandbox.InspectSandbox(ctx, rt, name)
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
func diffMultiDir(cmd *cobra.Command, name string, stat bool) error {
	results, err := sandbox.GenerateMultiDiff(sandbox.DiffOptions{Name: name, Stat: stat})
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
	backend := resolveBackendForSandbox(name)
	if stat {
		var c []sandbox.CommitInfoWithStat
		err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
			var listErr error
			c, listErr = sandbox.ListCommitsWithStats(ctx, rt, name)
			return listErr
		})
		if err != nil {
			return err
		}
		if c == nil {
			c = []sandbox.CommitInfoWithStat{}
		}
		commits = c
	} else {
		backend := resolveBackendForSandbox(name)
		var c []sandbox.CommitInfo
		err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
			var listErr error
			c, listErr = sandbox.ListCommitsBeyondBaseline(ctx, rt, name)
			return listErr
		})
		if err != nil {
			return err
		}
		if c == nil {
			c = []sandbox.CommitInfo{}
		}
		commits = c
	}

	backendWIP := resolveBackendForSandbox(name)
	var hasWIP bool
	_ = withRuntime(cmd.Context(), backendWIP, func(ctx context.Context, rt runtime.Runtime) error {
		hasWIP, _ = sandbox.HasUncommittedChanges(ctx, rt, name)
		return nil
	})
	tags, _ := sandbox.ListTagsBeyondBaseline(name)
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

	return writeJSON(cmd.OutOrStdout(), result)
}

// diffMultiDirJSON outputs multi-directory diffs as JSON.
func diffMultiDirJSON(cmd *cobra.Command, name string, stat bool) error {
	results, err := sandbox.GenerateMultiDiff(sandbox.DiffOptions{Name: name, Stat: stat})
	if err != nil {
		return err
	}
	if results == nil {
		results = []*sandbox.DiffResult{}
	}
	return writeJSON(cmd.OutOrStdout(), results)
}
