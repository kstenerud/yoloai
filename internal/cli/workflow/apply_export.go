// ABOUTME: --patches workflow — export .patch files (and an optional
// ABOUTME: uncommitted.diff) to a directory instead of applying. Routes through
// ABOUTME: Workdir().Export; this file owns the CLI confirmation-free reporting.
package workflow

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/spf13/cobra"
)

// runExport writes the sandbox's changes as patch files to dir instead of
// applying them. Routes through Workdir().Export; the CLI only prints the result.
func runExport(cmd *cobra.Command, name, hostPath string, _ yoloai.DirInfo, refs, paths []string, dir string, includeUncommitted bool) error {
	var result *yoloai.ExportResult
	var hasUncommitted bool
	err := cliutil.WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		wd, wdErr := trackedDirHandle(sb, hostPath)
		if wdErr != nil {
			return wdErr
		}
		if !includeUncommitted {
			// Best-effort: probe so we can hint that uncommitted edits exist but
			// weren't exported.
			hasUncommitted, _ = wd.HasUncommittedChanges(ctx)
		}
		var exportErr error
		result, exportErr = wd.Export(ctx, yoloai.WorkdirExportOptions{
			Dir:                dir,
			Refs:               refs,
			Paths:              paths,
			IncludeUncommitted: includeUncommitted,
		})
		return exportErr
	})
	if err != nil {
		return err
	}

	return reportExport(cmd, result, hasUncommitted)
}

// reportExport prints the exported files and how to apply them (or emits JSON).
func reportExport(cmd *cobra.Command, result *yoloai.ExportResult, hasUncommitted bool) error {
	patchCount := 0
	for _, f := range result.Files {
		if strings.HasSuffix(f, ".patch") {
			patchCount++
		}
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), applyResult{
			Target:             result.Dir,
			CommitsApplied:     patchCount,
			UncommittedApplied: result.UncommittedExported,
			Method:             "patches-export",
		})
	}

	out := cmd.OutOrStdout()
	if len(result.Files) == 0 {
		fmt.Fprintln(out, "No changes to export") //nolint:errcheck
		return nil
	}
	for _, f := range result.Files {
		fmt.Fprintf(out, "  %s\n", f) //nolint:errcheck
	}
	printExportInstructions(out, result, patchCount, hasUncommitted)
	return nil
}

// printExportInstructions tells the user how to apply the exported files.
func printExportInstructions(out io.Writer, result *yoloai.ExportResult, patchCount int, hasUncommitted bool) {
	fmt.Fprintln(out) //nolint:errcheck
	if patchCount > 0 {
		fmt.Fprintln(out, "To apply commits:  git am --3way <patches>/*.patch") //nolint:errcheck
	}
	switch {
	case result.UncommittedExported:
		fmt.Fprintln(out, "To apply uncommitted:  git apply <patches>/uncommitted.diff") //nolint:errcheck
	case hasUncommitted:
		fmt.Fprintln(out, "Note: sandbox has uncommitted changes (not exported); re-run with --include-uncommitted to write uncommitted.diff.") //nolint:errcheck
	}
}
