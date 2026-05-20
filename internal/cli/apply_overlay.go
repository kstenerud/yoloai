// ABOUTME: Overlay apply workflow — for sandboxes whose workdir uses overlayfs.
// ABOUTME: Captures the upperdir diff via 'git diff' inside the container, then
// ABOUTME: replays it on the host and advances the overlay baseline.
package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/sandbox/patch"
	"github.com/kstenerud/yoloai/sandbox/store"
	"github.com/kstenerud/yoloai/workspace"
	"github.com/spf13/cobra"
	"os"
)

// applyOverlay handles apply for sandboxes with overlay directories.
func applyOverlay(cmd *cobra.Command, name string, meta *store.Meta, refs, paths []string, patchesDir string, noWIP, yes, dryRun bool) error {
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

		patches, err := patch.GenerateOverlayPatch(ctx, rt, name, paths)
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
			return applyOverlayExportPatches(cmd, patches, patchesDir)
		}

		return applyOverlayPatches(cmd, ctx, rt, name, meta, patches, yes, dryRun)
	})
}

// applyOverlayExportPatches exports overlay patches to a directory.
func applyOverlayExportPatches(cmd *cobra.Command, patches []patch.PatchSet, patchesDir string) error {
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

// applyOverlayPatches applies overlay patches to the host and advances baselines.
func applyOverlayPatches(cmd *cobra.Command, ctx context.Context, rt runtime.Runtime, name string, meta *store.Meta, patches []patch.PatchSet, yes, dryRun bool) error {
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
		if err := patch.UpdateOverlayBaselineToHEAD(ctx, rt, name, ps.HostPath); err != nil {
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
}
