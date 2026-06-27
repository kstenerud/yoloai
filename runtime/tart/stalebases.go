package tart

// ABOUTME: Opt-in removal of superseded Cirrus base images (macos-<codename>-base
// ABOUTME: repos the current resolved base no longer references) for `prune --stale-bases`.

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/kstenerud/yoloai/runtime"
)

// Compile-time check: tart can reclaim superseded base images.
var _ runtime.StaleBasePruner = (*Runtime)(nil)

// PruneStaleBases removes (or, under dryRun, lists) every Cirrus base image on
// disk that differs from the currently resolved base — the bases left behind
// when the host macOS, and thus the resolved codename, changed. The current
// base and the provisioned yoloai-base VM are never touched. Implements
// runtime.StaleBasePruner.
func (r *Runtime) PruneStaleBases(ctx context.Context, dryRun bool, output io.Writer) ([]string, int64, error) {
	stale, err := r.staleBaseImages(ctx)
	if err != nil {
		return nil, 0, err
	}

	var removed []string
	var reclaimed int64
	for _, s := range stale {
		if dryRun {
			fmt.Fprintf(output, "tart: would remove superseded base image %s (%d bytes)\n", s.Repo, s.Bytes) //nolint:errcheck // best-effort output
			removed = append(removed, s.Repo)
			reclaimed += s.Bytes
			continue
		}
		if r.deleteStaleBase(ctx, s, output) {
			removed = append(removed, s.Repo)
			reclaimed += s.Bytes
			fmt.Fprintf(output, "tart: removed superseded base image %s\n", s.Repo) //nolint:errcheck // best-effort output
		}
	}
	return removed, reclaimed, nil
}

// deleteStaleBase removes every OCI row (tag + digest) for one superseded base
// repo. Returns true only when all rows are gone, so a partial failure leaves
// the repo reported as not-yet-reclaimed rather than silently counted.
func (r *Runtime) deleteStaleBase(ctx context.Context, s staleBaseImage, output io.Writer) bool {
	ok := true
	for _, ref := range s.Refs {
		if _, err := r.runTart(ctx, "delete", ref); err != nil && !errors.Is(err, runtime.ErrNotFound) {
			fmt.Fprintf(output, "tart: failed to remove superseded base image %s: %v\n", ref, err) //nolint:errcheck // best-effort output
			ok = false
		}
	}
	return ok
}
