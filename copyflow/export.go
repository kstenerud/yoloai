// ABOUTME: Export writes the sandbox's changes as patch files to a directory
// ABOUTME: instead of applying them (the `apply --patches` flow). Copy-mode emits
// ABOUTME: format-patch files (+ uncommitted.diff); overlay emits upper-layer diffs.

package copyflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
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
	// DirHostPath selects the directory to export; "" selects Dirs[0] (workdir).
	DirHostPath string
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
func Export(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, opts ExportOptions) (*ExportResult, error) {
	meta, err := store.LoadEnvironment(layout.SandboxDir(name))
	if err != nil {
		return nil, err
	}
	dir := meta.Dir(opts.DirHostPath)
	if dir == nil {
		return nil, yoerrors.NewUsageError("directory not found in sandbox")
	}
	if dir.Mode == store.DirModeRW {
		return nil, yoerrors.NewUsageError("export is not available for :rw directories — changes are already live")
	}
	if err := fileutil.MkdirAll(opts.Dir, 0750); err != nil {
		return nil, fmt.Errorf("create export directory: %w", err)
	}

	return exportCopy(ctx, layout, rt, name, opts)
}

// exportCopy writes format-patch files (+ optional uncommitted.diff) for a
// copy-mode sandbox.
func exportCopy(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, opts ExportOptions) (*ExportResult, error) {
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
		uncommitted, _, diffErr := GenerateUncommittedDiff(ctx, layout, rt, name, opts.DirHostPath, opts.Paths)
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
func generateExportPatch(ctx context.Context, layout config.Layout, rt runtime.Backend, name string, opts ExportOptions) (patchDir string, files []string, err error) {
	if len(opts.Refs) == 0 {
		return GenerateFormatPatch(ctx, layout, rt, name, opts.DirHostPath, opts.Paths)
	}
	commits, err := ResolveRefs(ctx, layout, rt, name, opts.DirHostPath, opts.Refs)
	if err != nil {
		return "", nil, err
	}
	shas := make([]string, len(commits))
	for i, c := range commits {
		shas[i] = c.SHA
	}
	return GenerateFormatPatchForRefs(ctx, layout, rt, name, opts.DirHostPath, shas, opts.Paths)
}
