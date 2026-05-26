// ABOUTME: --patches workflow — export .patch files (and an optional wip.diff)
// ABOUTME: to a directory instead of applying. Lets the user inspect or re-apply
// ABOUTME: changes manually via git am.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox/patch"
	"github.com/spf13/cobra"
)

// exportPatches writes .patch files and optional wip.diff to the given directory.
// wip.diff is only written when includeWIP is true; without it the user gets a
// hint that uncommitted changes exist and how to bring them in.
func exportPatches(cmd *cobra.Command, name string, paths []string, commits []patch.CommitInfo, hasWIP, includeWIP bool, dir string) error {
	if err := fileutil.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create patches directory: %w", err)
	}

	isJSON := jsonEnabled(cmd)
	out := cmd.OutOrStdout()

	if len(commits) > 0 {
		if err := exportCommitPatches(cmd, name, paths, dir, isJSON, out); err != nil {
			return err
		}
	}

	wipExported := false
	if hasWIP && includeWIP {
		if err := exportWIPDiff(cmd, name, paths, dir, isJSON, out); err != nil {
			return err
		}
		wipExported = true
	}

	if isJSON {
		return writeJSON(out, applyResult{
			Target:         dir,
			CommitsApplied: len(commits),
			WIPApplied:     wipExported,
			Method:         "patches-export",
		})
	}

	fmt.Fprintln(out)                                                       //nolint:errcheck
	fmt.Fprintln(out, "To apply commits:  git am --3way <patches>/*.patch") //nolint:errcheck
	if wipExported {
		fmt.Fprintln(out, "To apply WIP:      git apply wip.diff") //nolint:errcheck
	} else if hasWIP {
		fmt.Fprintln(out, "Note: sandbox has uncommitted changes (not exported); re-run with --include-wip to write wip.diff.") //nolint:errcheck
	}

	return nil
}

// exportCommitPatches generates format-patch files from sandbox commits and copies them to dir.
func exportCommitPatches(cmd *cobra.Command, name string, paths []string, dir string, isJSON bool, out io.Writer) error {
	layout := cliLayout()
	backend := resolveBackendForSandbox(name)
	var patchDir string
	var files []string
	err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		var genErr error
		patchDir, files, genErr = patch.GenerateFormatPatch(ctx, layout, rt, name, paths)
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
	return nil
}

// exportWIPDiff generates a wip.diff from uncommitted changes and writes it to dir.
func exportWIPDiff(cmd *cobra.Command, name string, paths []string, dir string, isJSON bool, out io.Writer) error {
	layout := cliLayout()
	backend := resolveBackendForSandbox(name)
	var wipPatch []byte
	err := withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		var genErr error
		wipPatch, _, genErr = patch.GenerateWIPDiff(ctx, layout, rt, name, paths)
		return genErr
	})
	if err != nil {
		return err
	}
	if len(wipPatch) == 0 {
		return nil
	}
	dst := filepath.Join(dir, "wip.diff")
	if err := fileutil.WriteFile(dst, wipPatch, 0600); err != nil {
		return fmt.Errorf("write wip.diff: %w", err)
	}
	if !isJSON {
		fmt.Fprintf(out, "  %s\n", dst) //nolint:errcheck
	}
	return nil
}
