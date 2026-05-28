// ABOUTME: --squash apply workflow — flattens all sandbox workdir changes
// ABOUTME: into one unstaged patch on the host. Also the fallback for non-git
// ABOUTME: targets.
package workflow

import (
	"context"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/runtime"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/spf13/cobra"
)

// applySquash implements the squashed-patch apply mode. The library
// (Workdir().Apply) owns generate/validate/apply/advance-baseline; this
// function owns the CLI preview + confirmation + output. It previews via
// DryRun (so the stat is exact, matching what the real apply lands), then —
// after confirmation — applies for real.
func applySquash(cmd *cobra.Command, name string, paths []string, meta *store.Meta, yes, dryRun, includeWIP bool) error {
	backend := cliutil.ResolveBackendForSandbox(name)

	var preview *yoloai.ApplyResult
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var e error
		preview, e = c.Sandbox(name).Workdir().Apply(ctx, yoloai.ApplyOptions{
			IncludeWIP: includeWIP, Paths: paths, DryRun: true,
		})
		return e
	})
	if err != nil {
		return err
	}

	// Surface uncommitted changes the user might want to bring along.
	if !includeWIP {
		warnSquashSkippedWIP(cmd, name, backend)
	}

	if preview == nil {
		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
				Target: meta.Workdir.HostPath,
				Method: "squash",
			})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No changes to apply")
		return err
	}

	targetDir := preview.Dir
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
		prompt := fmt.Sprintf("Apply these changes to %s? [y/N] ", targetDir)
		confirmed, confirmErr := sandbox.Confirm(cmd.Context(), prompt, os.Stdin, cmd.ErrOrStderr())
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			return nil
		}
	}

	err = cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		_, e := c.Sandbox(name).Workdir().Apply(ctx, yoloai.ApplyOptions{
			IncludeWIP: includeWIP, Paths: paths, DryRun: false,
		})
		return e
	})
	if err != nil {
		return err
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
			Target:     targetDir,
			WIPApplied: true,
			Method:     "squash",
		})
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Changes applied to %s\n", targetDir)
	return err
}

// warnSquashSkippedWIP prints the --include-wip hint when squash is excluding
// uncommitted work. Best-effort: a failed WIP check is silently swallowed
// because squash can still succeed on the committed delta.
func warnSquashSkippedWIP(cmd *cobra.Command, name string, backend runtime.BackendName) {
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
