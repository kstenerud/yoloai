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

	containers, err := r.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("name", "yoloai-")),
	})
	if err != nil {
		return runtime.PruneResult{}, fmt.Errorf("list containers: %w", err)
	}

	var result runtime.PruneResult
	for _, c := range containers {
		// Container names include a leading "/".
		name := strings.TrimPrefix(c.Names[0], "/")
		if !strings.HasPrefix(name, "yoloai-") {
			continue
		}
		if known[name] {
			continue
		}

		item := runtime.PruneItem{
			Kind: "container",
			Name: name,
		}

		if !dryRun {
			if err := r.client.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil {
				if !cerrdefs.IsNotFound(err) {
					fmt.Fprintf(output, "Warning: failed to remove container %s: %v\n", name, err) //nolint:errcheck // best-effort output
					continue
				}
				// Container already gone — treat as successful deletion.
			}
		}
		result.Items = append(result.Items, item)
	}

	// Scan for dangling images (stale build layers from image rebuilds).
	danglingImages, err := r.client.ImageList(ctx, image.ListOptions{
		Filters: filters.NewArgs(filters.Arg("dangling", "true")),
	})
	if err != nil {
		fmt.Fprintf(output, "Warning: failed to list dangling images: %v\n", err) //nolint:errcheck // best-effort output
		return result, nil
	}

	for _, img := range danglingImages {
		shortID := strings.TrimPrefix(img.ID, "sha256:")
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}

		item := runtime.PruneItem{
			Kind: "image",
			Name: shortID,
		}

		if !dryRun {
			if _, removeErr := r.client.ImageRemove(ctx, img.ID, image.RemoveOptions{Force: true, PruneChildren: true}); removeErr != nil {
				if !cerrdefs.IsNotFound(removeErr) {
					fmt.Fprintf(output, "Warning: failed to remove image %s: %v\n", shortID, removeErr) //nolint:errcheck // best-effort output
					continue
				}
				// Image already gone — treat as successful deletion.
			}
		}
		result.Items = append(result.Items, item)
	}

	return result, nil
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
