package tart

// ABOUTME: Finds and removes orphaned yoloai-* Tart VMs.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// Prune implements runtime.Runtime.
func (r *Runtime) Prune(ctx context.Context, knownInstances []string, dryRun bool, output io.Writer) (runtime.PruneResult, error) {
	known := make(map[string]bool, len(knownInstances))
	for _, name := range knownInstances {
		known[name] = true
	}

	out, err := r.runTart(ctx, "list", "--quiet")
	if err != nil {
		return runtime.PruneResult{}, fmt.Errorf("list VMs: %w", err)
	}

	var result runtime.PruneResult
	for line := range strings.SplitSeq(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || !strings.HasPrefix(name, "yoloai-") {
			continue
		}
		// The provisioned base template shares the yoloai- prefix and the
		// tart-list VM namespace with sandboxes, but it is not an orphan — it
		// is the reusable image every `new` clones from. Reclaiming it is the
		// job of PruneCache (`--images`), never the orphan sweep.
		if name == provisionedImageName {
			continue
		}
		if known[name] {
			continue
		}

		item := runtime.PruneItem{
			Kind: "vm",
			Name: name,
		}

		if !dryRun {
			// Stop the VM before deleting — tart delete fails on running VMs.
			r.stopVM(ctx, name)
			if _, err := r.runTart(ctx, "delete", name); err != nil {
				if !errors.Is(err, runtime.ErrNotFound) {
					fmt.Fprintf(output, "Warning: failed to delete VM %s: %v\n", name, err) //nolint:errcheck // best-effort output
					continue
				}
				// VM already gone — treat as successful deletion.
			}
		}
		result.Items = append(result.Items, item)
	}

	return result, nil
}

// PruneCache implements runtime.CachePruner for tart. Deletes the provisioned
// yoloai-base VM and every OCI row for the pulled base image (both the tag and
// the digest row — see ownedImageRefs), then drops the build-checksum marker so
// the next sandbox creation re-pulls and re-provisions from scratch.
//
// Tart has no regenerable build cache distinct from the base image, so when
// includeImages is false (plain `prune`) there is nothing to reclaim without
// forcing a re-pull — this is a no-op. With includeImages true (`prune
// --images`) it removes the multi-GB base image: a "host dedicated to yoloai"
// operation. Running sandboxes are unaffected — they are independent clones,
// not references to these images.
//
// Returns bytes reclaimed, measured as the drop in this backend's own
// CacheUsage across the prune (before − after), the same self-attributed delta
// docker/podman use (working-notes D37). tart's `list` Size is whole-GB, so the
// figure is coarse but reconciles with what `system disk` reports.
func (r *Runtime) PruneCache(ctx context.Context, includeImages, dryRun bool, output io.Writer) (int64, error) {
	if !includeImages {
		return 0, nil
	}

	before := r.ownedImageBytes(ctx)
	refs := r.ownedImageRefs(ctx)

	if dryRun {
		for _, name := range refs {
			fmt.Fprintf(output, "tart: would remove cached image %s\n", name) //nolint:errcheck // best-effort output
		}
		if before < 0 {
			before = 0
		}
		return before, nil
	}

	for _, name := range refs {
		// delete fails on a running VM; stop first (no-op for OCI images).
		r.stopVM(ctx, name)
		if _, err := r.runTart(ctx, "delete", name); err != nil && !errors.Is(err, runtime.ErrNotFound) {
			fmt.Fprintf(output, "tart: failed to remove cached image %s: %v\n", name, err) //nolint:errcheck // best-effort output
			continue
		}
		fmt.Fprintf(output, "tart: removed cached image %s\n", name) //nolint:errcheck // best-effort output
	}

	// Drop the provision checksum so needsBuild re-provisions cleanly even
	// if a future base happens to hash identically.
	_ = os.Remove(r.tartBaseChecksumPath()) //nolint:errcheck // best-effort

	reclaimed := int64(0)
	if after := r.ownedImageBytes(ctx); before >= 0 && after >= 0 && before > after {
		reclaimed = before - after
	}
	return reclaimed, nil
}
