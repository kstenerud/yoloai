// ABOUTME: Export writes the sandbox's changes as patch files to a directory
// ABOUTME: instead of applying them (the `apply --patches` flow). Copy-mode emits
// ABOUTME: format-patch files (+ uncommitted.diff); overlay emits upper-layer diffs.

package patch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// ExportOptions configures Export.
type ExportOptions struct {
	// Dir is the destination directory for the patch files (created if absent).
	Dir string
	// Refs selects a subset of commits/ranges to export (copy mode only). Empty
	// exports all beyond-baseline commits.
	Refs []string
	// Paths narrows the export to specific files (relative to the workdir).
	Paths []string
	// IncludeUncommitted additionally writes the agent's uncommitted edits as
	// uncommitted.diff (copy mode only; overlay has no commit/uncommitted split).
	IncludeUncommitted bool
}

// ExportResult reports what Export wrote.
type ExportResult struct {
	// Dir is the destination directory.
	Dir string
	// Files are the patch/diff files written (absolute paths).
	Files []string
	// UncommittedExported is true when uncommitted.diff was written.
	UncommittedExported bool
}

// Export writes the sandbox's changes as patch files under opts.Dir without
// applying them. It resolves the workdir's mount mode internally: copy-mode
// writes git format-patch files (the whole beyond-baseline range, or the
// opts.Refs subset) plus an optional uncommitted.diff; overlay-mode writes the
// upper-layer diff(s) captured by running git inside the container (which must
// be running). Never advances the baseline.
func Export(ctx context.Context, layout config.Layout, rt runtime.Runtime, name string, opts ExportOptions) (*ExportResult, error) {
	meta, err := store.LoadEnvironment(layout.SandboxDir(name))
	if err != nil {
		return nil, err
	}
	if meta.Workdir.Mode == store.DirModeRW {
		return nil, yoerrors.NewUsageError("export is not available for :rw directories — changes are already live")
	}
	if err := fileutil.MkdirAll(opts.Dir, 0750); err != nil {
		return nil, fmt.Errorf("create export directory: %w", err)
	}

	if meta.Workdir.Mode == store.DirModeOverlay {
		return exportOverlay(ctx, layout, rt, name, opts)
	}
	return exportCopy(ctx, layout, rt, name, opts)
}

// exportCopy writes format-patch files (+ optional uncommitted.diff) for a
// copy-mode sandbox.
func exportCopy(ctx context.Context, layout config.Layout, rt runtime.Runtime, name string, opts ExportOptions) (*ExportResult, error) {
	patchDir, files, err := generateExportPatch(ctx, layout, rt, name, opts)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(patchDir) //nolint:errcheck // best-effort cleanup

	result := &ExportResult{Dir: opts.Dir}
	for _, f := range files {
		data, readErr := os.ReadFile(filepath.Join(patchDir, f)) //nolint:gosec // G304: temp patch dir we created
		if readErr != nil {
			return nil, fmt.Errorf("read patch %s: %w", f, readErr)
		}
		dst := filepath.Join(opts.Dir, f)
		if writeErr := fileutil.WriteFile(dst, data, 0600); writeErr != nil {
			return nil, fmt.Errorf("write patch %s: %w", f, writeErr)
		}
		result.Files = append(result.Files, dst)
	}

	if opts.IncludeUncommitted {
		uncommitted, _, diffErr := GenerateUncommittedDiff(ctx, layout, rt, name, opts.Paths)
		if diffErr != nil {
			return nil, diffErr
		}
		if len(uncommitted) > 0 {
			dst := filepath.Join(opts.Dir, "uncommitted.diff")
			if writeErr := fileutil.WriteFile(dst, uncommitted, 0600); writeErr != nil {
				return nil, fmt.Errorf("write uncommitted.diff: %w", writeErr)
			}
			result.Files = append(result.Files, dst)
			result.UncommittedExported = true
		}
	}

	return result, nil
}

// generateExportPatch produces the format-patch series for the export: the
// opts.Refs subset when given, otherwise the whole beyond-baseline range.
func generateExportPatch(ctx context.Context, layout config.Layout, rt runtime.Runtime, name string, opts ExportOptions) (patchDir string, files []string, err error) {
	if len(opts.Refs) == 0 {
		return GenerateFormatPatch(ctx, layout, rt, name, opts.Paths)
	}
	commits, err := ResolveRefs(ctx, layout, rt, name, opts.Refs)
	if err != nil {
		return "", nil, err
	}
	shas := make([]string, len(commits))
	for i, c := range commits {
		shas[i] = c.SHA
	}
	return GenerateFormatPatchForRefs(ctx, layout, rt, name, shas, opts.Paths)
}

// exportOverlay writes the upper-layer diff(s) for an overlay-mode sandbox.
func exportOverlay(ctx context.Context, layout config.Layout, rt runtime.Runtime, name string, opts ExportOptions) (*ExportResult, error) {
	if len(opts.Refs) > 0 {
		return nil, yoerrors.NewUsageError("cannot export specific commits from an :overlay sandbox — overlay changes have no commit history")
	}
	patches, err := GenerateOverlayPatch(ctx, layout, rt, name, opts.Paths)
	if err != nil {
		return nil, err
	}
	result := &ExportResult{Dir: opts.Dir}
	for i, ps := range patches {
		dst := filepath.Join(opts.Dir, fmt.Sprintf("overlay-%d.diff", i+1))
		if writeErr := fileutil.WriteFile(dst, ps.Patch, 0600); writeErr != nil {
			return nil, fmt.Errorf("write patch: %w", writeErr)
		}
		result.Files = append(result.Files, dst)
	}
	return result, nil
}
