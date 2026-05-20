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

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/sandbox/patch"
	"github.com/kstenerud/yoloai/workspace"
	"github.com/spf13/cobra"
)

// runApplyFormatPatch handles the default format-patch apply flow.
func runApplyFormatPatch(cmd *cobra.Command, name string, paths []string, meta *sandbox.Meta, patchesDir string, yes, dryRun, noWIP, withTags bool) error {
	// Query work copy for commits and WIP
	backend := resolveBackendForSandbox(name)
	var commits []patch.CommitInfo
	err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		var listErr error
		commits, listErr = patch.ListCommitsBeyondBaseline(ctx, rt, name)
		return listErr
	})
	if err != nil {
		return err
	}

	var hasWIP bool
	if !noWIP {
		err = withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
			var wipErr error
			hasWIP, wipErr = patch.HasUncommittedChanges(ctx, rt, name)
			return wipErr
		})
		if err != nil {
			return err
		}
	}

	slog.Debug("commits to apply", "event", "sandbox.apply.commits", "sandbox", name, "count", len(commits)) //nolint:gosec // G706: name is validated by ValidateName
	if hasWIP {
		slog.Debug("WIP to apply", "event", "sandbox.apply.wip", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
	}
	if len(commits) == 0 && !hasWIP {
		return runApplyNoChanges(cmd, name, meta, withTags)
	}

	// --patches: export patch files to a directory
	if patchesDir != "" {
		return exportPatches(cmd, name, paths, commits, hasWIP, patchesDir)
	}

	targetDir := meta.Workdir.HostPath
	isGit := workspace.IsGitRepo(targetDir)

	// Non-git fallback: can't use git am on non-git targets
	if !isGit && len(commits) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "Note: target is not a git repository — falling back to squashed patch") //nolint:errcheck
		return applySquash(cmd, name, paths, meta, yes, dryRun)
	}

	// No commits, only WIP — use existing squash flow (HEAD == baseline equivalent)
	if len(commits) == 0 && hasWIP {
		if withTags {
			return sandbox.NewUsageError("--tags requires commits — cannot transfer tags with WIP-only changes")
		}
		return applySquash(cmd, name, paths, meta, yes, dryRun)
	}

	return runApplyCommits(cmd, name, paths, meta, commits, hasWIP, yes, dryRun, withTags)
}

// runApplyNoChanges handles the case where there are no commits or WIP to apply.
func runApplyNoChanges(cmd *cobra.Command, name string, meta *sandbox.Meta, withTags bool) error {
	// Check for unapplied tags even when there are no changes
	unappliedTags, _ := sandbox.ListUnappliedTags(name)

	// If --tags is used, transfer tags even without commits
	if withTags && len(unappliedTags) > 0 {
		targetDir := meta.Workdir.HostPath
		workDir := sandbox.WorkDir(name, meta.Workdir.HostPath)
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
func runApplyCommits(cmd *cobra.Command, name string, paths []string, meta *sandbox.Meta, commits []patch.CommitInfo, hasWIP, yes, dryRun, withTags bool) error {
	targetDir := meta.Workdir.HostPath
	sandboxWorkDir := sandbox.WorkDir(name, meta.Workdir.HostPath)
	isGit := workspace.IsGitRepo(targetDir)
	backend := resolveBackendForSandbox(name)

	// Fetch tags beyond baseline (best-effort; errors don't fail the apply).
	tags, _ := sandbox.ListTagsBeyondBaseline(name)
	tagsByCommit := buildTagsByCommit(tags)

	printApplyCommitsSummary(cmd, commits, tags, tagsByCommit, hasWIP, withTags)

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
		if err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
			return patch.AdvanceBaseline(ctx, rt, name)
		}); err != nil {
			return fmt.Errorf("advance baseline: %w", err)
		}
	}

	if stashErr != nil {
		return stashErr
	}

	wipApplied := applyWIPChanges(cmd, name, paths, targetDir, isGit, hasWIP, backend)
	tagsApplied, tagsSkipped := applyTags(cmd, tags, shaMap, sandboxWorkDir, targetDir, withTags)

	if !jsonEnabled(cmd) && !withTags {
		unappliedTags, _ := sandbox.ListUnappliedTags(name)
		if len(unappliedTags) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "\nHint: %d tag(s) available in sandbox but not on host. Run with --tags to transfer them.\n", len(unappliedTags)) //nolint:errcheck
		}
	}

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
func printApplyCommitsSummary(cmd *cobra.Command, commits []patch.CommitInfo, tags []sandbox.TagInfo, tagsByCommit map[string][]string, hasWIP, withTags bool) {
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
	if hasWIP {
		fmt.Fprintln(out, "\n+ uncommitted changes (will be applied as unstaged files)") //nolint:errcheck
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
	if err = withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		var genErr error
		patchDir, files, genErr = patch.GenerateFormatPatch(ctx, rt, name, paths)
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
	wipErr := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		var genErr error
		wipPatch, _, genErr = patch.GenerateWIPDiff(ctx, rt, name, paths)
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
