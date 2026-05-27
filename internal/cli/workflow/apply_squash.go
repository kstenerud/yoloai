// ABOUTME: --squash apply workflow — flattens all sandbox workdir changes
// ABOUTME: into one unstaged patch on the host. Also the fallback for non-git
// ABOUTME: targets.
package workflow

import (
	"context"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/spf13/cobra"
)

// applySquash implements the squashed-patch apply mode. The Q-U
// resolution (2026-05-25) collapses the diff/apply surface to the
// workdir only; the previous multi-:copy branch is gone.
func applySquash(cmd *cobra.Command, name string, paths []string, meta *store.Meta, yes, dryRun, includeWIP bool) error {
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
