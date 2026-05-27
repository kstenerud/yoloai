// ABOUTME: Default apply workflow — git format-patch + git am, preserving
// ABOUTME: individual commits. WIP changes are applied as unstaged. Falls back
// ABOUTME: to applySquash for non-git targets or WIP-only sandboxes.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/spf13/cobra"
)

// runApplyFormatPatch handles the default format-patch apply flow.
func runApplyFormatPatch(cmd *cobra.Command, name string, paths []string, meta *store.Meta, patchesDir string, yes, dryRun, includeWIP, withTags bool) error {
	// Query work copy for commits and WIP. WIP is always probed (even when
	// includeWIP is false) so we can report it to the user as a hint.
	backend := resolveBackendForSandbox(name)
	var commits []patch.CommitInfo
	err := withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var listErr error
		commits, listErr = c.ListCommits(ctx, name)
		return listErr
	})
	if err != nil {
		return err
	}

	var hasWIP bool
	err = withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var wipErr error
		hasWIP, wipErr = c.HasUncommittedChanges(ctx, name)
		return wipErr
	})
	if err != nil {
		return err
	}

	slog.Debug("commits to apply", "event", "sandbox.apply.commits", "sandbox", name, "count", len(commits)) //nolint:gosec // G706: name is validated by ValidateName
	if hasWIP {
		slog.Debug("WIP detected", "event", "sandbox.apply.wip", "sandbox", name, "include_wip", includeWIP) //nolint:gosec // G706: name is validated by ValidateName
	}
	if done, doneErr := maybeReportNoChanges(cmd, name, meta, commits, hasWIP, includeWIP, withTags); done {
		return doneErr
	}

	// --patches: export patch files to a directory
	if patchesDir != "" {
		return exportPatches(cmd, name, paths, commits, hasWIP, includeWIP, patchesDir)
	}

	targetDir := meta.Workdir.HostPath
	isGit := workspace.IsGitRepo(targetDir)

	// Non-git fallback: can't use git am on non-git targets
	if !isGit && len(commits) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "Note: target is not a git repository — falling back to squashed patch") //nolint:errcheck
		return applySquash(cmd, name, paths, meta, yes, dryRun, includeWIP)
	}

	// No commits, only WIP (user opted in) — use existing squash flow.
	if len(commits) == 0 && hasWIP && includeWIP {
		if withTags {
			return sandbox.NewUsageError("--tags requires commits — cannot transfer tags with WIP-only changes")
		}
		return applySquash(cmd, name, paths, meta, yes, dryRun, includeWIP)
	}

	return runApplyCommits(cmd, name, paths, meta, commits, hasWIP, yes, dryRun, includeWIP, withTags)
}

// printWIPHint tells the user there are uncommitted edits they could pull in
// via --include-wip. Human-mode only; JSON output skips it.
func printWIPHint(cmd *cobra.Command, reason string) {
	fmt.Fprintf(cmd.OutOrStdout(), "Note: sandbox has uncommitted changes (%s); re-run with --include-wip to apply them.\n", reason) //nolint:errcheck
}

// reportWIPSkipHint prints the WIP-skipped hint after commits land. Human-mode only.
func reportWIPSkipHint(cmd *cobra.Command, hasWIP, includeWIP bool) {
	if hasWIP && !includeWIP && !jsonEnabled(cmd) {
		printWIPHint(cmd, "not applied — commits only")
	}
}

// reportUnappliedTagsHint suggests --tags when sandbox has tags the user
// didn't ask to transfer. Human-mode only.
func reportUnappliedTagsHint(cmd *cobra.Command, name string, withTags bool) {
	if jsonEnabled(cmd) || withTags {
		return
	}
	unappliedTags, _ := sandbox.ListUnappliedTags(cliLayout(), name)
	if len(unappliedTags) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "\nHint: %d tag(s) available in sandbox but not on host. Run with --tags to transfer them.\n", len(unappliedTags)) //nolint:errcheck
	}
}

// maybeReportNoChanges handles the "nothing to apply" case at the entry of
// the format-patch flow. Returns (true, err) when the caller should return;
// (false, nil) to continue with the normal apply.
func maybeReportNoChanges(cmd *cobra.Command, name string, meta *store.Meta, commits []patch.CommitInfo, hasWIP, includeWIP, withTags bool) (bool, error) {
	if len(commits) > 0 {
		return false, nil
	}
	if hasWIP && includeWIP {
		return false, nil // WIP-only apply will proceed via the squash fallback
	}
	if hasWIP && !jsonEnabled(cmd) {
		printWIPHint(cmd, "no committed changes to apply")
	}
	return true, runApplyNoChanges(cmd, name, meta, withTags)
}

// runApplyNoChanges handles the case where there are no commits or WIP to apply.
func runApplyNoChanges(cmd *cobra.Command, name string, meta *store.Meta, withTags bool) error {
	layout := cliLayout()
	// Check for unapplied tags even when there are no changes
	unappliedTags, _ := sandbox.ListUnappliedTags(layout, name)

	// If --tags is used, transfer tags even without commits
	if withTags && len(unappliedTags) > 0 {
		targetDir := meta.Workdir.HostPath
		workDir := store.WorkDir(layout.SandboxDir(name), meta.Workdir.HostPath)
		if !jsonEnabled(cmd) {
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
		if jsonEnabled(cmd) {
			return writeJSON(cmd.OutOrStdout(), applyResult{
				Target:      meta.Workdir.HostPath,
				TagsApplied: tagsApplied,
				TagsSkipped: tagsSkipped,
				Method:      "format-patch",
			})
		}
		return nil
	}

	if jsonEnabled(cmd) {
		return writeJSON(cmd.OutOrStdout(), applyResult{
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

// runApplyCommits applies commits via format-patch/am to the target directory.
func runApplyCommits(cmd *cobra.Command, name string, paths []string, meta *store.Meta, commits []patch.CommitInfo, hasWIP, yes, dryRun, includeWIP, withTags bool) error {
	layout := cliLayout()
	targetDir := meta.Workdir.HostPath
	sandboxWorkDir := store.WorkDir(layout.SandboxDir(name), meta.Workdir.HostPath)
	isGit := workspace.IsGitRepo(targetDir)
	backend := resolveBackendForSandbox(name)

	// Fetch tags beyond baseline (best-effort; errors don't fail the apply).
	tags, _ := sandbox.ListTagsBeyondBaseline(layout, name)
	tagsByCommit := buildTagsByCommit(tags)

	printApplyCommitsSummary(cmd, commits, tags, tagsByCommit, hasWIP, includeWIP, withTags)

	if dryRun {
		if !jsonEnabled(cmd) {
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

	commitsApplied, shaMap, stashErr, err := applyFormatPatchFiles(cmd, name, paths, targetDir, backend)
	if err != nil {
		return err
	}

	// Advance baseline past applied commits (skip for path-filtered applies)
	if len(paths) == 0 {
		if err := withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
			return c.AdvanceBaseline(ctx, name)
		}); err != nil {
			return fmt.Errorf("advance baseline: %w", err)
		}
	}

	if stashErr != nil {
		return stashErr
	}

	wipApplied := applyWIPChanges(cmd, name, paths, targetDir, isGit, hasWIP && includeWIP, backend)
	reportWIPSkipHint(cmd, hasWIP, includeWIP)
	tagsApplied, tagsSkipped := applyTags(cmd, tags, shaMap, sandboxWorkDir, targetDir, withTags)
	reportUnappliedTagsHint(cmd, name, withTags)

	slog.Info("apply complete", "event", "sandbox.apply.complete", "sandbox", name, "commits_applied", commitsApplied, "wip_applied", wipApplied, "tags_applied", tagsApplied) //nolint:gosec // G706: name is validated by ValidateName
	if jsonEnabled(cmd) {
		return writeJSON(cmd.OutOrStdout(), applyResult{
			Target:         targetDir,
			CommitsApplied: commitsApplied,
			WIPApplied:     wipApplied,
			TagsApplied:    tagsApplied,
			TagsSkipped:    tagsSkipped,
			Method:         "format-patch",
		})
	}

	return nil
}

// printApplyCommitsSummary prints the list of commits about to be applied (human-readable only).
func printApplyCommitsSummary(cmd *cobra.Command, commits []patch.CommitInfo, tags []sandbox.TagInfo, tagsByCommit map[string][]string, hasWIP, includeWIP, withTags bool) {
	if jsonEnabled(cmd) {
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
	case hasWIP && includeWIP:
		fmt.Fprintln(out, "\n+ uncommitted changes (will be applied as unstaged files)") //nolint:errcheck
	case hasWIP:
		fmt.Fprintln(out, "\n  (sandbox also has uncommitted changes — not applied; re-run with --include-wip to include)") //nolint:errcheck
	}
	if len(tags) > 0 && !withTags {
		fmt.Fprintf(out, "\nWARNING: %d tag(s) will NOT be applied (cancel this apply and redo with --tags to include them)\n", len(tags)) //nolint:errcheck
	}
	fmt.Fprintln(out) //nolint:errcheck
}

// applyFormatPatchFiles generates a format-patch and applies it, returning stats and any deferred error.
func applyFormatPatchFiles(cmd *cobra.Command, name string, paths []string, targetDir, backend string) (commitsApplied int, shaMap map[string]string, stashErr, err error) {
	var patchDir string
	var files []string
	if err = withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var genErr error
		patchDir, files, genErr = c.GenerateFormatPatch(ctx, name, paths)
		return genErr
	}); err != nil {
		return 0, nil, nil, err
	}
	defer os.RemoveAll(patchDir) //nolint:errcheck // best-effort cleanup

	if len(files) == 0 {
		return 0, nil, nil, nil
	}

	shaMap, err = workspace.ApplyFormatPatch(patchDir, files, targetDir)
	if err != nil && shaMap == nil {
		// git am itself failed; nothing was applied.
		return 0, nil, nil, err
	}
	stashErr = err
	commitsApplied = len(files)
	if !jsonEnabled(cmd) {
		fmt.Fprintf(cmd.OutOrStdout(), "%d commit(s) applied to %s\n", len(files), targetDir) //nolint:errcheck
	}
	return commitsApplied, shaMap, stashErr, nil
}

// applyWIPChanges applies uncommitted changes from sandbox to the target directory.
// Returns true if WIP was applied successfully.
func applyWIPChanges(cmd *cobra.Command, name string, paths []string, targetDir string, isGit, hasWIP bool, backend string) bool {
	if !hasWIP {
		return false
	}
	var wipPatch []byte
	wipErr := withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var genErr error
		wipPatch, _, genErr = c.GenerateWIPDiff(ctx, name, paths)
		return genErr
	})
	if wipErr != nil {
		if !jsonEnabled(cmd) {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to generate WIP diff: %v\n", wipErr) //nolint:errcheck
		}
		return false
	}
	if len(wipPatch) == 0 {
		return false
	}
	if err := workspace.ApplyPatch(wipPatch, targetDir, isGit); err != nil {
		if !jsonEnabled(cmd) {
			fmt.Fprintf(cmd.ErrOrStderr(), //nolint:errcheck // best-effort warning
				"Warning: failed to apply WIP changes: %v\n"+
					"Commits were applied successfully. WIP changes need manual application.\n", err)
		}
		return false
	}
	if !jsonEnabled(cmd) {
		fmt.Fprintln(cmd.OutOrStdout(), "Uncommitted changes applied (unstaged)") //nolint:errcheck
	}
	return true
}
