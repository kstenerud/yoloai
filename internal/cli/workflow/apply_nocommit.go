// ABOUTME: --no-commit apply workflow — lands the net sandbox workdir changes
// ABOUTME: as one unstaged patch in the host working tree. Also the non-git fallback.

package workflow

import (
	"context"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/spf13/cobra"
)

// applyNoCommit implements the net-diff (--no-commit) apply mode. The library
// (Workdir().Apply) owns generate/validate/apply/advance-baseline; this
// function owns the CLI preview + confirmation + output. It previews via
// DryRun (so the stat is exact, matching what the real apply lands), then —
// after confirmation — applies for real.
func applyNoCommit(cmd *cobra.Command, name, hostPath, targetDir string, paths []string, yes, dryRun, includeUncommitted bool) error {
	backend := cliutil.ResolveBackendForSandbox(name)

	var preview *yoloai.ApplyResult
	err := cliutil.WithTrackedDir(cmd, name, hostPath, func(ctx context.Context, wd *yoloai.Workdir) error {
		var e error
		preview, e = wd.Apply(ctx, yoloai.WorkdirApplyOptions{
			Mode: yoloai.ApplyModeNoCommit, IncludeUncommitted: includeUncommitted, Paths: paths, DryRun: true,
		})
		return e
	})
	if err != nil {
		return err
	}

	// Surface uncommitted changes the user might want to bring along.
	if !includeUncommitted {
		warnNoCommitSkippedUncommitted(cmd, name, hostPath, backend)
	}

	if preview == nil {
		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
				Target: targetDir,
				Method: "no-commit",
			})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
		return err
	}

	applyTarget := preview.Dir
	if !cliutil.JSONEnabled(cmd) {
		fmt.Fprintln(cmd.OutOrStdout(), preview.Stat) //nolint:errcheck
	}

	if dryRun {
		if !cliutil.JSONEnabled(cmd) {
			fmt.Fprintln(cmd.OutOrStdout(), "(dry run)") //nolint:errcheck
		}
		return nil
	}

	if !yes {
		prompt := fmt.Sprintf("Apply these changes to %s? [y/N] ", applyTarget)
		confirmed, confirmErr := cliutil.Confirm(cmd.Context(), prompt, os.Stdin, cmd.ErrOrStderr())
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			return nil
		}
	}

	err = cliutil.WithTrackedDir(cmd, name, hostPath, func(ctx context.Context, wd *yoloai.Workdir) error {
		_, e := wd.Apply(ctx, yoloai.WorkdirApplyOptions{
			Mode: yoloai.ApplyModeNoCommit, IncludeUncommitted: includeUncommitted, Paths: paths, DryRun: false,
		})
		return e
	})
	if err != nil {
		return err
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
			Target:             applyTarget,
			UncommittedApplied: true,
			Method:             "no-commit",
		})
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Changes applied to %s\n", applyTarget)
	return err
}

// warnNoCommitSkippedUncommitted prints the --include-uncommitted hint when
// --no-commit excludes uncommitted work. Best-effort: a failed check is silently
// swallowed because the net-diff apply can still succeed on the committed delta.
func warnNoCommitSkippedUncommitted(cmd *cobra.Command, name, hostPath string, backend yoloai.BackendType) {
	if cliutil.JSONEnabled(cmd) {
		return
	}
	var hasUncommitted bool
	_ = cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, sbErr := c.Sandbox(name)
		if sbErr != nil {
			return sbErr
		}
		wd, wdErr := trackedDirHandle(sb, hostPath)
		if wdErr != nil {
			return wdErr
		}
		var uncommittedErr error
		hasUncommitted, uncommittedErr = wd.HasUncommittedChanges(ctx)
		return uncommittedErr
	})
	if hasUncommitted {
		fmt.Fprintln(cmd.OutOrStdout(), "Note: sandbox has uncommitted changes (excluded from --no-commit); re-run with --include-uncommitted to fold them in.") //nolint:errcheck
	}
}
