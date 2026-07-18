// ABOUTME: Apple-backend cache reclaim (build cache + dangling/unused images)
// ABOUTME: for `yoloai system prune`, implementing runtime.CachePruner.
package apple

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/kstenerud/yoloai/runtime"
)

// Compile-time check that Runtime satisfies the optional CachePruner interface.
var _ runtime.CachePruner = (*Runtime)(nil)

// dfUsage is the parsed `container system df --format json` snapshot: enough
// to measure total footprint before/after a prune and to estimate the
// dry-run reclaim from the images category's reported "reclaimable" figure.
type dfUsage struct {
	ImagesBytes       int64
	ContainersBytes   int64
	VolumesBytes      int64
	ImagesReclaimable int64
}

// totalBytes sums the three size categories `container system df` tracks.
// Comparing this before/after a prune is the honest reclaim measurement — see
// parseSystemDF and PruneCache for why the per-category "reclaimable" field
// can't be used for the builder's build cache.
func (d dfUsage) totalBytes() int64 {
	return d.ImagesBytes + d.ContainersBytes + d.VolumesBytes
}

// reclaimDelta returns the drop in total footprint from before to after,
// clamped to 0 so a measurement noise/growth blip never reports a negative
// reclaim.
func reclaimDelta(before, after dfUsage) int64 {
	delta := before.totalBytes() - after.totalBytes()
	if delta < 0 {
		return 0
	}
	return delta
}

// parseSystemDF unmarshals the JSON emitted by `container system df --format
// json` into a dfUsage. The nested structs mirror only the fields yoloai
// needs.
func parseSystemDF(data []byte) (dfUsage, error) {
	var raw struct {
		Containers struct {
			SizeInBytes int64 `json:"sizeInBytes"`
		} `json:"containers"`
		Images struct {
			SizeInBytes int64 `json:"sizeInBytes"`
			Reclaimable int64 `json:"reclaimable"`
		} `json:"images"`
		Volumes struct {
			SizeInBytes int64 `json:"sizeInBytes"`
		} `json:"volumes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return dfUsage{}, fmt.Errorf("parse container system df output: %w", err)
	}
	return dfUsage{
		ImagesBytes:       raw.Images.SizeInBytes,
		ContainersBytes:   raw.Containers.SizeInBytes,
		VolumesBytes:      raw.Volumes.SizeInBytes,
		ImagesReclaimable: raw.Images.Reclaimable,
	}, nil
}

// systemDF runs `container system df --format json` and parses the result.
func (r *Runtime) systemDF(ctx context.Context) (dfUsage, error) {
	out, err := r.runContainer(ctx, "system", "df", "--format", "json")
	if err != nil {
		return dfUsage{}, fmt.Errorf("container system df: %w", err)
	}
	return parseSystemDF([]byte(out))
}

// PruneCache implements runtime.CachePruner. Apple's `container` CLI has no
// dedicated build-cache-prune command — the BuildKit build cache lives inside
// the running builder container and is only freed by deleting the builder
// outright (`container builder delete --force`); Setup recreates it on next
// use via `container builder start`. See backend-idiosyncrasies.md for why
// `system df`'s per-category "reclaimable" figure can't measure this (the
// running builder reports containers.reclaimable=0 even though deleting it
// frees real bytes) — reclaim here is instead measured as the drop in total
// `system df` footprint (images+containers+volumes) across the prune.
func (r *Runtime) PruneCache(ctx context.Context, includeImages, dryRun bool, output io.Writer) (int64, error) {
	if dryRun {
		return r.pruneCacheDryRun(ctx, includeImages, output), nil
	}

	before, _ := r.systemDF(ctx)

	// DF137: unlike the docker backend (whose ContainersPrune/NetworksPrune are
	// now label-scoped), these `container ...` prunes are NOT yoloai-scoped —
	// Apple's container CLI has no label filter — so on a shared macOS host this
	// reaps foreign content. Deferred: needs a Mac to fix and verify. See the
	// open DF137 in findings-unresolved.md.
	//
	// Stopped containers first, mirroring the docker backend's ordering.
	if _, err := r.runContainer(ctx, "prune"); err != nil {
		fmt.Fprintf(output, "apple: container prune failed: %v\n", err) //nolint:errcheck
	}

	// Dangling images — always, both modes (matches the core Prune's docker
	// counterpart, which removes these regardless of includeImages).
	if _, err := r.runContainer(ctx, "image", "prune"); err != nil {
		fmt.Fprintf(output, "apple: image prune failed: %v\n", err) //nolint:errcheck
	}

	// Build cache: only lever is deleting the builder; it is recreated
	// automatically by Setup on next use. May error if no builder exists —
	// swallow and continue.
	if _, err := r.runContainer(ctx, "builder", "delete", "--force"); err != nil {
		fmt.Fprintf(output, "apple: builder delete failed: %v\n", err) //nolint:errcheck
	}

	// Unused (non-dangling) images too, but only when asked — this forces a
	// rebuild of yoloai-base on next sandbox creation.
	if includeImages {
		if _, err := r.runContainer(ctx, "image", "prune", "--all"); err != nil {
			fmt.Fprintf(output, "apple: image prune --all failed: %v\n", err) //nolint:errcheck
		}
	}

	after, _ := r.systemDF(ctx)
	reclaimed := reclaimDelta(before, after)
	fmt.Fprintf(output, "apple: reclaimed %s\n", runtime.FormatBytes(reclaimed)) //nolint:errcheck
	return reclaimed, nil
}

// dryRunReclaimEstimate returns the reclaim a dry-run can promise up front.
// `system df`'s images "reclaimable" figure counts every unused image (tagged
// or dangling), but only --images (which runs `image prune --all`) removes the
// unused *tagged* ones; plain prune removes only dangling images, whose bytes
// `system df` doesn't break out and which usually share layers with the base
// (≈0 freed). The builder's build cache is unmeasurable up front either way
// (see backend-idiosyncrasies.md). So the honest estimate is df.ImagesReclaimable
// under --images and 0 otherwise — the real reclaim is measured after the fact
// by the before/after `system df` delta in PruneCache.
func dryRunReclaimEstimate(df dfUsage, includeImages bool) int64 {
	if includeImages {
		return df.ImagesReclaimable
	}
	return 0
}

// pruneCacheDryRun reports what PruneCache would remove without doing it. The
// `container` CLI has no dry-run prune, so this reads `system df` and reports
// the pre-measurable reclaim (see dryRunReclaimEstimate); the build cache freed
// by deleting the builder is unmeasurable up front, so it's called out in the
// message but not counted in the returned estimate.
func (r *Runtime) pruneCacheDryRun(ctx context.Context, includeImages bool, output io.Writer) int64 {
	df, err := r.systemDF(ctx)
	if err != nil {
		fmt.Fprintf(output, "apple: could not estimate reclaimable cache: %v\n", err) //nolint:errcheck
		return 0
	}

	// This runs as the CLI's internal scan on every prune, not only under
	// --dry-run, so it must not frame itself as a dry-run mode. The BuildKit
	// build cache freed by deleting the builder is unmeasurable up front, so it's
	// always called out even when the measurable estimate is 0.
	estimate := dryRunReclaimEstimate(df, includeImages)
	if includeImages {
		//nolint:errcheck // best-effort progress output
		fmt.Fprintf(output, "apple: would remove build cache + dangling and unused images (~%s); BuildKit build cache also freed\n",
			runtime.FormatBytes(estimate))
	} else {
		//nolint:errcheck // best-effort progress output
		fmt.Fprintf(output, "apple: would remove build cache + dangling images (size measured after prune); BuildKit build cache also freed\n")
	}
	return estimate
}
