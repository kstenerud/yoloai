// ABOUTME: ApplyOverlay lands an :overlay sandbox's upper-layer changes onto the
// ABOUTME: host. The diff is captured by running git inside the container (which
// ABOUTME: must be running); there is no commit history, so it's a net-diff apply.

package patch

import (
	"context"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/internal/workspace"
)

// ApplyOverlayOptions configures ApplyOverlay.
type ApplyOverlayOptions struct {
	// Paths narrows the apply to specific files (relative to the workdir).
	Paths []string
	// DryRun captures the upper-layer diff and returns its stat without applying
	// or advancing the overlay baseline.
	DryRun bool
}

// ApplyOverlay lands an :overlay sandbox's upper-layer changes onto the host as
// a net diff (overlay has no commit history). It captures the diff by running
// git inside the container (which must be running), applies it to each modified
// host path, and advances the overlay baseline. Returns (nil, nil) when there
// is nothing to apply.
func ApplyOverlay(ctx context.Context, layout config.Layout, rt runtime.Runtime, name string, opts ApplyOverlayOptions) (*ApplyResult, error) {
	unlock, err := store.AcquireLock(layout, name)
	if err != nil {
		return nil, err
	}
	defer unlock()

	meta, err := store.LoadMeta(layout.SandboxDir(name))
	if err != nil {
		return nil, err
	}
	if meta.Workdir.Mode != store.DirModeOverlay {
		return nil, nil
	}

	patches, err := GenerateOverlayPatch(ctx, layout, rt, name, opts.Paths)
	if err != nil {
		return nil, err
	}
	if len(patches) == 0 {
		return nil, nil
	}

	result := &ApplyResult{Dir: meta.Workdir.HostPath, Stat: overlayStat(patches)}
	if opts.DryRun {
		return result, nil
	}

	for _, ps := range patches {
		if applyErr := workspace.ApplyPatch(ps.Patch, ps.HostPath, workspace.IsGitRepo(ps.HostPath)); applyErr != nil {
			return nil, fmt.Errorf("%s: %w", ps.HostPath, applyErr)
		}
	}
	for _, ps := range patches {
		if baseErr := UpdateOverlayBaselineToHEAD(ctx, layout, rt, name, ps.HostPath); baseErr != nil {
			return nil, fmt.Errorf("advance overlay baseline: %w", baseErr)
		}
	}
	result.UncommittedApplied = true
	return result, nil
}

// overlayStat renders the per-directory stat summary for a set of overlay
// patches (one section per modified host path).
func overlayStat(patches []PatchSet) string {
	var b strings.Builder
	for i, ps := range patches {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "=== %s (%s) ===\n%s", ps.HostPath, ps.Mode, ps.Stat)
	}
	return b.String()
}
