//go:build linux

package containerdrt

// ABOUTME: Prune orphaned containerd containers and CNI state from the yoloai namespace.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/snapshots"
	cerrdefs "github.com/containerd/errdefs"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// Prune removes orphaned containerd containers in the yoloai namespace.
// Any container named yoloai-* that is not in knownInstances is considered orphaned.
// For each removed container, CNI teardown is attempted.
func (r *Runtime) Prune(ctx context.Context, knownInstances []string, dryRun bool, output io.Writer) (runtime.PruneResult, error) {
	ctx = r.withNamespace(ctx)

	known := make(map[string]bool, len(knownInstances))
	for _, name := range knownInstances {
		known[name] = true
	}

	containers, err := r.client.Containers(ctx)
	if err != nil {
		return runtime.PruneResult{}, fmt.Errorf("list containers: %w", err)
	}

	var result runtime.PruneResult
	for _, ctr := range containers {
		name := ctr.ID()
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
			if err := r.Remove(ctx, name); err != nil {
				if !errors.Is(err, runtime.ErrNotFound) {
					fmt.Fprintf(output, "Warning: failed to remove container %s: %v\n", name, err) //nolint:errcheck // best-effort output
					continue
				}
				// Container already gone — treat as successful deletion.
			}
		}
		result.Items = append(result.Items, item)
	}

	return result, nil
}

// PruneCache implements runtime.CachePruner for containerd. Removes every
// image and snapshot in the yoloai namespace (assumed dedicated to yoloai),
// then lets containerd's garbage collector reclaim the unreferenced content
// blobs and overlay snapshot dirs. Forces a yoloai-base rebuild + re-link on
// next sandbox creation.
//
// Refuses to run if any container in the namespace is still active — the
// caller must stop those first (typically via `yoloai destroy` or
// `yoloai system prune`).
func (r *Runtime) PruneCache(ctx context.Context, dryRun bool, output io.Writer) error {
	ctx = r.withNamespace(ctx)

	if err := r.refuseIfContainersExist(ctx); err != nil {
		return err
	}
	if err := r.pruneImages(ctx, dryRun, output); err != nil {
		return err
	}
	r.pruneSnapshots(ctx, dryRun, output)
	return nil
}

// refuseIfContainersExist returns an error if any yoloai-* container record
// is still present in the namespace; cache prune isn't safe while one exists.
func (r *Runtime) refuseIfContainersExist(ctx context.Context) error {
	containers, err := r.client.Containers(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	for _, ctr := range containers {
		if strings.HasPrefix(ctr.ID(), "yoloai-") {
			return fmt.Errorf("containerd cache prune: container %q still exists in yoloai namespace; stop and remove it first (yoloai system prune)", ctr.ID())
		}
	}
	return nil
}

// pruneImages removes every image record in the namespace (or reports what
// would be removed in dry-run mode).
func (r *Runtime) pruneImages(ctx context.Context, dryRun bool, output io.Writer) error {
	imgSvc := r.client.ImageService()
	imgs, err := imgSvc.List(ctx)
	if err != nil {
		return fmt.Errorf("list images: %w", err)
	}
	for _, img := range imgs {
		if dryRun {
			fmt.Fprintf(output, "containerd: would remove image %s\n", img.Name) //nolint:errcheck
			continue
		}
		if err := imgSvc.Delete(ctx, img.Name, images.SynchronousDelete()); err != nil && !cerrdefs.IsNotFound(err) {
			fmt.Fprintf(output, "containerd: failed to remove image %s: %v\n", img.Name, err) //nolint:errcheck
			continue
		}
		fmt.Fprintf(output, "containerd: removed image %s\n", img.Name) //nolint:errcheck
	}
	return nil
}

// pruneSnapshots removes every overlayfs snapshot in the namespace. Active
// snapshots or those with active children return FailedPrecondition and are
// skipped silently — GC will reclaim them once the holding container is gone.
func (r *Runtime) pruneSnapshots(ctx context.Context, dryRun bool, output io.Writer) {
	snapSvc := r.client.SnapshotService("overlayfs")
	var snapNames []string
	if err := snapSvc.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		snapNames = append(snapNames, info.Name)
		return nil
	}); err != nil {
		fmt.Fprintf(output, "containerd: snapshot walk failed: %v\n", err) //nolint:errcheck
	}
	for _, name := range snapNames {
		if dryRun {
			fmt.Fprintf(output, "containerd: would remove snapshot %s\n", name) //nolint:errcheck
			continue
		}
		if err := snapSvc.Remove(ctx, name); err != nil {
			if !cerrdefs.IsFailedPrecondition(err) && !cerrdefs.IsNotFound(err) {
				fmt.Fprintf(output, "containerd: failed to remove snapshot %s: %v\n", name, err) //nolint:errcheck
			}
		}
	}
}

// CacheUsage implements runtime.DiskUsageReporter for containerd. Counts
// images and snapshots in the yoloai namespace; byte totals require reading
// the content store which is expensive, so BytesUsed is left at -1 (unknown)
// and the count is reported in Detail.
func (r *Runtime) CacheUsage(ctx context.Context) (runtime.CacheUsage, error) {
	ctx = r.withNamespace(ctx)

	imgs, err := r.client.ImageService().List(ctx)
	if err != nil {
		return runtime.CacheUsage{BytesUsed: -1}, fmt.Errorf("list images: %w", err)
	}

	var snapCount int
	_ = r.client.SnapshotService("overlayfs").Walk(ctx, func(_ context.Context, _ snapshots.Info) error {
		snapCount++
		return nil
	})

	return runtime.CacheUsage{
		BytesUsed: -1,
		Detail:    fmt.Sprintf("%d images, %d snapshots in namespace %q (run 'du -sh /var/lib/containerd' for bytes)", len(imgs), snapCount, r.namespace),
	}, nil
}
