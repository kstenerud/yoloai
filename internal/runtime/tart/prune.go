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
		// job of PruneCache (`--cache`), never the orphan sweep.
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
// yoloai-base VM and the pulled base macOS image, then drops the build-checksum
// marker so the next sandbox creation re-pulls and re-provisions from scratch.
//
// More aggressive than Prune: the pulled base image is multi-GB, so this is a
// "host dedicated to yoloai" operation. Running sandboxes are unaffected — they
// are independent clones, not references to these images.
func (r *Runtime) PruneCache(ctx context.Context, dryRun bool, output io.Writer) error {
	// resolveBaseImage honours the tart.image config override; an empty
	// sourceDir is fine because the override (if any) is process-level.
	baseImage := r.resolveBaseImage("")
	// Dedupe in case the override happens to equal the provisioned name.
	targets := []string{provisionedImageName}
	if baseImage != provisionedImageName {
		targets = append(targets, baseImage)
	}

	for _, name := range targets {
		exists, err := r.vmExistsNamed(ctx, name)
		if err != nil {
			fmt.Fprintf(output, "tart: failed to check cached image %s: %v\n", name, err) //nolint:errcheck // best-effort output
			continue
		}
		if !exists {
			continue
		}
		if dryRun {
			fmt.Fprintf(output, "tart: would remove cached image %s\n", name) //nolint:errcheck // best-effort output
			continue
		}
		// delete fails on a running VM; stop first (no-op for OCI images).
		r.stopVM(ctx, name)
		if _, err := r.runTart(ctx, "delete", name); err != nil && !errors.Is(err, runtime.ErrNotFound) {
			fmt.Fprintf(output, "tart: failed to remove cached image %s: %v\n", name, err) //nolint:errcheck // best-effort output
			continue
		}
		fmt.Fprintf(output, "tart: removed cached image %s\n", name) //nolint:errcheck // best-effort output
	}

	if !dryRun {
		// Drop the provision checksum so needsBuild re-provisions cleanly even
		// if a future base happens to hash identically.
		_ = os.Remove(r.tartBaseChecksumPath()) //nolint:errcheck // best-effort
	}
	return nil
}
