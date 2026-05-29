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

// snapshotterNames lists the snapshotters yoloai populates: overlayfs for
// container/VM isolation, devmapper for --isolation vm-enhanced (Firecracker).
// Both extract a physical copy of the base image, so a host that has run both
// isolation modes holds two on-disk copies — they're summed and pruned
// independently. A snapshotter that isn't configured on this host (devmapper on
// a plain Linux box) is skipped silently when its Walk fails.
var snapshotterNames = []string{"overlayfs", "devmapper"}

// PruneCache implements runtime.CachePruner for containerd. Removes every
// image and snapshot in the yoloai namespace (assumed dedicated to yoloai),
// then lets containerd's garbage collector reclaim the unreferenced content
// blobs and snapshot dirs. Forces a yoloai-base rebuild + re-link on next
// sandbox creation. Returns the on-disk bytes released, summed from the Usage
// of each removed snapshot.
//
// Refuses to run if any container in the namespace is still active — the
// caller must stop those first (typically via `yoloai destroy` or
// `yoloai system prune`).
func (r *Runtime) PruneCache(ctx context.Context, includeImages, dryRun bool, output io.Writer) (int64, error) {
	// The containerd backend has no regenerable build cache distinct from the
	// base image — image layers and their snapshots ARE the only reclaimable
	// content, and removing them forces a rebuild. So plain `prune`
	// (includeImages=false) is a no-op; only `prune --images` reclaims.
	if !includeImages {
		return 0, nil
	}

	ctx = r.withNamespace(ctx)

	if err := r.refuseIfContainersExist(ctx); err != nil {
		return 0, err
	}
	if err := r.pruneImages(ctx, dryRun, output); err != nil {
		return 0, err
	}
	return r.pruneSnapshots(ctx, dryRun, output), nil
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

// snapshotNames returns the names of every snapshot in the given snapshotter
// within the current namespace. present is false when the snapshotter isn't
// configured on this host (Walk RPC fails) — callers skip it silently.
func (r *Runtime) snapshotNames(ctx context.Context, snapshotter string) (names []string, present bool) {
	snapSvc := r.client.SnapshotService(snapshotter)
	if err := snapSvc.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		names = append(names, info.Name)
		return nil
	}); err != nil {
		return nil, false
	}
	return names, true
}

// pruneSnapshots removes every snapshot in each configured snapshotter (both
// overlayfs and devmapper) within the namespace, returning the on-disk bytes
// released (summed from each removed snapshot's Usage, measured before
// removal). Active snapshots or those with active children return
// FailedPrecondition and are skipped silently — GC reclaims them once the
// holding container is gone.
//
// devmapper caveat: removing a thin snapshot frees blocks back to the
// thin-pool, but the pool's backing loopback file does not shrink, so host
// `df` is unchanged even though the pool regains free blocks. We surface this
// so the reported reclaim isn't mistaken for freed disk.
func (r *Runtime) pruneSnapshots(ctx context.Context, dryRun bool, output io.Writer) int64 {
	var reclaimed int64
	touchedDevmapper := false
	for _, snapshotter := range snapshotterNames {
		names, present := r.snapshotNames(ctx, snapshotter)
		if !present || len(names) == 0 {
			continue
		}
		if snapshotter == "devmapper" {
			touchedDevmapper = true
		}
		reclaimed += r.pruneSnapshotter(ctx, snapshotter, names, dryRun, output)
	}
	if touchedDevmapper {
		verb := "are returned"
		if dryRun {
			verb = "would be returned"
		}
		fmt.Fprintf(output, "containerd: devmapper blocks %s to the thin-pool; the pool backing file does not shrink, so host df is unchanged\n", verb) //nolint:errcheck
	}
	return reclaimed
}

// pruneSnapshotter removes the named snapshots from one snapshotter, returning
// the bytes released (each snapshot's Usage, measured before removal). In
// dry-run it only reports. Snapshots that are active or have active children
// return FailedPrecondition and are skipped silently.
func (r *Runtime) pruneSnapshotter(ctx context.Context, snapshotter string, names []string, dryRun bool, output io.Writer) int64 {
	snapSvc := r.client.SnapshotService(snapshotter)
	var reclaimed int64
	for _, name := range names {
		var size int64
		if u, uerr := snapSvc.Usage(ctx, name); uerr == nil {
			size = u.Size
		}
		if dryRun {
			fmt.Fprintf(output, "containerd: would remove %s snapshot %s\n", snapshotter, name) //nolint:errcheck
			reclaimed += size
			continue
		}
		if err := snapSvc.Remove(ctx, name); err != nil {
			if !cerrdefs.IsFailedPrecondition(err) && !cerrdefs.IsNotFound(err) {
				fmt.Fprintf(output, "containerd: failed to remove %s snapshot %s: %v\n", snapshotter, name, err) //nolint:errcheck
			}
			continue
		}
		reclaimed += size
	}
	return reclaimed
}

// CacheUsage implements runtime.DiskUsageReporter for containerd. Sums the
// on-disk Usage of every snapshot across both snapshotters (overlayfs +
// devmapper) in the yoloai namespace — these layers ARE the only reclaimable
// content and removing them forces a rebuild, so the total is reported as
// ImageBytes. CachedBytes is always 0 (containerd has no no-rebuild-forcing
// cache). Querying Usage per snapshot goes through the containerd socket, so
// it needs no host filesystem access (yoloai may run unprivileged via the
// containerd group) and no devmapper/dmsetup privileges.
func (r *Runtime) CacheUsage(ctx context.Context) (runtime.CacheUsage, error) {
	ctx = r.withNamespace(ctx)

	imgs, err := r.client.ImageService().List(ctx)
	if err != nil {
		return runtime.CacheUsage{CachedBytes: 0, ImageBytes: -1}, fmt.Errorf("list images: %w", err)
	}

	var total int64
	var parts []string
	hasDevmapper := false
	for _, snapshotter := range snapshotterNames {
		names, present := r.snapshotNames(ctx, snapshotter)
		if !present || len(names) == 0 {
			continue
		}
		snapSvc := r.client.SnapshotService(snapshotter)
		var bytes int64
		for _, name := range names {
			if u, uerr := snapSvc.Usage(ctx, name); uerr == nil {
				bytes += u.Size
			}
		}
		total += bytes
		parts = append(parts, fmt.Sprintf("%s: %d snapshots", snapshotter, len(names)))
		if snapshotter == "devmapper" {
			hasDevmapper = true
		}
	}

	detail := fmt.Sprintf("%d images in namespace %q", len(imgs), r.namespace)
	if len(parts) > 0 {
		detail += "; " + strings.Join(parts, ", ")
	}
	if hasDevmapper {
		detail += "; devmapper bytes are thin-pool allocation (the pool file does not shrink on prune)"
	}

	return runtime.CacheUsage{
		CachedBytes: 0,
		ImageBytes:  total,
		Detail:      detail,
	}, nil
}
