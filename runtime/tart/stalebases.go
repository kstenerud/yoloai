package tart

// ABOUTME: Opt-in removal of superseded Cirrus base images (macos-<codename>-base
// ABOUTME: repos the current resolved base no longer references) for `prune --stale-bases`.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/feedback"
	"github.com/kstenerud/yoloai/runtime"
)

// Compile-time check: tart can reclaim superseded base images.
var _ runtime.StaleBasePruner = (*Runtime)(nil)

// PruneStaleBases removes (or, under dryRun, lists) every Cirrus base image on
// disk that differs from the currently resolved base — the bases left behind
// when the host macOS, and thus the resolved codename, changed. The current
// base and the provisioned yoloai-base VM are never touched. Implements
// runtime.StaleBasePruner.
func (r *Runtime) PruneStaleBases(ctx context.Context, dryRun bool) (runtime.StaleBasePruneResult, error) {
	var result runtime.StaleBasePruneResult
	// When tart.image is pinned to a non-base image (e.g. an -xcode flavor),
	// the override itself becomes the "current" image for stale detection, while
	// the host-matched base is protected and stays on disk. Report a
	// transparency notice so a stale leftover override config can't silently
	// look like "free cleanup" to the user.
	if r.baseImageOverride != "" && !isBaseImageFamily(baseImageRepo(r.baseImageOverride)) {
		currentRepo := baseImageRepo(r.resolveBaseImage(""))
		result.Notices = append(result.Notices, feedback.Notice{
			Event: "prune.base_resolved_from_override",
			Level: feedback.LevelInfo,
			Message: fmt.Sprintf("tart: current base resolved from tart.image override = %s (current repo %s)",
				r.baseImageOverride, currentRepo),
			Fields: map[string]any{"backend": "tart", "override": r.baseImageOverride, "current_repo": currentRepo},
		})
	}

	stale, err := r.staleBaseImages(ctx)
	if err != nil {
		return runtime.StaleBasePruneResult{}, err
	}

	for _, s := range stale {
		item := runtime.PruneItem{
			Kind:           "stale-base",
			Name:           s.Repo,
			Action:         runtime.PruneActionWouldRemove,
			BytesReclaimed: s.Bytes,
		}
		if !dryRun {
			if reason := r.deleteStaleBase(ctx, s); reason != "" {
				// A partial failure leaves the repo reported as not reclaimed
				// rather than silently counted — the reason now travels with
				// the item instead of past the caller on a writer.
				item.Action = runtime.PruneActionFailed
				item.Reason = reason
				item.BytesReclaimed = 0
			} else {
				item.Action = runtime.PruneActionRemoved
			}
		}
		result.BytesReclaimed += item.BytesReclaimed
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// deleteStaleBase removes every OCI row (tag + digest) for one superseded base
// repo. Returns "" when all rows are gone, and otherwise the joined failure
// reasons, so a partial failure leaves the repo reported as not-yet-reclaimed
// rather than silently counted.
func (r *Runtime) deleteStaleBase(ctx context.Context, s staleBaseImage) string {
	var failures []string
	for _, ref := range s.Refs {
		if _, err := r.runTart(ctx, "delete", ref); err != nil && !errors.Is(err, runtime.ErrNotFound) {
			failures = append(failures, fmt.Sprintf("%s: %v", ref, err))
		}
	}
	return strings.Join(failures, "; ")
}
