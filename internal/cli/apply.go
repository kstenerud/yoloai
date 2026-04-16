package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/workspace"
	"github.com/spf13/cobra"
)

// applyResult holds JSON output for the apply command.
type applyResult struct {
	Target         string `json:"target"`
	CommitsApplied int    `json:"commits_applied"`
	WIPApplied     bool   `json:"wip_applied"`
	TagsApplied    int    `json:"tags_applied"`
	TagsSkipped    int    `json:"tags_skipped"`
	Method         string `json:"method"` // "format-patch", "squash", "selective", "patches-export"
}

func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply <name> [<ref>...] [-- <path>...]",
		Short: "Apply agent changes back to original work directory",
		Long: `Apply agent changes back to the original directory.

By default, individual commits are preserved using git format-patch/am.
Uncommitted (WIP) changes are applied as unstaged modifications.

Specific commits can be cherry-picked by providing ref arguments:
  yoloai apply mybox abc123 def456       # specific commits
  yoloai apply mybox abc123..def456      # range
  yoloai apply mybox                     # all (unchanged behavior)

Use --squash to flatten everything into a single unstaged patch.
Use --patches to export .patch files without applying them.`,
		GroupID: groupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, rest, err := resolveName(cmd, args)
			if err != nil {
				return err
			}
			defer openCLIJSONLSink(name, cmd)()

			yes := effectiveYes(cmd)
			squash, _ := cmd.Flags().GetBool("squash")
			patchesDir, _ := cmd.Flags().GetString("patches")
			if patchesDir != "" {
				var expandErr error
				patchesDir, expandErr = sandbox.ExpandPath(patchesDir)
				if expandErr != nil {
					return fmt.Errorf("expand patches path: %w", expandErr)
				}
			}
			noWIP, _ := cmd.Flags().GetBool("no-wip")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			withTags, _ := cmd.Flags().GetBool("tags")

			// Parse refs and paths from remaining args
			refs, paths := parseApplyArgs(rest, cmd)

			// Validate mutually exclusive options
			if len(refs) > 0 && squash {
				return sandbox.NewUsageError("--squash cannot be used with commit refs — they are mutually exclusive")
			}
			// Load metadata for target directory and mode validation
			meta, err := sandbox.LoadMeta(sandbox.Dir(name))
			if err != nil {
				return sandboxErrorHint(name, err)
			}
			if meta.Workdir.Mode == "rw" {
				return sandbox.NewUsageError("apply is not needed for :rw directories — changes are already live")
			}

			slog.Info("applying changes", "event", "sandbox.apply", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
			if hasOverlayDirs(meta) {
				return applyOverlay(cmd, name, meta, refs, paths, patchesDir, noWIP, yes, dryRun)
			}

			if !jsonEnabled(cmd) {
				fmt.Fprintf(cmd.OutOrStdout(), "Target: %s\n\n", meta.Workdir.HostPath) //nolint:errcheck
			}

			// Best-effort agent-running warning
			if !jsonEnabled(cmd) {
				agentRunningWarning(cmd, name)
			}

			// Selective apply: specific commit refs
			if len(refs) > 0 {
				return applySelectedCommits(cmd, name, refs, paths, meta, yes, dryRun, withTags)
			}

			// --squash: flatten everything into one unstaged patch
			if squash {
				return applySquash(cmd, name, paths, meta, yes, dryRun)
			}

			// Query work copy for commits and WIP
			backend := resolveBackendForSandbox(name)
			var commits []sandbox.CommitInfo
			err = withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				var listErr error
				commits, listErr = sandbox.ListCommitsBeyondBaseline(ctx, rt, name)
				return listErr
			})
			if err != nil {
				return err
			}

			var hasWIP bool
			if !noWIP {
				err = withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
					var wipErr error
					hasWIP, wipErr = sandbox.HasUncommittedChanges(ctx, rt, name)
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
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
				// Inform user if tags are available but not on host
				if len(unappliedTags) > 0 && !withTags {
					fmt.Fprintf(cmd.OutOrStdout(), "\nHint: %d tag(s) available in sandbox but not on host. Run with --tags to transfer them.\n", len(unappliedTags)) //nolint:errcheck
				}
				return err
			}

			// --patches: export patch files to a directory
			if patchesDir != "" {
				return exportPatches(cmd, name, paths, commits, hasWIP, patchesDir)
			}

			targetDir := meta.Workdir.HostPath
			sandboxWorkDir := sandbox.WorkDir(name, meta.Workdir.HostPath)
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

			// Fetch tags beyond baseline (best-effort; errors don't fail the apply).
			tags, _ := sandbox.ListTagsBeyondBaseline(name)
			tagsByCommit := buildTagsByCommit(tags)

			// Show summary
			if !jsonEnabled(cmd) {
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

			// --dry-run: show summary and stop
			if dryRun {
				if !jsonEnabled(cmd) {
					fmt.Fprintln(cmd.OutOrStdout(), "(dry run)") //nolint:errcheck
				}
				return nil
			}

			// Confirmation
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

			// Apply commits via format-patch/am
			var patchDir string
			var files []string
			err = withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				var genErr error
				patchDir, files, genErr = sandbox.GenerateFormatPatch(ctx, rt, name, paths)
				return genErr
			})
			if err != nil {
				return err
			}
			defer os.RemoveAll(patchDir) //nolint:errcheck // best-effort cleanup

			commitsApplied := 0
			var shaMap map[string]string
			if len(files) > 0 {
				shaMap, err = workspace.ApplyFormatPatch(patchDir, files, targetDir)
				if err != nil {
					return err
				}
				commitsApplied = len(files)
				if !jsonEnabled(cmd) {
					fmt.Fprintf(cmd.OutOrStdout(), "%d commit(s) applied to %s\n", len(files), targetDir) //nolint:errcheck
				}
			}

			// Advance baseline past applied commits (skip for path-filtered applies)
			if len(paths) == 0 {
				err = withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
					return sandbox.AdvanceBaseline(ctx, rt, name)
				})
				if err != nil {
					return fmt.Errorf("advance baseline: %w", err)
				}
			}

			// Apply WIP changes
			wipApplied := false
			if hasWIP {
				var wipPatch []byte
				wipErr := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
					var genErr error
					wipPatch, _, genErr = sandbox.GenerateWIPDiff(ctx, rt, name, paths)
					return genErr
				})
				if wipErr != nil {
					if !jsonEnabled(cmd) {
						fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to generate WIP diff: %v\n", wipErr) //nolint:errcheck
					}
				} else if len(wipPatch) > 0 {
					if err := workspace.ApplyPatch(wipPatch, targetDir, isGit); err != nil {
						if !jsonEnabled(cmd) {
							fmt.Fprintf(cmd.ErrOrStderr(), //nolint:errcheck // best-effort warning
								"Warning: failed to apply WIP changes: %v\n"+
									"Commits were applied successfully. WIP changes need manual application.\n", err)
						}
					} else {
						wipApplied = true
						if !jsonEnabled(cmd) {
							fmt.Fprintln(cmd.OutOrStdout(), "Uncommitted changes applied (unstaged)") //nolint:errcheck
						}
					}
				}
			}

			// Apply tags
			tagsApplied, tagsSkipped := applyTags(cmd, tags, shaMap, sandboxWorkDir, targetDir, withTags)

			// Inform user if tags remain unapplied
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
		},
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().Bool("squash", false, "Flatten all changes into a single unstaged patch")
	cmd.Flags().String("patches", "", "Export .patch files to directory instead of applying")
	cmd.Flags().Bool("no-wip", false, "Skip uncommitted changes, only apply commits")
	cmd.Flags().Bool("dry-run", false, "Show what would be applied without applying")
	cmd.Flags().Bool("tags", false, "Transfer git tags created by the agent")

	cmd.MarkFlagsMutuallyExclusive("squash", "patches")
	cmd.MarkFlagsMutuallyExclusive("squash", "no-wip")
	cmd.MarkFlagsMutuallyExclusive("squash", "tags")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "patches")

	return cmd
}

// applyOverlay handles apply for sandboxes with overlay directories.
func applyOverlay(cmd *cobra.Command, name string, meta *sandbox.Meta, refs, paths []string, patchesDir string, noWIP, yes, dryRun bool) error {
	// Reject unsupported flag combos for overlay
	if len(refs) > 0 {
		return sandbox.NewPlatformError("selective ref apply is not supported for :overlay sandboxes")
	}
	if noWIP {
		return sandbox.NewPlatformError("--no-wip is not supported for :overlay sandboxes (no commit/WIP separation)")
	}

	backend := resolveBackendForSandbox(name)
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		if err := requireOverlayRunning(ctx, rt, name); err != nil {
			return err
		}

		patches, err := sandbox.GenerateOverlayPatch(ctx, rt, name, paths)
		if err != nil {
			return err
		}

		if len(patches) == 0 {
			if jsonEnabled(cmd) {
				return writeJSON(cmd.OutOrStdout(), applyResult{
					Target: meta.Workdir.HostPath,
					Method: "overlay",
				})
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
			return err
		}

		// --patches: export patch files
		if patchesDir != "" {
			if err := fileutil.MkdirAll(patchesDir, 0750); err != nil {
				return fmt.Errorf("create patches directory: %w", err)
			}
			for i, ps := range patches {
				dst := filepath.Join(patchesDir, fmt.Sprintf("overlay-%d.diff", i+1))
				if err := fileutil.WriteFile(dst, ps.Patch, 0600); err != nil { //nolint:gosec // G703: dst is constructed from user-provided --patches flag
					return fmt.Errorf("write patch: %w", err)
				}
				if !jsonEnabled(cmd) {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", dst) //nolint:errcheck
				}
			}
			if jsonEnabled(cmd) {
				return writeJSON(cmd.OutOrStdout(), applyResult{
					Target:     patchesDir,
					WIPApplied: true,
					Method:     "overlay",
				})
			}
			return nil
		}

		// Show summary
		isJSON := jsonEnabled(cmd)
		out := cmd.OutOrStdout()
		if !isJSON {
			for _, ps := range patches {
				fmt.Fprintf(out, "=== %s (%s) ===\n", ps.HostPath, ps.Mode) //nolint:errcheck
				fmt.Fprintln(out, ps.Stat)                                  //nolint:errcheck
			}
		}

		// --dry-run: show summary and stop
		if dryRun {
			if !isJSON {
				fmt.Fprintln(out, "(dry run)") //nolint:errcheck
			}
			return nil
		}

		// Confirmation
		if !yes {
			confirmed, confirmErr := sandbox.Confirm(cmd.Context(), "Apply these changes? [y/N] ", os.Stdin, cmd.ErrOrStderr())
			if confirmErr != nil {
				return confirmErr
			}
			if !confirmed {
				return nil
			}
		}

		// Apply each patch to host
		for _, ps := range patches {
			isGit := workspace.IsGitRepo(ps.HostPath)
			if err := workspace.ApplyPatch(ps.Patch, ps.HostPath, isGit); err != nil {
				return fmt.Errorf("%s: %w", ps.HostPath, err)
			}
			if !isJSON {
				fmt.Fprintf(out, "Changes applied to %s\n", ps.HostPath) //nolint:errcheck
			}
		}

		// Advance overlay baseline
		for _, ps := range patches {
			if err := sandbox.UpdateOverlayBaselineToHEAD(ctx, rt, name, ps.HostPath); err != nil {
				return fmt.Errorf("advance overlay baseline: %w", err)
			}
		}

		if isJSON {
			return writeJSON(out, applyResult{
				Target:     meta.Workdir.HostPath,
				WIPApplied: true,
				Method:     "overlay",
			})
		}

		return nil
	})
}

// parseApplyArgs separates ref arguments from path arguments.
// Refs appear between the sandbox name and "--"; paths appear after "--".
// Without "--", all remaining args are treated as refs if they look like
// hex SHA prefixes or ranges, otherwise all are treated as paths.
func parseApplyArgs(rest []string, cmd *cobra.Command) (refs []string, paths []string) {
	if len(rest) == 0 {
		return nil, nil
	}

	dashAt := cmd.ArgsLenAtDash()
	if dashAt >= 0 {
		// Explicit "--" separator. Account for name already consumed.
		beforeDash := dashAt - 1
		if beforeDash < 0 {
			beforeDash = 0
		}
		if beforeDash > len(rest) {
			beforeDash = len(rest)
		}
		refs = rest[:beforeDash]
		paths = rest[beforeDash:]
		return refs, paths
	}

	// No "--": check if all args look like refs
	allRefs := true
	for _, arg := range rest {
		if !looksLikeRef(arg) {
			allRefs = false
			break
		}
	}

	if allRefs {
		return rest, nil
	}

	// If the first arg doesn't look like a ref, they're all paths (backward compat)
	return nil, rest
}

// applySelectedCommits cherry-picks specific commits into the target.
func applySelectedCommits(cmd *cobra.Command, name string, refs, paths []string, meta *sandbox.Meta, yes, dryRun, withTags bool) error {
	targetDir := meta.Workdir.HostPath
	sandboxWorkDir := sandbox.WorkDir(name, meta.Workdir.HostPath)
	if !workspace.IsGitRepo(targetDir) {
		return fmt.Errorf("selective apply requires a git target directory — %s is not a git repository", targetDir)
	}

	// Resolve refs to full SHAs
	backend := resolveBackendForSandbox(name)
	var resolved []sandbox.CommitInfo
	err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		var resolveErr error
		resolved, resolveErr = sandbox.ResolveRefs(ctx, rt, name, refs)
		return resolveErr
	})
	if err != nil {
		return err
	}

	if len(resolved) == 0 {
		if jsonEnabled(cmd) {
			return writeJSON(cmd.OutOrStdout(), applyResult{
				Target: targetDir,
				Method: "selective",
			})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No commits matched")
		return err
	}

	// Fetch tags (best-effort); filter to those on selected commits.
	allTags, _ := sandbox.ListTagsBeyondBaseline(name)
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
	tagsByCommit := buildTagsByCommit(selectedTags)

	// Show summary
	if !jsonEnabled(cmd) {
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

	// --dry-run: show summary and stop
	if dryRun {
		if !jsonEnabled(cmd) {
			fmt.Fprintln(cmd.OutOrStdout(), "(dry run)") //nolint:errcheck
		}
		return nil
	}

	// Confirmation
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

	// Generate patches for selected commits only
	shas := make([]string, len(resolved))
	for i, c := range resolved {
		shas[i] = c.SHA
	}

	backend = resolveBackendForSandbox(name)
	var patchDir string
	var files []string
	err = withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		var genErr error
		patchDir, files, genErr = sandbox.GenerateFormatPatchForRefs(ctx, rt, name, shas, paths)
		return genErr
	})
	if err != nil {
		return err
	}
	defer os.RemoveAll(patchDir) //nolint:errcheck

	shaMap, err := workspace.ApplyFormatPatch(patchDir, files, targetDir)
	if err != nil {
		return err
	}
	if !jsonEnabled(cmd) {
		fmt.Fprintf(cmd.OutOrStdout(), "%d commit(s) applied to %s\n", len(files), targetDir) //nolint:errcheck
	}

	// Advance baseline using contiguous prefix logic (skip for path-filtered applies)
	if len(paths) == 0 {
		var allCommits []sandbox.CommitInfo
		err = withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
			var listErr error
			allCommits, listErr = sandbox.ListCommitsBeyondBaseline(ctx, rt, name)
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
			if err := sandbox.AdvanceBaselineTo(name, allCommits[prefixEnd].SHA); err != nil {
				return fmt.Errorf("advance baseline: %w", err)
			}
		}
	}

	// Apply tags
	tagsApplied, tagsSkipped := applyTags(cmd, selectedTags, shaMap, sandboxWorkDir, targetDir, withTags)

	// Inform user if tags remain unapplied
	if !jsonEnabled(cmd) && !withTags {
		unappliedTags, _ := sandbox.ListUnappliedTags(name)
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

	return nil
}

// applySquash implements the squashed-patch apply mode.
func applySquash(cmd *cobra.Command, name string, paths []string, meta *sandbox.Meta, yes, dryRun bool) error {
	// Check for aux :copy dirs
	if len(meta.Directories) > 0 {
		backend := resolveBackendForSandbox(name)
		return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
			return applySquashMulti(cmd, ctx, rt, name, paths, meta, yes, dryRun)
		})
	}

	var patch []byte
	var stat string
	backend := resolveBackendForSandbox(name)
	err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		var genErr error
		patch, stat, genErr = sandbox.GeneratePatch(ctx, rt, name, paths)
		return genErr
	})
	if err != nil {
		return err
	}
	if len(patch) == 0 {
		if jsonEnabled(cmd) {
			return writeJSON(cmd.OutOrStdout(), applyResult{
				Target: meta.Workdir.HostPath,
				Method: "squash",
			})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
		return err
	}

	targetDir := meta.Workdir.HostPath
	if !jsonEnabled(cmd) {
		fmt.Fprintln(cmd.OutOrStdout(), stat) //nolint:errcheck
	}

	if dryRun {
		if !jsonEnabled(cmd) {
			fmt.Fprintln(cmd.OutOrStdout(), "(dry run)") //nolint:errcheck
		}
		return nil
	}

	isGit := workspace.IsGitRepo(targetDir)

	if err := workspace.CheckPatch(patch, targetDir, isGit); err != nil {
		return err
	}

	if !yes {
		prompt := fmt.Sprintf("Apply these changes to %s? [y/N] ", targetDir)
		confirmed, confirmErr := sandbox.Confirm(cmd.Context(), prompt, os.Stdin, cmd.ErrOrStderr())
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			return nil
		}
	}

	if err := workspace.ApplyPatch(patch, targetDir, isGit); err != nil {
		return err
	}

	// Advance baseline past applied changes (skip for path-filtered applies)
	if len(paths) == 0 {
		backend := resolveBackendForSandbox(name)
		err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
			return sandbox.AdvanceBaseline(ctx, rt, name)
		})
		if err != nil {
			return fmt.Errorf("advance baseline: %w", err)
		}
	}

	if jsonEnabled(cmd) {
		return writeJSON(cmd.OutOrStdout(), applyResult{
			Target:     targetDir,
			WIPApplied: true,
			Method:     "squash",
		})
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Changes applied to %s\n", targetDir)
	return err
}

// applySquashMulti applies squashed patches for multiple :copy directories.
func applySquashMulti(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime, name string, paths []string, _ *sandbox.Meta, yes, dryRun bool) error {
	patches, err := sandbox.GenerateMultiPatch(ctx, rt, name, paths)
	if err != nil {
		return err
	}
	if len(patches) == 0 {
		if jsonEnabled(cmd) {
			return writeJSON(cmd.OutOrStdout(), applyResult{
				Target: "multi",
				Method: "squash",
			})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
		return err
	}

	isJSON := jsonEnabled(cmd)
	out := cmd.OutOrStdout()
	if !isJSON {
		for _, ps := range patches {
			fmt.Fprintf(out, "=== %s (%s) ===\n", ps.HostPath, ps.Mode) //nolint:errcheck
			fmt.Fprintln(out, ps.Stat)                                  //nolint:errcheck
		}
	}

	if dryRun {
		if !isJSON {
			fmt.Fprintln(out, "(dry run)") //nolint:errcheck
		}
		return nil
	}

	if !yes {
		confirmed, confirmErr := sandbox.Confirm(cmd.Context(), "Apply these changes? [y/N] ", os.Stdin, cmd.ErrOrStderr())
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			return nil
		}
	}

	for _, ps := range patches {
		isGit := workspace.IsGitRepo(ps.HostPath)
		if err := workspace.CheckPatch(ps.Patch, ps.HostPath, isGit); err != nil {
			return fmt.Errorf("%s: %w", ps.HostPath, err)
		}
		if err := workspace.ApplyPatch(ps.Patch, ps.HostPath, isGit); err != nil {
			return fmt.Errorf("%s: %w", ps.HostPath, err)
		}
		if !isJSON {
			fmt.Fprintf(out, "Changes applied to %s\n", ps.HostPath) //nolint:errcheck
		}
	}

	// Advance baseline for workdir
	if len(paths) == 0 {
		if err := sandbox.AdvanceBaseline(ctx, rt, name); err != nil {
			return fmt.Errorf("advance baseline: %w", err)
		}
	}

	if isJSON {
		return writeJSON(out, applyResult{
			Target:     "multi",
			WIPApplied: true,
			Method:     "squash",
		})
	}

	return nil
}

// exportPatches writes .patch files and optional wip.diff to the given directory.
func exportPatches(cmd *cobra.Command, name string, paths []string, commits []sandbox.CommitInfo, hasWIP bool, dir string) error {
	if err := fileutil.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create patches directory: %w", err)
	}

	isJSON := jsonEnabled(cmd)
	out := cmd.OutOrStdout()

	if len(commits) > 0 {
		backend := resolveBackendForSandbox(name)
		var patchDir string
		var files []string
		err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
			var genErr error
			patchDir, files, genErr = sandbox.GenerateFormatPatch(ctx, rt, name, paths)
			return genErr
		})
		if err != nil {
			return err
		}
		defer os.RemoveAll(patchDir) //nolint:errcheck

		for _, f := range files {
			src := filepath.Join(patchDir, f)
			dst := filepath.Join(dir, f)
			data, err := os.ReadFile(src) //nolint:gosec // G304: controlled path
			if err != nil {
				return fmt.Errorf("read patch %s: %w", f, err)
			}
			if err := fileutil.WriteFile(dst, data, 0600); err != nil { //nolint:gosec // G703: dst is under controlled dir
				return fmt.Errorf("write patch %s: %w", f, err)
			}
			if !isJSON {
				fmt.Fprintf(out, "  %s\n", dst) //nolint:errcheck
			}
		}
	}

	if hasWIP {
		backend := resolveBackendForSandbox(name)
		var wipPatch []byte
		err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
			var genErr error
			wipPatch, _, genErr = sandbox.GenerateWIPDiff(ctx, rt, name, paths)
			return genErr
		})
		if err != nil {
			return err
		}
		if len(wipPatch) > 0 {
			dst := filepath.Join(dir, "wip.diff")
			if err := fileutil.WriteFile(dst, wipPatch, 0600); err != nil {
				return fmt.Errorf("write wip.diff: %w", err)
			}
			if !isJSON {
				fmt.Fprintf(out, "  %s\n", dst) //nolint:errcheck
			}
		}
	}

	if isJSON {
		return writeJSON(out, applyResult{
			Target:         dir,
			CommitsApplied: len(commits),
			WIPApplied:     hasWIP,
			Method:         "patches-export",
		})
	}

	fmt.Fprintln(out)                                                       //nolint:errcheck
	fmt.Fprintln(out, "To apply commits:  git am --3way <patches>/*.patch") //nolint:errcheck
	fmt.Fprintln(out, "To apply WIP:      git apply wip.diff")              //nolint:errcheck

	return nil
}

// buildTagsByCommit builds a map of lowercase commit SHA → tag names from a tag list.
func buildTagsByCommit(tags []sandbox.TagInfo) map[string][]string {
	m := make(map[string][]string, len(tags))
	for _, t := range tags {
		key := strings.ToLower(t.SHA)
		m[key] = append(m[key], t.Name)
	}
	return m
}

// applyTags transfers tags to the host using the sandbox→host SHA map.
// sandboxWorkDir is used to fetch the full tag message (which is not stored
// in TagInfo to keep tag listing fast and reliable).
// Returns counts of applied and skipped tags. No-ops if withTags is false.
func applyTags(cmd *cobra.Command, tags []sandbox.TagInfo, shaMap map[string]string, sandboxWorkDir, targetDir string, withTags bool) (applied, skipped int) {
	if !withTags || len(tags) == 0 {
		return 0, 0
	}
	isJSON := jsonEnabled(cmd)
	for _, tag := range tags {
		hostSHA, ok := shaMap[strings.ToLower(tag.SHA)]
		if !ok {
			skipped++
			if !isJSON {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: tag %q skipped (target commit not applied)\n", tag.Name) //nolint:errcheck
			}
			continue
		}
		// Fetch full tag message from sandbox
		message := sandbox.GetTagMessage(sandboxWorkDir, tag.Name)
		if createErr := workspace.CreateTag(targetDir, tag.Name, hostSHA, message); createErr != nil {
			skipped++
			if !isJSON {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: tag %q: %v\n", tag.Name, createErr) //nolint:errcheck
			}
		} else {
			applied++
			if !isJSON {
				fmt.Fprintf(cmd.OutOrStdout(), "Tag %q applied\n", tag.Name) //nolint:errcheck
			}
		}
	}
	return applied, skipped
}
