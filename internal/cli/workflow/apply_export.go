// ABOUTME: --patches workflow — export .patch files (and an optional uncommitted.diff)
// ABOUTME: to a directory instead of applying. Lets the user inspect or re-apply
// ABOUTME: changes manually via git am.
package workflow

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/spf13/cobra"
)

// exportPatches writes .patch files and optional uncommitted.diff to the given
// directory. uncommitted.diff is only written when includeUncommitted is true;
// without it the user gets a hint that uncommitted changes exist and how to bring them in.
func exportPatches(cmd *cobra.Command, name string, paths []string, commits []patch.CommitInfo, hasUncommitted, includeUncommitted bool, dir string) error {
	if err := fileutil.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create patches directory: %w", err)
	}

	isJSON := cliutil.JSONEnabled(cmd)
	out := cmd.OutOrStdout()

	if len(commits) > 0 {
		if err := exportCommitPatches(cmd, name, paths, dir, isJSON, out); err != nil {
			return err
		}
	}

	uncommittedExported := false
	if hasUncommitted && includeUncommitted {
		if err := exportUncommittedDiff(cmd, name, paths, dir, isJSON, out); err != nil {
			return err
		}
		uncommittedExported = true
	}

	if isJSON {
		return cliutil.WriteJSON(out, applyResult{
			Target:             dir,
			CommitsApplied:     len(commits),
			UncommittedApplied: uncommittedExported,
			Method:             "patches-export",
		})
	}

	fmt.Fprintln(out)                                                       //nolint:errcheck
	fmt.Fprintln(out, "To apply commits:  git am --3way <patches>/*.patch") //nolint:errcheck
	if uncommittedExported {
		fmt.Fprintln(out, "To apply uncommitted:  git apply uncommitted.diff") //nolint:errcheck
	} else if hasUncommitted {
		fmt.Fprintln(out, "Note: sandbox has uncommitted changes (not exported); re-run with --include-uncommitted to write uncommitted.diff.") //nolint:errcheck
	}

	return nil
}

// exportCommitPatches generates format-patch files from sandbox commits and copies them to dir.
func exportCommitPatches(cmd *cobra.Command, name string, paths []string, dir string, isJSON bool, out io.Writer) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	var patchDir string
	var files []string
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var genErr error
		patchDir, files, genErr = c.GenerateFormatPatch(ctx, name, paths)
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

// exportUncommittedDiff generates an uncommitted.diff from uncommitted changes and writes it to dir.
func exportUncommittedDiff(cmd *cobra.Command, name string, paths []string, dir string, isJSON bool, out io.Writer) error {
	backend := cliutil.ResolveBackendForSandbox(name)
	var uncommittedPatch []byte
	err := cliutil.WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		var genErr error
		uncommittedPatch, _, genErr = c.GenerateUncommittedDiff(ctx, name, paths)
		return genErr
	})
	if err != nil {
		return err
	}
	if len(uncommittedPatch) == 0 {
		return nil
	}
	dst := filepath.Join(dir, "uncommitted.diff")
	if err := fileutil.WriteFile(dst, uncommittedPatch, 0600); err != nil {
		return fmt.Errorf("write uncommitted.diff: %w", err)
	}
	if !isJSON {
		fmt.Fprintf(out, "  %s\n", dst) //nolint:errcheck
	}
	return nil
}
