// ABOUTME: Overlay apply workflow — for sandboxes whose workdir uses overlayfs.
// ABOUTME: Captures the upperdir diff via 'git diff' inside the container, then
// ABOUTME: replays it on the host and advances the overlay baseline.
package workflow

import (
	"context"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
	"github.com/spf13/cobra"
)

// applyOverlay handles apply for sandboxes with overlay directories.
// --include-uncommitted has no effect here: overlay sandboxes have no commit
// history inside the agent's workspace, so all upper-layer changes are applied
// as a single patch regardless.
func applyOverlay(cmd *cobra.Command, name string, meta *store.Meta, refs, paths []string, yes, dryRun bool) error {
	if len(refs) > 0 {
		return sandbox.NewPlatformError("selective ref apply is not supported for :overlay sandboxes")
	}

	backend := cliutil.ResolveBackendForSandbox(name)
	return cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		if err := requireOverlayRunning(ctx, c, name); err != nil {
			return err
		}

		patches, err := c.OverlayPatch(ctx, name, paths)
		if err != nil {
			return err
		}

		if len(patches) == 0 {
			if cliutil.JSONEnabled(cmd) {
				return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
					Target: meta.Workdir.HostPath,
					Method: "overlay",
				})
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
			return err
		}

		return applyOverlayPatches(cmd, ctx, c, name, meta, patches, yes, dryRun)
	})
}

// applyOverlayPatches applies overlay patches to the host and advances baselines.
func applyOverlayPatches(cmd *cobra.Command, ctx context.Context, c *yoloai.Client, name string, meta *store.Meta, patches []patch.PatchSet, yes, dryRun bool) error {
	isJSON := cliutil.JSONEnabled(cmd)
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
		if err := c.UpdateOverlayBaseline(ctx, name, ps.HostPath); err != nil {
			return fmt.Errorf("advance overlay baseline: %w", err)
		}
	}

	if isJSON {
		return cliutil.WriteJSON(out, applyResult{
			Target:             meta.Workdir.HostPath,
			UncommittedApplied: true,
			Method:             "overlay",
		})
	}

	return nil
}
