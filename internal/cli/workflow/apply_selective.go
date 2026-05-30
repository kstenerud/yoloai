// ABOUTME: Selective apply workflow — cherry-pick specific commits identified
// ABOUTME: by ref arguments. Routes through Workdir().Apply(ApplyModeCommits) with
// ABOUTME: Refs; the library replays the series and advances the baseline.
package workflow

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/runtime"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/spf13/cobra"
)

// applySelectedCommits cherry-picks specific commits into the target. It previews
// the resolved commits (DryRun) for the summary/confirm, then replays them via
// Workdir().Apply — the library resolves refs, replays the series, and advances
// the baseline across the contiguous applied prefix.
func applySelectedCommits(cmd *cobra.Command, name string, refs, paths []string, meta *store.Meta, yes, dryRun, withTags bool) error {
	targetDir := meta.Workdir.HostPath
	if !workspace.IsGitRepo(targetDir) {
		return fmt.Errorf("selective apply requires a git target directory — %s is not a git repository", targetDir)
	}

	backend := cliutil.ResolveBackendForSandbox(name)

	preview, err := runSeriesApply(cmd, name, backend, refs, paths, true)
	if err != nil {
		return err
	}
	if preview == nil || len(preview.Commits) == 0 {
		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{Target: targetDir, Method: "selective"})
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No commits matched")
		return err
	}

	resolved := commitInfosFromApplied(preview.Commits)
	selectedTags := filterTagsForResolved(name, resolved)
	tagsByCommit := buildTagsByCommit(selectedTags)

	if !cliutil.JSONEnabled(cmd) {
		printSelectiveApplySummary(cmd, resolved, tagsByCommit, selectedTags, withTags)
	}

	if dryRun {
		if !cliutil.JSONEnabled(cmd) {
			fmt.Fprintln(cmd.OutOrStdout(), "(dry run)") //nolint:errcheck
		}
		return nil
	}

	confirmed, confirmErr := confirmSelectiveApply(cmd, yes, targetDir)
	if confirmErr != nil {
		return confirmErr
	}
	if !confirmed {
		return nil
	}

	result, applyErr := runSeriesApply(cmd, name, backend, refs, paths, false)
	if result == nil {
		return applyErr
	}

	if !cliutil.JSONEnabled(cmd) {
		fmt.Fprintf(cmd.OutOrStdout(), "%d commit(s) applied to %s\n", len(result.Commits), targetDir) //nolint:errcheck
	}

	shaMap := make(map[string]string, len(result.Commits))
	for _, c := range result.Commits {
		shaMap[strings.ToLower(c.SourceSHA)] = c.HostSHA
	}

	sandboxWorkDir := store.WorkDir(cliutil.Layout().SandboxDir(name), targetDir)
	return finishSelectiveApply(cmd, name, len(result.Commits), shaMap, applyErr, selectedTags, sandboxWorkDir, targetDir, withTags)
}

// runSeriesApply runs a commit-series apply through the workdir handle — dryRun
// previews the commits that would land; otherwise it replays them. A non-nil
// result with a non-nil error means the commits landed but a follow-on step
// (git am stash, uncommitted changes) had a non-fatal issue.
func runSeriesApply(cmd *cobra.Command, name string, backend runtime.BackendName, refs, paths []string, dryRun bool) (*yoloai.ApplyResult, error) {
	var result *yoloai.ApplyResult
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var applyErr error
		sb, sbErr := c.Sandbox(name)
		if sbErr != nil {
			return sbErr
		}
		result, applyErr = sb.Workdir().Apply(ctx, yoloai.ApplyOptions{
			Mode:   yoloai.ApplyModeCommits,
			Refs:   refs,
			Paths:  paths,
			DryRun: dryRun,
		})
		return applyErr
	})
	return result, err
}

// commitInfosFromApplied converts previewed/applied commits back to the
// CommitInfo shape used for the summary and tag filtering.
func commitInfosFromApplied(applied []yoloai.AppliedCommit) []patch.CommitInfo {
	out := make([]patch.CommitInfo, len(applied))
	for i, c := range applied {
		out[i] = patch.CommitInfo{SHA: c.SourceSHA, Subject: c.Subject}
	}
	return out
}

// confirmSelectiveApply prompts the user if yes is false.
// Returns (true, nil) if confirmed or yes is true, (false, nil) if declined, (false, err) on error.
func confirmSelectiveApply(cmd *cobra.Command, yes bool, targetDir string) (bool, error) {
	if yes {
		return true, nil
	}
	prompt := fmt.Sprintf("Apply to %s? [y/N] ", targetDir)
	confirmed, confirmErr := sandbox.Confirm(cmd.Context(), prompt, os.Stdin, cmd.ErrOrStderr())
	return confirmed, confirmErr
}

// finishSelectiveApply prints results, handles tags, and returns any follow-on error.
func finishSelectiveApply(cmd *cobra.Command, name string, commitsApplied int, shaMap map[string]string, applyErr error, selectedTags []yoloai.TagInfo, sandboxWorkDir, targetDir string, withTags bool) error {
	tagsApplied, tagsSkipped := applyTags(cmd, selectedTags, shaMap, sandboxWorkDir, targetDir, withTags)

	if !cliutil.JSONEnabled(cmd) && !withTags {
		unappliedTags, _ := sandbox.ListUnappliedTags(cliutil.Layout(), name)
		if len(unappliedTags) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "\nHint: %d tag(s) available in sandbox but not on host. Run with --tags to transfer them.\n", len(unappliedTags)) //nolint:errcheck
		}
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
			Target:         targetDir,
			CommitsApplied: commitsApplied,
			TagsApplied:    tagsApplied,
			TagsSkipped:    tagsSkipped,
			Method:         "selective",
		})
	}

	return applyErr
}

// filterTagsForResolved fetches tags beyond baseline and filters to those on the resolved commits.
func filterTagsForResolved(name string, resolved []patch.CommitInfo) []yoloai.TagInfo {
	allTags, _ := sandbox.ListTagsBeyondBaseline(cliutil.Layout(), name)
	resolvedSet := make(map[string]bool, len(resolved))
	for _, c := range resolved {
		resolvedSet[strings.ToLower(c.SHA)] = true
	}
	var selectedTags []yoloai.TagInfo
	for _, t := range allTags {
		if resolvedSet[strings.ToLower(t.SHA)] {
			selectedTags = append(selectedTags, t)
		}
	}
	return selectedTags
}

// printSelectiveApplySummary prints the commit summary for selective apply.
func printSelectiveApplySummary(cmd *cobra.Command, resolved []patch.CommitInfo, tagsByCommit map[string][]string, selectedTags []yoloai.TagInfo, withTags bool) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Commits to apply (%d):\n", len(resolved)) //nolint:errcheck
	for _, c := range resolved {
		line := fmt.Sprintf("  %.12s %s", c.SHA, c.Subject)
		if names := tagsByCommit[strings.ToLower(c.SHA)]; len(names) > 0 {
			line += "  [tag: " + strings.Join(names, ", ") + "]"
		}
		fmt.Fprintln(out, line) //nolint:errcheck
	}
	if len(selectedTags) > 0 && !withTags {
		fmt.Fprintf(out, "\nWARNING: %d tag(s) will NOT be applied (cancel this apply and redo with --tags to include them)\n", len(selectedTags)) //nolint:errcheck
	}
	fmt.Fprintln(out) //nolint:errcheck
}
