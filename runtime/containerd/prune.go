//go:build linux

package containerdrt

// ABOUTME: Prune orphaned containerd containers and CNI state from the yoloai namespace.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
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

	proceed, err := r.reconcileBlockingContainers(ctx, dryRun, output)
	if err != nil {
		return 0, err
	}
	if !proceed {
		return 0, nil
	}

	// Measure the content store before and after the image prune. Deleting an
	// image record with SynchronousDelete GCs its now-unreferenced content blobs
	// before returning (images.SynchronousDelete docs), so the drop in
	// content-store size is host disk that the prune actually freed. In dry-run
	// nothing is deleted, so the whole content store is what *would* be freed.
	contentBefore := r.contentStoreBytes(ctx)
	if err := r.pruneImages(ctx, dryRun, output); err != nil {
		return 0, err
	}
	contentFreed := contentBefore
	if !dryRun {
		contentFreed = max(contentBefore-r.contentStoreBytes(ctx), 0)
	}

	snapFreed := r.pruneSnapshots(ctx, dryRun, output)
	reclaimed := contentFreed + snapFreed
	// Print a per-backend reclaim line like docker/podman so containerd's
	// contribution to the aggregate is visible rather than silently folded in.
	// Both tiers free host disk and are counted: the content store (compressed
	// blobs) and the overlayfs snapshots (extracted layers). devmapper snapshot
	// blocks return to the thin-pool but free no host disk, so pruneSnapshots
	// reports them separately and excludes them from snapFreed (DF59).
	if !dryRun {
		fmt.Fprintf(output, "containerd: reclaimed %s (%s image content + %s snapshot layers)\n", //nolint:errcheck
			runtime.FormatBytes(reclaimed), runtime.FormatBytes(contentFreed), runtime.FormatBytes(snapFreed))
	}
	return reclaimed, nil
}

// contentStoreBytes sums the on-disk size of every blob in the namespace's
// content store (compressed image layers, manifests, configs). Best-effort: a
// Walk error yields the bytes counted so far rather than failing the prune —
// the figure is a reporting aid, not a correctness invariant. The content store
// is namespaced, so this counts only yoloai's blobs.
func (r *Runtime) contentStoreBytes(ctx context.Context) int64 {
	var total int64
	cs := r.client.ContentStore()
	_ = cs.Walk(ctx, func(info content.Info) error { //nolint:errcheck // best-effort accounting
		total += info.Size
		return nil
	})
	return total
}

// reconcileBlockingContainers prepares the namespace for a cache prune that
// removes the base image and its snapshots. A container that still holds a
// snapshot reference makes image/snapshot removal fail, so the prune cannot
// proceed while one exists. Rather than aborting ALL containerd reclaim on the
// first container it sees (DF59 — one stale stopped sandbox defeated the whole
// command), it classifies the principal's containers (scoped by instance prefix,
// DF19) and reconciles:
//
//   - A RUNNING container is a live sandbox whose agent must not be killed by a
//     cache prune. Its presence skips image reclaim entirely — the shared base it
//     pins can't be removed anyway — and each is reported with a stop/destroy fix
//     command. Returns proceed=false.
//   - A STOPPED container only pins the image because it lingers. `--images` is
//     already destructive (forces a base rebuild) and a stopped container carries
//     no live session — `start` recreates it on demand — so it is removed to let
//     the reclaim proceed, and reported. Returns proceed=true.
//
// In dry-run nothing is removed; blockers are reported as "would …". A stopped
// container that fails to remove is treated as a hard blocker (skip, report).
func (r *Runtime) reconcileBlockingContainers(ctx context.Context, dryRun bool, output io.Writer) (proceed bool, err error) {
	containers, err := r.client.Containers(ctx)
	if err != nil {
		return false, fmt.Errorf("list containers: %w", err)
	}
	prefix := config.InstancePrefix(r.layout.Principal)

	var running, stopped []string
	for _, ctr := range containers {
		if !strings.HasPrefix(ctr.ID(), prefix) {
			continue
		}
		if r.containerRunning(ctx, ctr) {
			running = append(running, ctr.ID())
		} else {
			stopped = append(stopped, ctr.ID())
		}
	}

	if len(running) > 0 {
		for _, name := range running {
			fmt.Fprintf(output, "containerd: skipping image reclaim — sandbox %q is running; stop it first (yoloai stop %s, or yoloai destroy %s)\n", //nolint:errcheck
				name, name, name)
		}
		return false, nil
	}

	for _, name := range stopped {
		if dryRun {
			fmt.Fprintf(output, "containerd: would remove stopped sandbox container %s to reclaim its image (recreated on next start)\n", name) //nolint:errcheck
			continue
		}
		if rmErr := r.Remove(ctx, name); rmErr != nil && !errors.Is(rmErr, runtime.ErrNotFound) {
			fmt.Fprintf(output, "containerd: could not remove stopped container %s: %v — skipping image reclaim\n", name, rmErr) //nolint:errcheck
			return false, nil
		}
		fmt.Fprintf(output, "containerd: removed stopped sandbox container %s to reclaim its image (recreated on next start)\n", name) //nolint:errcheck
	}
	return true, nil
}

// containerRunning reports whether the container has a task in the Running
// state. No task (or any status-read failure) is treated as not-running — a
// container with no live task cannot be holding an active agent session.
func (r *Runtime) containerRunning(ctx context.Context, ctr client.Container) bool {
	task, taskErr := ctr.Task(ctx, nil)
	if taskErr != nil {
		return false
	}
	status, statusErr := task.Status(ctx)
	if statusErr != nil {
		return false
	}
	return status.Status == client.Running
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
// devmapper is reported separately and EXCLUDED from the returned total because
// whether it frees host disk depends on a pool config yoloai can't see over the
// socket: removing a thin snapshot always returns its blocks to the thin-pool,
// but only a pool created with discard_blocks = true (which yoloai cannot detect
// via the snapshot API, and does not own — the pool is a host prerequisite)
// passes a BLKDISCARD down to the sparse backing file so host `df` actually
// drops. Without it the backing file stays fully allocated. Counting these bytes
// in the reclaimed total would over-report on a no-discard pool, so we surface
// them on their own line with the discard caveat and leave them out (DF59).
func (r *Runtime) pruneSnapshots(ctx context.Context, dryRun bool, output io.Writer) int64 {
	var hostFreed, devmapperBytes int64
	for _, snapshotter := range snapshotterNames {
		infos, present := r.snapshotInfos(ctx, snapshotter)
		if !present || len(infos) == 0 {
			continue
		}
		n := r.pruneSnapshotter(ctx, snapshotter, orderLeafFirst(infos), dryRun, output)
		// devmapper host reclaim is discard-dependent (see the doc comment), so
		// it's reported separately and kept out of the counted total; overlayfs
		// snapshots always free host disk and are counted.
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
		fmt.Fprintf(output, "containerd: %s of devmapper blocks %s to the thin-pool (not counted in the reclaimed total).\n", //nolint:errcheck
			runtime.FormatBytes(devmapperBytes), verb)
		fmt.Fprintf(output, "  With discard_blocks = true in the pool config (recommended) this is also freed from the host; otherwise the pool backing file stays allocated — enable discard_blocks, or reclaim it manually (see backend-idiosyncrasies.md \"devmapper caveat — discard_blocks\").\n") //nolint:errcheck
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
// on-disk footprint of yoloai's images in the namespace — the content store
// (compressed blobs) plus the extracted snapshots across both snapshotters
// (overlayfs + devmapper). These ARE the only reclaimable content and removing
// them forces a rebuild, so the total is reported as ImageBytes. The two tiers
// are distinct on-disk storage (blobs vs extracted layers) and coexist, so
// summing them is the true footprint, not a double-count. CachedBytes is always
// 0 (containerd has no no-rebuild-forcing cache). Every query goes through the
// containerd socket, so it needs no host filesystem access (yoloai may run
// unprivileged via the containerd group) and no devmapper/dmsetup privileges.
func (r *Runtime) CacheUsage(ctx context.Context) (runtime.CacheUsage, error) {
	ctx = r.withNamespace(ctx)

	imgs, err := r.client.ImageService().List(ctx)
	if err != nil {
		return runtime.CacheUsage{CachedBytes: 0, ImageBytes: -1}, fmt.Errorf("list images: %w", err)
	}

	contentBytes := r.contentStoreBytes(ctx)
	total := contentBytes
	parts := []string{fmt.Sprintf("content store: %s", runtime.FormatBytes(contentBytes))}
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
