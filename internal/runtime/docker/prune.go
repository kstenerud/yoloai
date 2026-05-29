package docker

// ABOUTME: Finds and removes orphaned yoloai-* Docker containers and dangling images.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"syscall"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"

	"github.com/kstenerud/yoloai/internal/runtime"
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

// PruneCache implements runtime.CachePruner. The depth is set by includeImages:
//
//   - false (plain `prune`): stopped containers + unused volumes/networks +
//     the full BuildKit cache. The base/profile IMAGES are kept, so the next
//     `new` reuses them — no rebuild. On the containerd image store this is the
//     step that actually frees space: image layers stay pinned by the build
//     cache until it's pruned, so removing dangling images alone reclaims
//     nothing (see backend-idiosyncrasies.md).
//   - true (`prune --images`): also `images prune -a`, removing unused base
//     images and forcing a rebuild on next creation.
//
// Affects ALL backend content, not just yoloai's — appropriate for a host
// dedicated to yoloai testing; on shared hosts users should run the backend's
// own prune commands instead.
//
// Returns bytes reclaimed, measured as the free-space delta on the daemon's
// data root (statfs). The Docker SDK's per-call SpaceReclaimed badly
// undercounts on the containerd image store (it ignores layers freed by GC
// once the pinning build cache is gone), so it's used only as a fallback when
// the data root isn't visible on the host filesystem (e.g. Docker Desktop's
// LinuxKit VM on macOS).
func (r *Runtime) PruneCache(ctx context.Context, includeImages, dryRun bool, output io.Writer) (int64, error) {
	if dryRun {
		return r.pruneCacheDryRun(ctx, includeImages, output), nil
	}

	rootDir := r.daemonDataRoot(ctx)
	freeBefore := freeBytes(rootDir) // -1 if not host-visible

	var sdkReclaimed uint64

	// Containers first (stopped only). Removing stopped containers releases
	// holds on otherwise-unreferenced images.
	if rep, err := r.client.ContainersPrune(ctx, filters.NewArgs()); err == nil {
		sdkReclaimed += rep.SpaceReclaimed
	} else {
		fmt.Fprintf(output, "%s: containers prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	// BuildKit cache: usually the biggest single category on a heavy-build
	// host, and the pin that keeps image layers alive on the containerd image
	// store. Prune it before images so the layers they share are actually
	// freed by containerd GC.
	if rep, err := r.client.BuildCachePrune(ctx, build.CachePruneOptions{All: true}); err == nil && rep != nil {
		sdkReclaimed += rep.SpaceReclaimed
	} else if err != nil {
		fmt.Fprintf(output, "%s: build cache prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	// Volumes (only unused ones).
	if rep, err := r.client.VolumesPrune(ctx, filters.NewArgs()); err == nil {
		sdkReclaimed += rep.SpaceReclaimed
	} else {
		fmt.Fprintf(output, "%s: volumes prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	// Networks (only unused user-defined networks; defaults are preserved).
	if _, err := r.client.NetworksPrune(ctx, filters.NewArgs()); err != nil {
		fmt.Fprintf(output, "%s: networks prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	// Images last, and only when asked: -a (also non-dangling). This is what
	// forces a rebuild, so it's gated behind includeImages. Dangling images
	// from prior builds are already removed by the core Prune; here we drop the
	// reusable base/profile images too.
	if includeImages {
		if rep, err := r.client.ImagesPrune(ctx, filters.NewArgs(filters.Arg("dangling", "false"))); err == nil {
			sdkReclaimed += rep.SpaceReclaimed
		} else {
			fmt.Fprintf(output, "%s: images prune failed: %v\n", r.binaryName, err) //nolint:errcheck
		}
	}

	reclaimed := measuredReclaim(freeBefore, freeBytes(rootDir), sdkReclaimed)
	fmt.Fprintf(output, "%s: reclaimed %s\n", r.binaryName, formatBytes(uint64(reclaimed))) //nolint:errcheck,gosec // G115: reclaim is non-negative
	return reclaimed, nil
}

// pruneCacheDryRun reports what PruneCache would remove and returns an estimate
// of the reclaimable bytes (build cache + volumes, plus images when
// includeImages). The Docker API has no dry-run prune, so this reads current
// disk usage instead of removing anything.
func (r *Runtime) pruneCacheDryRun(ctx context.Context, includeImages bool, output io.Writer) int64 {
	du, err := r.client.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		fmt.Fprintf(output, "%s: cache prune skipped (--dry-run); usage query failed: %v\n", r.binaryName, err) //nolint:errcheck
		return 0
	}
	cached, images := splitCacheBytes(du)
	what := "unused volumes, build cache"
	estimate := cached
	if includeImages {
		what = "unused images, volumes, build cache"
		estimate += images
	}
	fmt.Fprintf(output, "%s: cache prune skipped (--dry-run): would remove %s (~%s)\n", r.binaryName, what, formatBytes(uint64(estimate))) //nolint:errcheck,gosec // G115: estimate is non-negative
	return estimate
}

// measuredReclaim prefers the statfs free-space delta (accurate on the
// containerd image store) and falls back to the SDK's reported total when the
// data root isn't visible on the host (freeBefore/freeAfter == -1) or the delta
// is non-positive (concurrent writes outran the prune).
func measuredReclaim(freeBefore, freeAfter int64, sdkReclaimed uint64) int64 {
	if freeBefore >= 0 && freeAfter >= 0 {
		if delta := freeAfter - freeBefore; delta > 0 {
			return delta
		}
	}
	return int64(sdkReclaimed) //nolint:gosec // G115: daemon-reported byte counts fit in int64
}

// CacheUsage implements runtime.DiskUsageReporter, splitting reclaimable bytes
// by whether freeing them forces a rebuild: CachedBytes (build cache + volumes,
// reclaimed by plain `prune`) vs ImageBytes (image layers, reclaimed only by
// `prune --images`).
func (r *Runtime) CacheUsage(ctx context.Context) (runtime.CacheUsage, error) {
	du, err := r.client.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return runtime.CacheUsage{CachedBytes: 0, ImageBytes: -1}, fmt.Errorf("%s disk usage: %w", r.binaryName, err)
	}
	cached, images := splitCacheBytes(du)
	detail := fmt.Sprintf("%d images, %d containers, %d volumes, %d build-cache entries",
		len(du.Images), len(du.Containers), len(du.Volumes), len(du.BuildCache))
	return runtime.CacheUsage{CachedBytes: cached, ImageBytes: images, Detail: detail}, nil
}

// splitCacheBytes returns (cachedBytes, imageBytes): the no-rebuild-forcing
// reclaim (build cache + container writable layers + volumes) and the
// rebuild-forcing reclaim (image layers). The image portion uses du.LayersSize
// — the deduplicated layer-store total that `docker/podman system df` reports —
// NOT the sum of each img.Size, which multiply-counts shared base layers
// (dozens of intermediate build stages sharing one 5 GiB base would otherwise
// read as ~130 GiB).
func splitCacheBytes(du types.DiskUsage) (cached, images int64) {
	images = du.LayersSize
	for _, ct := range du.Containers {
		cached += ct.SizeRw
	}
	for _, v := range du.Volumes {
		if v.UsageData != nil && v.UsageData.Size > 0 {
			cached += v.UsageData.Size
		}
	}
	for _, bc := range du.BuildCache {
		cached += bc.Size
	}
	return cached, images
}

// daemonDataRoot returns the daemon's on-disk data root (e.g. /var/lib/docker),
// used to measure a statfs free-space delta around a prune. Empty string if the
// daemon can't be queried; freeBytes treats that as "not host-visible".
func (r *Runtime) daemonDataRoot(ctx context.Context) string {
	info, err := r.client.Info(ctx)
	if err != nil {
		return ""
	}
	return info.DockerRootDir
}

// freeBytes returns user-visible free bytes on the filesystem backing path, or
// -1 if path is empty or not statable on this host (e.g. the daemon's data root
// lives inside a VM, as with Docker Desktop on macOS).
func freeBytes(path string) int64 {
	if path == "" {
		return -1
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize) //nolint:gosec // G115: filesystem free space fits in int64
}
