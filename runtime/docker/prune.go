package docker

// ABOUTME: Finds and removes orphaned yoloai-* Docker containers and dangling images.

import (
	"context"
	"fmt"
	"io"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
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
