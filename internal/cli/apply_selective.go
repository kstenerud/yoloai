// ABOUTME: Selective apply workflow — cherry-pick specific commits identified
// ABOUTME: by ref arguments. Uses format-patch under the hood and advances the
// ABOUTME: baseline only across a contiguous prefix of applied commits.
package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/spf13/cobra"
)

// applySelectedCommits cherry-picks specific commits into the target.
func applySelectedCommits(cmd *cobra.Command, name string, refs, paths []string, meta *store.Meta, yes, dryRun, withTags bool) error {
	targetDir := meta.Workdir.HostPath
	if !workspace.IsGitRepo(targetDir) {
		return fmt.Errorf("selective apply requires a git target directory — %s is not a git repository", targetDir)
	}

	backend := resolveBackendForSandbox(name)
	resolved, err := resolveSelectiveRefs(cmd, name, refs, backend)
	if err != nil {
		return err
	}

	if len(resolved) == 0 {
		if jsonEnabled(cmd) {
			return writeJSON(cmd.OutOrStdout(), applyResult{Target: targetDir, Method: "selective"})
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "No commits matched")
		return err
	}

	selectedTags := filterTagsForResolved(name, resolved)
	tagsByCommit := buildTagsByCommit(selectedTags)

	if !jsonEnabled(cmd) {
		printSelectiveApplySummary(cmd, resolved, tagsByCommit, selectedTags, withTags)
	}

	if dryRun {
		if !jsonEnabled(cmd) {
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

	files, shaMap, stashErr, err := applyFormatPatchForRefs(cmd, name, refs, resolved, paths, targetDir, backend)
	if err != nil {
		return err
	}

	if len(paths) == 0 {
		if err := advanceSelectiveBaseline(cmd, name, backend, resolved); err != nil {
			return err
		}
	}

	return finishSelectiveApply(cmd, name, files, shaMap, stashErr, selectedTags, store.WorkDir(cliLayout().SandboxDir(name), targetDir), targetDir, withTags)
}

// resolveSelectiveRefs resolves the ref arguments to CommitInfo slices.
func resolveSelectiveRefs(cmd *cobra.Command, name string, refs []string, backend string) ([]patch.CommitInfo, error) {
	var resolved []patch.CommitInfo
	if err := withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var resolveErr error
		resolved, resolveErr = c.ResolveCommitRefs(ctx, name, refs)
		return resolveErr
	}); err != nil {
		return nil, err
	}
	return resolved, nil
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

// finishSelectiveApply prints results, handles tags, and returns any stash error.
func finishSelectiveApply(cmd *cobra.Command, name string, files []string, shaMap map[string]string, stashErr error, selectedTags []sandbox.TagInfo, sandboxWorkDir, targetDir string, withTags bool) error {
	tagsApplied, tagsSkipped := applyTags(cmd, selectedTags, shaMap, sandboxWorkDir, targetDir, withTags)

	if !jsonEnabled(cmd) && !withTags {
		unappliedTags, _ := sandbox.ListUnappliedTags(cliLayout(), name)
		if len(unappliedTags) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "\nHint: %d tag(s) available in sandbox but not on host. Run with --tags to transfer them.\n", len(unappliedTags)) //nolint:errcheck
		}
	}

	if jsonEnabled(cmd) {
		return writeJSON(cmd.OutOrStdout(), applyResult{
			Target:         targetDir,
			CommitsApplied: len(files),
			TagsApplied:    tagsApplied,
			TagsSkipped:    tagsSkipped,
			Method:         "selective",
		})
	}

	return stashErr
}

// applyFormatPatchForRefs generates format-patch for specific refs and applies it.
func applyFormatPatchForRefs(cmd *cobra.Command, name string, _ []string, resolved []patch.CommitInfo, paths []string, targetDir, backend string) (files []string, shaMap map[string]string, stashErr, err error) {
	shas := make([]string, len(resolved))
	for i, c := range resolved {
		shas[i] = c.SHA
	}

	var patchDir string
	if err = withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var genErr error
		patchDir, files, genErr = c.GenerateFormatPatchForRefs(ctx, name, shas, paths)
		return genErr
	}); err != nil {
		return nil, nil, nil, err
	}
	defer os.RemoveAll(patchDir) //nolint:errcheck

	shaMap, err = workspace.ApplyFormatPatch(patchDir, files, targetDir)
	if err != nil && shaMap == nil {
		return nil, nil, nil, err
	}
	stashErr = err
	if !jsonEnabled(cmd) {
		fmt.Fprintf(cmd.OutOrStdout(), "%d commit(s) applied to %s\n", len(files), targetDir) //nolint:errcheck
	}
	return files, shaMap, stashErr, nil
}

// filterTagsForResolved fetches tags beyond baseline and filters to those on the resolved commits.
func filterTagsForResolved(name string, resolved []patch.CommitInfo) []sandbox.TagInfo {
	allTags, _ := sandbox.ListTagsBeyondBaseline(cliLayout(), name)
	resolvedSet := make(map[string]bool, len(resolved))
	for _, c := range resolved {
		resolvedSet[strings.ToLower(c.SHA)] = true
	}
	var selectedTags []sandbox.TagInfo
	for _, t := range allTags {
		if resolvedSet[strings.ToLower(t.SHA)] {
			selectedTags = append(selectedTags, t)
		}
	}
	return selectedTags
}

// printSelectiveApplySummary prints the commit summary for selective apply.
func printSelectiveApplySummary(cmd *cobra.Command, resolved []patch.CommitInfo, tagsByCommit map[string][]string, selectedTags []sandbox.TagInfo, withTags bool) {
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

// advanceSelectiveBaseline advances the baseline using contiguous prefix logic after a selective apply.
func advanceSelectiveBaseline(cmd *cobra.Command, name, backend string, resolved []patch.CommitInfo) error {
	var allCommits []patch.CommitInfo
	err := withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var listErr error
		allCommits, listErr = c.ListCommits(ctx, name)
		return listErr
	})
	if err != nil {
		return fmt.Errorf("advance baseline: %w", err)
	}

	appliedSet := make(map[string]bool, len(resolved))
	for _, c := range resolved {
		appliedSet[c.SHA] = true
	}

	prefixEnd := workspace.ContiguousPrefixEnd(allCommits, appliedSet)
	if prefixEnd >= 0 {
		if err := patch.AdvanceBaselineTo(cliLayout(), name, allCommits[prefixEnd].SHA); err != nil {
			return fmt.Errorf("advance baseline: %w", err)
		}
	}
	return nil
}
