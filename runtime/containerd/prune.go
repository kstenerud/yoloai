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

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/runtime"
)

// Prune removes orphaned containerd containers in the yoloai namespace.
// A container is orphaned when its com.yoloai.* labels mark it a yoloai instance
// owned by this principal (runtime.IsOrphanCandidate, D62) and its name is not in
// knownInstances. For each removed container, CNI teardown is attempted.
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
		// Identify candidates by label, not name; the principal label scopes the
		// sweep so a test or secondary principal never reclaims another
		// principal's containers (DF19).
		labels, err := ctr.Labels(ctx)
		if err != nil {
			continue // can't read labels (container vanishing mid-sweep) — skip
		}
		if !runtime.IsOrphanCandidate(labels, r.layout.Principal) {
			continue
		}
		name := ctr.ID()
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
	reclaimed := r.pruneSnapshots(ctx, dryRun, output)
	// Print a per-backend reclaim line like docker/podman so containerd's
	// contribution to the aggregate is visible rather than silently folded in.
	// The figure is the overlayfs snapshot reclaim only: the image content-store
	// blobs that pruneImages frees are not separately measured (DF59), so on a
	// host that mostly held content this can read low despite real reclaim.
	if !dryRun {
		fmt.Fprintf(output, "containerd: reclaimed %s (snapshot layers; image content not separately measured)\n", //nolint:errcheck
			runtime.FormatBytes(reclaimed))
	}
	return reclaimed, nil
}

// refuseIfContainersExist returns an error if any container owned by this
// principal still exists in the namespace; cache prune isn't safe while one
// exists. Scoped to this runtime's principal (DF19).
func (r *Runtime) refuseIfContainersExist(ctx context.Context) error {
	containers, err := r.client.Containers(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}
	prefix := config.InstancePrefix(r.layout.Principal)
	for _, ctr := range containers {
		if strings.HasPrefix(ctr.ID(), prefix) {
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

// snapshotInfos returns the Info of every snapshot in the given snapshotter
// within the current namespace. present is false when the snapshotter isn't
// configured on this host (Walk RPC fails) — callers skip it silently. Info
// carries each snapshot's Parent, which prune uses to remove children first.
func (r *Runtime) snapshotInfos(ctx context.Context, snapshotter string) (infos []snapshots.Info, present bool) {
	snapSvc := r.client.SnapshotService(snapshotter)
	if err := snapSvc.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
		infos = append(infos, info)
		return nil
	}); err != nil {
		return nil, false
	}
	return infos, true
}

// orderLeafFirst returns the snapshot names ordered so a snapshot always
// precedes its parent (children first). Removing in this order means every
// Remove succeeds synchronously: containerd refuses to remove a snapshot that
// still has a child (FailedPrecondition), and walking in arbitrary order would
// hit that for most of a layer chain, leaving the bulk to be reclaimed only by
// a later GC pass (or not at all, for snapshots GC doesn't root). A Kahn-style
// topological pass over the in-memory Parent links avoids both. Any snapshot
// not reached (e.g. a parent outside this set, or a cycle that shouldn't exist)
// is appended at the end so nothing is silently dropped.
func orderLeafFirst(infos []snapshots.Info) []string {
	inSet := make(map[string]bool, len(infos))
	for _, info := range infos {
		inSet[info.Name] = true
	}
	childCount := make(map[string]int, len(infos))
	for _, info := range infos {
		if info.Parent != "" && inSet[info.Parent] {
			childCount[info.Parent]++
		}
	}
	parentOf := make(map[string]string, len(infos))
	var queue []string
	for _, info := range infos {
		parentOf[info.Name] = info.Parent
		if childCount[info.Name] == 0 {
			queue = append(queue, info.Name)
		}
	}
	ordered := make([]string, 0, len(infos))
	emitted := make(map[string]bool, len(infos))
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		ordered = append(ordered, name)
		emitted[name] = true
		if parent := parentOf[name]; parent != "" && inSet[parent] {
			childCount[parent]--
			if childCount[parent] == 0 {
				queue = append(queue, parent)
			}
		}
	}
	for _, info := range infos {
		if !emitted[info.Name] {
			ordered = append(ordered, info.Name)
		}
	}
	return ordered
}

// pruneSnapshots removes every snapshot in each configured snapshotter (both
// overlayfs and devmapper) within the namespace, returning only the bytes that
// free HOST disk — i.e. the overlayfs reclaim. Each figure is summed from the
// removed snapshot's Usage, measured just before removal; removal is leaf-first
// (see orderLeafFirst) so the chain frees synchronously and the figure reflects
// bytes truly reclaimed, not an optimistic later-GC assumption.
//
// devmapper is reported separately and EXCLUDED from the returned total:
// removing a thin snapshot returns blocks to the thin-pool, but the pool's
// backing loopback file does not shrink, so host `df` is unchanged. Counting
// those bytes as "reclaimed" would over-report freed disk, so we surface them in
// their own line and leave them out of the total.
func (r *Runtime) pruneSnapshots(ctx context.Context, dryRun bool, output io.Writer) int64 {
	var hostFreed, devmapperBytes int64
	for _, snapshotter := range snapshotterNames {
		infos, present := r.snapshotInfos(ctx, snapshotter)
		if !present || len(infos) == 0 {
			continue
		}
		n := r.pruneSnapshotter(ctx, snapshotter, orderLeafFirst(infos), dryRun, output)
		// devmapper blocks return to the thin-pool but the backing file does not
		// shrink, so they free no host disk — report them separately and DON'T
		// count them in the reclaimed total (overcount otherwise). overlayfs
		// snapshots do free host disk and are counted.
		if snapshotter == "devmapper" {
			devmapperBytes += n
		} else {
			hostFreed += n
		}
	}
	if devmapperBytes > 0 {
		verb := "returned"
		if dryRun {
			verb = "would be returned"
		}
		fmt.Fprintf(output, "containerd: %s of devmapper blocks %s to the thin-pool; the pool backing file does not shrink, so this frees no host disk (excluded from the reclaimed total)\n", //nolint:errcheck
			runtime.FormatBytes(devmapperBytes), verb)
	}
	return hostFreed
}

// pruneSnapshotter removes the given snapshots (in the leaf-first order the
// caller supplies) from one snapshotter, returning the bytes released — each
// snapshot's Usage, measured just before its own removal. In dry-run it only
// reports. With children removed before parents, every Remove succeeds; a
// snapshot already gone (NotFound) still counts. Only a genuine Remove error
// (including a FailedPrecondition that survives correct ordering, e.g. an
// unexpected external hold) is excluded from the total and warned about.
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
		if err := snapSvc.Remove(ctx, name); err != nil && !cerrdefs.IsNotFound(err) {
			fmt.Fprintf(output, "containerd: failed to remove %s snapshot %s: %v\n", snapshotter, name, err) //nolint:errcheck
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
		infos, present := r.snapshotInfos(ctx, snapshotter)
		if !present || len(infos) == 0 {
			continue
		}
		snapSvc := r.client.SnapshotService(snapshotter)
		var bytes int64
		for _, info := range infos {
			if u, uerr := snapSvc.Usage(ctx, info.Name); uerr == nil {
				bytes += u.Size
			}
		}
		total += bytes
		parts = append(parts, fmt.Sprintf("%s: %d snapshots", snapshotter, len(infos)))
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
