// ABOUTME: Overlay apply workflow — for sandboxes whose workdir uses overlayfs.
// ABOUTME: Thin CLI wrapper: enforces the running-container precondition, then
// ABOUTME: routes through Workdir().Apply(ApplyModeNoCommit) (the library owns
// ABOUTME: capture/apply/baseline-advance via copyflow.ApplyOverlay).
package workflow

import (
	"context"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// applyOverlay handles apply for :overlay sandboxes. Overlay changes have no
// commit history, so this is always a net-diff apply (ApplyModeNoCommit);
// --include-uncommitted has no effect. The container must be running (the diff
// is captured by running git inside it).
func applyOverlay(cmd *cobra.Command, name, hostPath, targetDir string, refs, paths []string, yes, dryRun bool) error {
	if len(refs) > 0 {
		return yoerrors.NewPlatformError("selective ref apply is not supported for :overlay sandboxes")
	}

	preview, err := overlayApplyViaClient(cmd, name, hostPath, paths, true)
	if err != nil {
		return err
	}
	if preview == nil {
		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{Target: targetDir, Method: "overlay"})
		}
		_, e := fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
		return e
	}

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
		confirmed, confirmErr := cliutil.Confirm(cmd.Context(), "Apply these changes? [y/N] ", os.Stdin, cmd.ErrOrStderr())
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			return nil
		}
	}

	result, err := overlayApplyViaClient(cmd, name, hostPath, paths, false)
	if result == nil {
		return err
	}
	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
			Target:             result.Dir,
			UncommittedApplied: result.UncommittedApplied,
			Method:             "overlay",
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Changes applied to %s\n", result.Dir) //nolint:errcheck
	return err
}

// overlayApplyViaClient runs the overlay apply through the workdir handle after
// enforcing the running-container precondition. dryRun captures the diff stat
// without applying.
func overlayApplyViaClient(cmd *cobra.Command, name, hostPath string, paths []string, dryRun bool) (*yoloai.ApplyResult, error) {
	var result *yoloai.ApplyResult
	err := cliutil.WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		if runErr := requireOverlayRunning(ctx, sb, name); runErr != nil {
			return runErr
		}
		wd, wdErr := trackedDirHandle(sb, hostPath)
		if wdErr != nil {
			return wdErr
		}
		var applyErr error
		result, applyErr = wd.Apply(ctx, yoloai.WorkdirApplyOptions{
			Mode:   yoloai.ApplyModeNoCommit,
			Paths:  paths,
			DryRun: dryRun,
		})
		return applyErr
	})
	return result, err
}
