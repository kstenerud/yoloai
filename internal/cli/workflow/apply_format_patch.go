// ABOUTME: Default apply workflow — git format-patch + git am, preserving
// ABOUTME: individual commits. Uncommitted changes are applied as unstaged. Falls back
// ABOUTME: to applyNoCommit for non-git targets or uncommitted-only sandboxes.
package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/spf13/cobra"
)

// runApplyFormatPatch handles the default format-patch apply flow.
func runApplyFormatPatch(cmd *cobra.Command, name string, paths []string, meta *store.Meta, patchesDir string, yes, dryRun, includeUncommitted, withTags bool) error {
	// Query work copy for commits and uncommitted changes. Uncommitted changes are
	// always probed (even when includeUncommitted is false) so we can report them
	// to the user as a hint.
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

	var hasUncommitted bool
	err = cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var uncommittedErr error
		hasUncommitted, uncommittedErr = c.HasUncommittedChanges(ctx, name)
		return uncommittedErr
	})
	if err != nil {
		return err
	}

	slog.Debug("commits to apply", "event", "sandbox.apply.commits", "sandbox", name, "count", len(commits)) //nolint:gosec // G706: name is validated by ValidateName
	if hasUncommitted {
		slog.Debug("uncommitted changes detected", "event", "sandbox.apply.uncommitted", "sandbox", name, "include_uncommitted", includeUncommitted) //nolint:gosec // G706: name is validated by ValidateName
	}
	if done, doneErr := maybeReportNoChanges(cmd, name, meta, commits, hasUncommitted, includeUncommitted, withTags); done {
		return doneErr
	}

	// --patches: export patch files to a directory
	if patchesDir != "" {
		return exportPatches(cmd, name, paths, commits, hasUncommitted, includeUncommitted, patchesDir)
	}

	targetDir := meta.Workdir.HostPath
	isGit := workspace.IsGitRepo(targetDir)

	// Non-git fallback: can't use git am on non-git targets
	if !isGit && len(commits) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "Note: target is not a git repository — falling back to a single unstaged patch (--no-commit)") //nolint:errcheck
		return applyNoCommit(cmd, name, paths, meta, yes, dryRun, includeUncommitted)
	}

	// No commits, only uncommitted changes (user opted in) — use the net-diff (no-commit) flow.
	if len(commits) == 0 && hasUncommitted && includeUncommitted {
		if withTags {
			return sandbox.NewUsageError("--tags requires commits — cannot transfer tags with uncommitted-only changes")
		}
		return applyNoCommit(cmd, name, paths, meta, yes, dryRun, includeUncommitted)
	}

	return runApplyCommits(cmd, name, paths, meta, commits, hasUncommitted, yes, dryRun, includeUncommitted, withTags)
}

// printUncommittedHint tells the user there are uncommitted edits they could
// pull in via --include-uncommitted. Human-mode only; JSON output skips it.
func printUncommittedHint(cmd *cobra.Command, reason string) {
	fmt.Fprintf(cmd.OutOrStdout(), "Note: sandbox has uncommitted changes (%s); re-run with --include-uncommitted to apply them.\n", reason) //nolint:errcheck
}

// reportUncommittedSkipHint prints the uncommitted-skipped hint after commits land. Human-mode only.
func reportUncommittedSkipHint(cmd *cobra.Command, hasUncommitted, includeUncommitted bool) {
	if hasUncommitted && !includeUncommitted && !cliutil.JSONEnabled(cmd) {
		printUncommittedHint(cmd, "not applied — commits only")
	}
}

// reportUnappliedTagsHint suggests --tags when sandbox has tags the user
// didn't ask to transfer. Human-mode only.
func reportUnappliedTagsHint(cmd *cobra.Command, name string, withTags bool) {
	if cliutil.JSONEnabled(cmd) || withTags {
		return
	}
	unappliedTags, _ := sandbox.ListUnappliedTags(cliutil.Layout(), name)
	if len(unappliedTags) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "\nHint: %d tag(s) available in sandbox but not on host. Run with --tags to transfer them.\n", len(unappliedTags)) //nolint:errcheck
	}
}

// maybeReportNoChanges handles the "nothing to apply" case at the entry of
// the format-patch flow. Returns (true, err) when the caller should return;
// (false, nil) to continue with the normal apply.
func maybeReportNoChanges(cmd *cobra.Command, name string, meta *store.Meta, commits []patch.CommitInfo, hasUncommitted, includeUncommitted, withTags bool) (bool, error) {
	if len(commits) > 0 {
		return false, nil
	}
	if hasUncommitted && includeUncommitted {
		return false, nil // uncommitted-only apply will proceed via the no-commit fallback
	}
	if hasUncommitted && !cliutil.JSONEnabled(cmd) {
		printUncommittedHint(cmd, "no committed changes to apply")
	}
	return true, runApplyNoChanges(cmd, name, meta, withTags)
}

// runApplyNoChanges handles the case where there are no commits or uncommitted changes to apply.
func runApplyNoChanges(cmd *cobra.Command, name string, meta *store.Meta, withTags bool) error {
	layout := cliutil.Layout()
	// Check for unapplied tags even when there are no changes
	unappliedTags, _ := sandbox.ListUnappliedTags(layout, name)

	// If --tags is used, transfer tags even without commits
	if withTags && len(unappliedTags) > 0 {
		targetDir := meta.Workdir.HostPath
		workDir := store.WorkDir(layout.SandboxDir(name), meta.Workdir.HostPath)
		if !cliutil.JSONEnabled(cmd) {
			fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")                                                  //nolint:errcheck
			fmt.Fprintf(cmd.OutOrStdout(), "\nTransferring %d tag(s) by matching commits...\n", len(unappliedTags)) //nolint:errcheck
		}
		// Build SHA map by matching commits (author, timestamp, subject)
		sandboxSHAs := make([]string, len(unappliedTags))
		for i, tag := range unappliedTags {
			sandboxSHAs[i] = tag.SHA
		}
		shaMap, matchErr := workspace.BuildSHAMapByMatching(workDir, targetDir, sandboxSHAs)
		if matchErr != nil {
			return fmt.Errorf("build SHA map: %w", matchErr)
		}
		// Transfer tags using the SHA map
		tagsApplied, tagsSkipped := applyTags(cmd, unappliedTags, shaMap, workDir, targetDir, true)
		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
				Target:      meta.Workdir.HostPath,
				TagsApplied: tagsApplied,
				TagsSkipped: tagsSkipped,
				Method:      "format-patch",
			})
		}
		return nil
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
			Target: meta.Workdir.HostPath,
			Method: "format-patch",
		})
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
	// Inform user if tags are available but not on host
	if len(unappliedTags) > 0 && !withTags {
		fmt.Fprintf(cmd.OutOrStdout(), "\nHint: %d tag(s) available in sandbox but not on host. Run with --tags to transfer them.\n", len(unappliedTags)) //nolint:errcheck
	}
	return err
}

// runApplyCommits replays commits via the library's series apply
// (Workdir().Apply ApplyModeCommits), then transfers tags using the SHA mapping
// it returns. The library owns generate / git am / baseline-advance / uncommitted;
// this function owns the CLI summary, confirmation, tag transfer, and output.
func runApplyCommits(cmd *cobra.Command, name string, paths []string, meta *store.Meta, commits []patch.CommitInfo, hasUncommitted, yes, dryRun, includeUncommitted, withTags bool) error {
	layout := cliutil.Layout()
	targetDir := meta.Workdir.HostPath
	sandboxWorkDir := store.WorkDir(layout.SandboxDir(name), meta.Workdir.HostPath)
	backend := cliutil.ResolveBackendForSandbox(name)

	// Fetch tags beyond baseline (best-effort; errors don't fail the apply).
	tags, _ := sandbox.ListTagsBeyondBaseline(layout, name)
	printApplyCommitsSummary(cmd, commits, tags, buildTagsByCommit(tags), hasUncommitted, includeUncommitted, withTags)

	if dryRun {
		if !cliutil.JSONEnabled(cmd) {
			fmt.Fprintln(cmd.OutOrStdout(), "(dry run)") //nolint:errcheck
		}
		return nil
	}

	if !yes {
		prompt := fmt.Sprintf("Apply to %s? [y/N] ", targetDir)
		confirmed, confirmErr := sandbox.Confirm(cmd.Context(), prompt, os.Stdin, cmd.ErrOrStderr())
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			return nil
		}
	}

	var result *yoloai.ApplyResult
	applyErr := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var e error
		result, e = c.Sandbox(name).Workdir().Apply(ctx, yoloai.ApplyOptions{
			Mode: yoloai.ApplyModeCommits, IncludeUncommitted: includeUncommitted, Paths: paths,
		})
		return e
	})
	// result != nil means the commits landed; a non-nil applyErr alongside it is
	// a non-fatal follow-on issue (git am stash, or uncommitted changes that failed
	// to apply), surfaced after we report what did land. result == nil is a hard failure.
	if result == nil {
		return applyErr
	}

	commitsApplied := len(result.Commits)
	if !cliutil.JSONEnabled(cmd) {
		fmt.Fprintf(cmd.OutOrStdout(), "%d commit(s) applied to %s\n", commitsApplied, targetDir) //nolint:errcheck
	}

	shaMap := make(map[string]string, len(result.Commits))
	for _, c := range result.Commits {
		shaMap[strings.ToLower(c.SourceSHA)] = c.HostSHA
	}
	reportUncommittedSkipHint(cmd, hasUncommitted, includeUncommitted)
	tagsApplied, tagsSkipped := applyTags(cmd, tags, shaMap, sandboxWorkDir, targetDir, withTags)
	reportUnappliedTagsHint(cmd, name, withTags)

	slog.Info("apply complete", "event", "sandbox.apply.complete", "sandbox", name, "commits_applied", commitsApplied, "uncommitted_applied", result.UncommittedApplied, "tags_applied", tagsApplied) //nolint:gosec // G706: name is validated by ValidateName
	if cliutil.JSONEnabled(cmd) {
		if writeErr := cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
			Target:             targetDir,
			CommitsApplied:     commitsApplied,
			UncommittedApplied: result.UncommittedApplied,
			TagsApplied:        tagsApplied,
			TagsSkipped:        tagsSkipped,
			Method:             "format-patch",
		}); writeErr != nil {
			return writeErr
		}
	}

	return applyErr
}

// printApplyCommitsSummary prints the list of commits about to be applied (human-readable only).
func printApplyCommitsSummary(cmd *cobra.Command, commits []patch.CommitInfo, tags []sandbox.TagInfo, tagsByCommit map[string][]string, hasUncommitted, includeUncommitted, withTags bool) {
	if cliutil.JSONEnabled(cmd) {
		return
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Commits to apply (%d):\n", len(commits)) //nolint:errcheck
	for _, c := range commits {
		line := fmt.Sprintf("  %.12s %s", c.SHA, c.Subject)
		if names := tagsByCommit[strings.ToLower(c.SHA)]; len(names) > 0 {
			line += "  [tag: " + strings.Join(names, ", ") + "]"
		}
		fmt.Fprintln(out, line) //nolint:errcheck
	}
	switch {
	case hasUncommitted && includeUncommitted:
		fmt.Fprintln(out, "\n+ uncommitted changes (will be applied as unstaged files)") //nolint:errcheck
	case hasUncommitted:
		fmt.Fprintln(out, "\n  (sandbox also has uncommitted changes — not applied; re-run with --include-uncommitted to include)") //nolint:errcheck
	}
	if len(tags) > 0 && !withTags {
		fmt.Fprintf(out, "\nWARNING: %d tag(s) will NOT be applied (cancel this apply and redo with --tags to include them)\n", len(tags)) //nolint:errcheck
	}
	fmt.Fprintln(out) //nolint:errcheck
}
