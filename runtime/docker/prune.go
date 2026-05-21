package docker

// ABOUTME: Finds and removes orphaned yoloai-* Docker containers and dangling images.

import (
	"context"
	"fmt"
	"io"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"

	"github.com/kstenerud/yoloai/runtime"
)

// Prune implements runtime.Runtime.
func (r *Runtime) Prune(ctx context.Context, knownInstances []string, dryRun bool, output io.Writer) (runtime.PruneResult, error) {
	known := make(map[string]bool, len(knownInstances))
	for _, name := range knownInstances {
		known[name] = true
	}

	var result runtime.PruneResult

	containerItems, err := r.pruneContainers(ctx, known, dryRun, output)
	if err != nil {
		return runtime.PruneResult{}, err
	}
	result.Items = append(result.Items, containerItems...)

	imageItems := r.pruneDanglingImages(ctx, dryRun, output)
	result.Items = append(result.Items, imageItems...)

	return result, nil
}

// pruneContainers removes orphaned yoloai-* containers not in the known set.
func (r *Runtime) pruneContainers(ctx context.Context, known map[string]bool, dryRun bool, output io.Writer) ([]runtime.PruneItem, error) {
	containers, err := r.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("name", "yoloai-")),
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	var items []runtime.PruneItem
	for _, c := range containers {
		// Container names include a leading "/".
		name := strings.TrimPrefix(c.Names[0], "/")
		if !strings.HasPrefix(name, "yoloai-") || known[name] {
			continue
		}
		if !dryRun && !r.removeContainer(ctx, name, output) {
			continue
		}
		items = append(items, runtime.PruneItem{Kind: "container", Name: name})
	}
	return items, nil
}

// removeContainer removes one container. Returns false if removal failed for a
// reason other than "already gone" (in which case the caller should skip
// recording it as pruned). A warning is written to output on real failures.
func (r *Runtime) removeContainer(ctx context.Context, name string, output io.Writer) bool {
	err := r.client.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
	if err == nil || cerrdefs.IsNotFound(err) {
		return true
	}
	fmt.Fprintf(output, "Warning: failed to remove container %s: %v\n", name, err) //nolint:errcheck // best-effort output
	return false
}

// pruneDanglingImages removes dangling images (stale build layers from rebuilds).
// Failures during listing or removal are reported as warnings; this is best-effort.
func (r *Runtime) pruneDanglingImages(ctx context.Context, dryRun bool, output io.Writer) []runtime.PruneItem {
	danglingImages, err := r.client.ImageList(ctx, image.ListOptions{
		Filters: filters.NewArgs(filters.Arg("dangling", "true")),
	})
	if err != nil {
		fmt.Fprintf(output, "Warning: failed to list dangling images: %v\n", err) //nolint:errcheck // best-effort output
		return nil
	}

	var items []runtime.PruneItem
	for _, img := range danglingImages {
		shortID := strings.TrimPrefix(img.ID, "sha256:")
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		if !dryRun && !r.removeImage(ctx, img.ID, shortID, output) {
			continue
		}
		items = append(items, runtime.PruneItem{Kind: "image", Name: shortID})
	}
	return items
}

// removeImage removes one image. Returns false if removal failed for a reason
// other than "already gone". A warning is written to output on real failures.
func (r *Runtime) removeImage(ctx context.Context, id, shortID string, output io.Writer) bool {
	_, err := r.client.ImageRemove(ctx, id, image.RemoveOptions{Force: true, PruneChildren: true})
	if err == nil || cerrdefs.IsNotFound(err) {
		return true
	}
	fmt.Fprintf(output, "Warning: failed to remove image %s: %v\n", shortID, err) //nolint:errcheck // best-effort output
	return false
}

// formatBytes formats a byte count as a human-readable string.
func formatBytes(b uint64) string {
	const (
		mb = 1024 * 1024
		gb = 1024 * 1024 * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// PruneCache implements runtime.CachePruner. Removes unused images, stopped
// containers, unused volumes, unused networks, and the full BuildKit cache.
// Equivalent to `docker system prune -a --force --volumes` plus a buildx
// prune. Forces a yoloai-base rebuild on next sandbox creation.
//
// Affects ALL backend content, not just yoloai's — appropriate for a host
// dedicated to yoloai testing; on shared hosts users should run the backend's
// own prune commands instead.
func (r *Runtime) PruneCache(ctx context.Context, dryRun bool, output io.Writer) error {
	if dryRun {
		// PruneReport has no dry-run mode in the Docker API; report intent and
		// fall through to leave nothing removed.
		fmt.Fprintf(output, "%s: cache prune skipped (--dry-run): would remove unused images, volumes, build cache\n", r.binaryName) //nolint:errcheck
		return nil
	}

	var reclaimed uint64

	// Containers first (stopped only). Removing stopped containers releases
	// holds on otherwise-unreferenced images.
	if rep, err := r.client.ContainersPrune(ctx, filters.NewArgs()); err == nil {
		reclaimed += rep.SpaceReclaimed
	} else {
		fmt.Fprintf(output, "%s: containers prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	// Images: -a (also non-dangling). Won't touch images still referenced by
	// running containers.
	if rep, err := r.client.ImagesPrune(ctx, filters.NewArgs(filters.Arg("dangling", "false"))); err == nil {
		reclaimed += rep.SpaceReclaimed
	} else {
		fmt.Fprintf(output, "%s: images prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	// Volumes (only unused ones).
	if rep, err := r.client.VolumesPrune(ctx, filters.NewArgs()); err == nil {
		reclaimed += rep.SpaceReclaimed
	} else {
		fmt.Fprintf(output, "%s: volumes prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	// Networks (only unused user-defined networks; defaults are preserved).
	if _, err := r.client.NetworksPrune(ctx, filters.NewArgs()); err != nil {
		fmt.Fprintf(output, "%s: networks prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	// BuildKit cache: usually the biggest single category on a heavy-build host.
	if rep, err := r.client.BuildCachePrune(ctx, build.CachePruneOptions{All: true}); err == nil && rep != nil {
		reclaimed += rep.SpaceReclaimed
	} else if err != nil {
		fmt.Fprintf(output, "%s: build cache prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	fmt.Fprintf(output, "%s: reclaimed %s\n", r.binaryName, formatBytes(reclaimed)) //nolint:errcheck
	return nil
}

// CacheUsage implements runtime.DiskUsageReporter. Returns the total
// daemon-managed bytes (images + containers + volumes + build cache) with a
// short breakdown.
func (r *Runtime) CacheUsage(ctx context.Context) (runtime.CacheUsage, error) {
	du, err := r.client.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return runtime.CacheUsage{BytesUsed: -1}, fmt.Errorf("%s disk usage: %w", r.binaryName, err)
	}
	var total int64
	for _, img := range du.Images {
		total += img.Size
	}
	for _, ct := range du.Containers {
		total += ct.SizeRw
	}
	for _, v := range du.Volumes {
		if v.UsageData != nil && v.UsageData.Size > 0 {
			total += v.UsageData.Size
		}
	}
	for _, bc := range du.BuildCache {
		total += bc.Size
	}
	detail := fmt.Sprintf("%d images, %d containers, %d volumes, %d build-cache entries",
		len(du.Images), len(du.Containers), len(du.Volumes), len(du.BuildCache))
	return runtime.CacheUsage{BytesUsed: total, Detail: detail}, nil
}
