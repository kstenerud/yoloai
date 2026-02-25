package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply <name> [<ref>...] [-- <path>...]",
		Short: "Apply agent changes back to original directory",
		Long: `Apply agent changes back to the original directory.

By default, individual commits are preserved using git format-patch/am.
Uncommitted (WIP) changes are applied as unstaged modifications.

Specific commits can be cherry-picked by providing ref arguments:
  yoloai apply mybox abc123 def456       # specific commits
  yoloai apply mybox abc123..def456      # range
  yoloai apply mybox                     # all (unchanged behavior)

Use --squash to flatten everything into a single unstaged patch (legacy behavior).
Use --patches to export .patch files without applying them.`,
		GroupID: groupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, rest, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			yes, _ := cmd.Flags().GetBool("yes")
			squash, _ := cmd.Flags().GetBool("squash")
			patchesDir, _ := cmd.Flags().GetString("patches")
			noWIP, _ := cmd.Flags().GetBool("no-wip")
			force, _ := cmd.Flags().GetBool("force")

			// Parse refs and paths from remaining args
			refs, paths := parseApplyArgs(rest, cmd)

			// Validate mutually exclusive options
			if len(refs) > 0 && squash {
				return fmt.Errorf("--squash cannot be used with commit refs — they are mutually exclusive")
			}
			if len(refs) > 0 && len(paths) > 0 {
				return fmt.Errorf("commit refs and path filters cannot be combined")
			}

			// Load metadata for target directory and mode validation
			meta, err := sandbox.LoadMeta(sandbox.Dir(name))
			if err != nil {
				return err
			}
			if meta.Workdir.Mode == "rw" {
				return fmt.Errorf("apply is not needed for :rw directories — changes are already live")
			}

			// Best-effort agent-running warning
			agentRunningWarning(cmd, name)

			// Selective apply: specific commit refs
			if len(refs) > 0 {
				return applySelectedCommits(cmd, name, refs, meta, yes, force)
			}

			// --squash: legacy behavior — flatten everything into one unstaged patch
			if squash {
				return applySquash(cmd, name, paths, meta, yes)
			}

			// Query work copy for commits and WIP
			commits, err := sandbox.ListCommitsBeyondBaseline(name)
			if err != nil {
				return err
			}

			var hasWIP bool
			if !noWIP {
				hasWIP, err = sandbox.HasUncommittedChanges(name)
				if err != nil {
					return err
				}
			}

			if len(commits) == 0 && !hasWIP {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
				return err
			}

			// --patches: export patch files to a directory
			if patchesDir != "" {
				return exportPatches(cmd, name, paths, commits, hasWIP, patchesDir)
			}

			targetDir := meta.Workdir.HostPath
			isGit := sandbox.IsGitRepo(targetDir)

			// Non-git fallback: can't use git am on non-git targets
			if !isGit && len(commits) > 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "Note: target is not a git repository — falling back to squashed patch") //nolint:errcheck
				return applySquash(cmd, name, paths, meta, yes)
			}

			// No commits, only WIP — use existing squash flow (HEAD == baseline equivalent)
			if len(commits) == 0 && hasWIP {
				return applySquash(cmd, name, paths, meta, yes)
			}

			// Pre-flight: check for dirty repo when applying commits
			if isGit {
				warning, checkErr := sandbox.CheckDirtyRepo(targetDir)
				if checkErr != nil {
					return checkErr
				}
				if warning != "" && !force {
					return fmt.Errorf("target repo has uncommitted changes (%s)\n"+
						"commit or stash them first, or use --force to proceed anyway", warning)
				}
			}

			// Show summary
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Commits to apply (%d):\n", len(commits)) //nolint:errcheck
			for _, c := range commits {
				fmt.Fprintf(out, "  %.12s %s\n", c.SHA, c.Subject) //nolint:errcheck
			}
			if hasWIP {
				fmt.Fprintln(out, "\n+ uncommitted changes (will be applied as unstaged files)") //nolint:errcheck
			}
			fmt.Fprintln(out) //nolint:errcheck

			// Confirmation
			if !yes {
				prompt := fmt.Sprintf("Apply to %s? [y/N] ", targetDir)
				if !sandbox.Confirm(prompt, os.Stdin, cmd.ErrOrStderr()) {
					return nil
				}
			}

			// Apply commits via format-patch/am
			patchDir, files, err := sandbox.GenerateFormatPatch(name, paths)
			if err != nil {
				return err
			}
			defer os.RemoveAll(patchDir) //nolint:errcheck // best-effort cleanup

			if len(files) > 0 {
				if err := sandbox.ApplyFormatPatch(patchDir, files, targetDir); err != nil {
					return err
				}
				fmt.Fprintf(out, "%d commit(s) applied to %s\n", len(files), targetDir) //nolint:errcheck
			}

			// Advance baseline past applied commits (skip for path-filtered applies)
			if len(paths) == 0 {
				if err := sandbox.AdvanceBaseline(name); err != nil {
					return fmt.Errorf("advance baseline: %w", err)
				}
			}

			// Apply WIP changes
			if hasWIP {
				wipPatch, _, wipErr := sandbox.GenerateWIPDiff(name, paths)
				if wipErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to generate WIP diff: %v\n", wipErr) //nolint:errcheck
					return nil
				}
				if len(wipPatch) > 0 {
					if err := sandbox.ApplyPatch(wipPatch, targetDir, isGit); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), //nolint:errcheck // best-effort warning
							"Warning: failed to apply WIP changes: %v\n"+
								"Commits were applied successfully. WIP changes need manual application.\n", err)
						return nil
					}
					fmt.Fprintln(out, "Uncommitted changes applied (unstaged)") //nolint:errcheck
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	cmd.Flags().Bool("squash", false, "Flatten all changes into a single unstaged patch")
	cmd.Flags().String("patches", "", "Export .patch files to directory instead of applying")
	cmd.Flags().Bool("no-wip", false, "Skip uncommitted changes, only apply commits")
	cmd.Flags().Bool("force", false, "Proceed even if host repo has uncommitted changes")

	return cmd
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
func applySelectedCommits(cmd *cobra.Command, name string, refs []string, meta *sandbox.Meta, yes, force bool) error {
	targetDir := meta.Workdir.HostPath
	if !sandbox.IsGitRepo(targetDir) {
		return fmt.Errorf("selective apply requires a git target directory — %s is not a git repository", targetDir)
	}

	// Resolve refs to full SHAs
	resolved, err := sandbox.ResolveRefs(name, refs)
	if err != nil {
		return err
	}

	if len(resolved) == 0 {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No commits matched")
		return err
	}

	// Pre-flight: dirty repo check
	warning, checkErr := sandbox.CheckDirtyRepo(targetDir)
	if checkErr != nil {
		return checkErr
	}
	if warning != "" && !force {
		return fmt.Errorf("target repo has uncommitted changes (%s)\n"+
			"commit or stash them first, or use --force to proceed anyway", warning)
	}

	// Show summary
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Commits to apply (%d):\n", len(resolved)) //nolint:errcheck
	for _, c := range resolved {
		fmt.Fprintf(out, "  %.12s %s\n", c.SHA, c.Subject) //nolint:errcheck
	}
	fmt.Fprintln(out) //nolint:errcheck

	// Confirmation
	if !yes {
		prompt := fmt.Sprintf("Apply to %s? [y/N] ", targetDir)
		if !sandbox.Confirm(prompt, os.Stdin, cmd.ErrOrStderr()) {
			return nil
		}
	}

	// Generate patches for selected commits only
	shas := make([]string, len(resolved))
	for i, c := range resolved {
		shas[i] = c.SHA
	}

	patchDir, files, err := sandbox.GenerateFormatPatchForRefs(name, shas)
	if err != nil {
		return err
	}
	defer os.RemoveAll(patchDir) //nolint:errcheck

	if err := sandbox.ApplyFormatPatch(patchDir, files, targetDir); err != nil {
		return err
	}
	fmt.Fprintf(out, "%d commit(s) applied to %s\n", len(files), targetDir) //nolint:errcheck

	// Advance baseline using contiguous prefix logic
	allCommits, err := sandbox.ListCommitsBeyondBaseline(name)
	if err != nil {
		return fmt.Errorf("advance baseline: %w", err)
	}

	appliedSet := make(map[string]bool, len(resolved))
	for _, c := range resolved {
		appliedSet[c.SHA] = true
	}

	prefixEnd := sandbox.ContiguousPrefixEnd(allCommits, appliedSet)
	if prefixEnd >= 0 {
		if err := sandbox.AdvanceBaselineTo(name, allCommits[prefixEnd].SHA); err != nil {
			return fmt.Errorf("advance baseline: %w", err)
		}
	}

	return nil
}

// applySquash implements the legacy squashed-patch behavior.
func applySquash(cmd *cobra.Command, name string, paths []string, meta *sandbox.Meta, yes bool) error {
	patch, stat, err := sandbox.GeneratePatch(name, paths)
	if err != nil {
		return err
	}
	if len(patch) == 0 {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
		return err
	}

	fmt.Fprintln(cmd.OutOrStdout(), stat) //nolint:errcheck

	targetDir := meta.Workdir.HostPath
	isGit := sandbox.IsGitRepo(targetDir)

	if err := sandbox.CheckPatch(patch, targetDir, isGit); err != nil {
		return err
	}

	if !yes {
		prompt := fmt.Sprintf("Apply these changes to %s? [y/N] ", targetDir)
		if !sandbox.Confirm(prompt, os.Stdin, cmd.ErrOrStderr()) {
			return nil
		}
	}

	if err := sandbox.ApplyPatch(patch, targetDir, isGit); err != nil {
		return err
	}

	// Advance baseline past applied changes (skip for path-filtered applies)
	if len(paths) == 0 {
		if err := sandbox.AdvanceBaseline(name); err != nil {
			return fmt.Errorf("advance baseline: %w", err)
		}
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Changes applied to %s\n", targetDir)
	return err
}

// exportPatches writes .patch files and optional wip.diff to the given directory.
func exportPatches(cmd *cobra.Command, name string, paths []string, commits []sandbox.CommitInfo, hasWIP bool, dir string) error {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create patches directory: %w", err)
	}

	out := cmd.OutOrStdout()

	if len(commits) > 0 {
		patchDir, files, err := sandbox.GenerateFormatPatch(name, paths)
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
			if err := os.WriteFile(dst, data, 0600); err != nil {
				return fmt.Errorf("write patch %s: %w", f, err)
			}
			fmt.Fprintf(out, "  %s\n", dst) //nolint:errcheck
		}
	}

	if hasWIP {
		wipPatch, _, err := sandbox.GenerateWIPDiff(name, paths)
		if err != nil {
			return err
		}
		if len(wipPatch) > 0 {
			dst := filepath.Join(dir, "wip.diff")
			if err := os.WriteFile(dst, wipPatch, 0600); err != nil {
				return fmt.Errorf("write wip.diff: %w", err)
			}
			fmt.Fprintf(out, "  %s\n", dst) //nolint:errcheck
		}
	}

	fmt.Fprintln(out)                                                       //nolint:errcheck
	fmt.Fprintln(out, "To apply commits:  git am --3way <patches>/*.patch") //nolint:errcheck
	fmt.Fprintln(out, "To apply WIP:      git apply wip.diff")              //nolint:errcheck

	return nil
}
