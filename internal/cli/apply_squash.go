// ABOUTME: --squash apply workflow — flattens all sandbox changes into one
// ABOUTME: unstaged patch on the host. Also the fallback for non-git targets
// ABOUTME: and the multi-:copy-dir aggregate apply path.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/spf13/cobra"
)

// applySquash implements the squashed-patch apply mode.
func applySquash(cmd *cobra.Command, name string, paths []string, meta *store.Meta, yes, dryRun, includeWIP bool) error {
	// Check for aux :copy dirs
	if len(meta.Directories) > 0 {
		backend := cliutil.ResolveBackendForSandbox(name)
		return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
			return applySquashMulti(cmd, ctx, c, name, paths, meta, yes, dryRun, includeWIP)
		})
	}

	var patchBytes []byte
	var stat string
	backend := cliutil.ResolveBackendForSandbox(name)
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var genErr error
		patchBytes, stat, genErr = c.GeneratePatch(ctx, name, paths, includeWIP)
		return genErr
	})
	if err != nil {
		return err
	}
	// Surface uncommitted changes the user might want to bring along.
	if !includeWIP {
		warnSquashSkippedWIP(cmd, name, backend)
	}
	if len(patchBytes) == 0 {
		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
				Target: meta.Workdir.HostPath,
				Method: "squash",
			})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
		return err
	}

	targetDir := meta.Workdir.HostPath
	if !cliutil.JSONEnabled(cmd) {
		fmt.Fprintln(cmd.OutOrStdout(), stat) //nolint:errcheck
	}

	if dryRun {
		if !cliutil.JSONEnabled(cmd) {
			fmt.Fprintln(cmd.OutOrStdout(), "(dry run)") //nolint:errcheck
		}
		return nil
	}

	return applySquashPatch(cmd, name, paths, targetDir, patchBytes, yes, backend)
}

// reportSquashMultiNoChanges emits the "nothing to do" output for the
// multi-:copy squash path in either JSON or human mode.
func reportSquashMultiNoChanges(cmd *cobra.Command) error {
	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{Target: "multi", Method: "squash"})
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
	return err
}

// warnSquashSkippedWIP prints the --include-wip hint when squash is excluding
// uncommitted work. Best-effort: a failed WIP check is silently swallowed
// because squash can still succeed on the committed delta.
func warnSquashSkippedWIP(cmd *cobra.Command, name, backend string) {
	if cliutil.JSONEnabled(cmd) {
		return
	}
	var hasWIP bool
	_ = cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var wipErr error
		hasWIP, wipErr = c.HasUncommittedChanges(ctx, name)
		return wipErr
	})
	if hasWIP {
		fmt.Fprintln(cmd.OutOrStdout(), "Note: sandbox has uncommitted changes (excluded from squash); re-run with --include-wip to fold them in.") //nolint:errcheck
	}
}

// applySquashPatch applies a squash patch after confirmation.
func applySquashPatch(cmd *cobra.Command, name string, paths []string, targetDir string, patchBytes []byte, yes bool, backend string) error {
	isGit := workspace.IsGitRepo(targetDir)

	if err := workspace.CheckPatch(patchBytes, targetDir, isGit); err != nil {
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

	if err := workspace.ApplyPatch(patchBytes, targetDir, isGit); err != nil {
		return err
	}

	// Advance baseline past applied changes (skip for path-filtered applies)
	if len(paths) == 0 {
		err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
			return c.AdvanceBaseline(ctx, name)
		})
		if err != nil {
			return fmt.Errorf("advance baseline: %w", err)
		}
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
			Target:     targetDir,
			WIPApplied: true,
			Method:     "squash",
		})
	}

	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Changes applied to %s\n", targetDir)
	return err
}

// applySquashMulti applies squashed patches for multiple :copy directories.
func applySquashMulti(cmd *cobra.Command, ctx context.Context, c *yoloai.Client, name string, paths []string, _ *store.Meta, yes, dryRun, includeWIP bool) error {
	patches, err := c.GenerateMultiPatch(ctx, name, paths, includeWIP)
	if err != nil {
		return err
	}
	if !includeWIP {
		warnSquashSkippedWIP(cmd, name, cliutil.ResolveBackendForSandbox(name))
	}
	if len(patches) == 0 {
		return reportSquashMultiNoChanges(cmd)
	}

	isJSON := cliutil.JSONEnabled(cmd)
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

	if err := applyMultiPatches(cmd, patches, isJSON); err != nil {
		return err
	}

	// Advance baseline for workdir
	if len(paths) == 0 {
		if err := c.AdvanceBaseline(ctx, name); err != nil {
			return fmt.Errorf("advance baseline: %w", err)
		}
	}

	if isJSON {
		return cliutil.WriteJSON(out, applyResult{
			Target:     "multi",
			WIPApplied: true,
			Method:     "squash",
		})
	}

	return nil
}

// applyMultiPatches checks and applies a slice of PatchSet values to their host paths.
func applyMultiPatches(cmd *cobra.Command, patches []patch.PatchSet, isJSON bool) error {
	out := cmd.OutOrStdout()
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
	return nil
}
