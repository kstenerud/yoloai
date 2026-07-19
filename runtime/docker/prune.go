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

// managedLabel marks Docker volumes (and any future networks) created by
// yoloai. Volume/network reclaim accounting (splitCacheBytes) and pruning
// (PruneCache) are scoped to resources carrying this label so yoloai never
// counts or deletes the user's unrelated ones — e.g. a project's database
// volume. yoloai creates no volumes or networks today; any future code that
// does MUST stamp them with this label.
const managedLabel = "com.yoloai.managed"

// Prune implements runtime.Backend.
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

// pruneContainers removes orphaned containers owned by this runtime's principal
// that are not in the known set. Candidates are identified by the canonical
// com.yoloai.* labels (runtime.IsOrphanCandidate, D62) rather than the yoloai-*
// name prefix, so a foreign container merely named yoloai-* is never removed and
// the per-principal scoping (DF19) comes from the principal label. The known set
// and removal stay keyed on the real container name.
func (r *Runtime) pruneContainers(ctx context.Context, known map[string]bool, dryRun bool, output io.Writer) ([]runtime.PruneItem, error) {
	containers, err := r.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", runtime.LabelSandbox)),
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	var items []runtime.PruneItem
	for _, c := range containers {
		if !runtime.IsOrphanCandidate(c.Labels, r.principal) {
			continue
		}
		// Container names include a leading "/".
		name := strings.TrimPrefix(c.Names[0], "/")
		if known[name] {
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
		shortID := shortImageID(img.ID)
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

// pruneManagedImages is the scoped `--images` sweep: it removes unused images
// that are yoloai's — carrying managedLabel (stamped by the base Dockerfile
// and inherited by `FROM yoloai-base` profile builds) or, as a deprecated
// bridge, bearing a bare-local `yoloai-` name (the population built before
// the label existed; registered with a sunset in
// docs/contributors/deprecations.md). A foreign unused image on a shared
// daemon is spared. "Unused" means referenced by no container at all,
// foreign containers included — a foreign container legitimately pins any
// image, ours or not.
//
// The docker API's ImagesPrune cannot express this predicate (it filters on
// labels but not names), so the sweep lists and removes individually — the
// same shape as pruneDanglingImages. Slightly more round-trips than a
// server-side prune; accepted for the isolation, and the name half retires
// with the bridge.
func (r *Runtime) pruneManagedImages(ctx context.Context, output io.Writer) {
	imgs, err := r.client.ImageList(ctx, image.ListOptions{})
	if err != nil {
		fmt.Fprintf(output, "%s: images prune: list images failed: %v\n", r.binaryName, err) //nolint:errcheck
		return
	}
	// Without the full container list we cannot know what is in use, and
	// guessing risks removing a pinned image — so remove nothing.
	containers, err := r.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		fmt.Fprintf(output, "%s: images prune: list containers failed: %v\n", r.binaryName, err) //nolint:errcheck
		return
	}
	inUse := make(map[string]bool, len(containers))
	for _, c := range containers {
		inUse[c.ImageID] = true
	}
	for _, cand := range managedImageCandidates(imgs, inUse) {
		if cand.nameOnly {
			// The discrepancy signal for the bridge's settling period: this
			// image predates the label. The population should decay to zero as
			// rebuilds re-stamp; if it doesn't, the labeling machinery has a
			// bug to find before the bridge retires.
			fmt.Fprintf(output, "%s: image %s matched by name only (pre-label; rebuilt images carry %s)\n", //nolint:errcheck
				r.binaryName, cand.display, managedLabel)
		}
		for _, ref := range cand.removeRefs {
			_ = r.removeImage(ctx, ref, cand.display, output)
		}
	}
}

// managedImageCandidate is one unused yoloai image the scoped sweep will
// remove. nameOnly marks a match via the deprecated name bridge rather than
// the label — the signal logged during the bridge's settling period.
// removeRefs is what to hand ImageRemove: the image ID for a labeled image
// (whole-image removal — the label marks it yoloai-derived even under a
// foreign tag), but only the matching yoloai tags for a bridge match, so a
// foreign image someone re-tagged with a yoloai- name loses that tag while
// its own tags — and the image under them — survive.
type managedImageCandidate struct {
	id         string
	display    string
	nameOnly   bool
	removeRefs []string
}

// managedImageCandidates selects the unused yoloai images from a full image
// list: not referenced by any container, and carrying managedLabel or (bridge)
// a bare-local yoloai- name. Pure so the selection is testable without a
// daemon.
func managedImageCandidates(imgs []image.Summary, inUse map[string]bool) []managedImageCandidate {
	var out []managedImageCandidate
	for _, img := range imgs {
		if inUse[img.ID] {
			continue
		}
		_, labeled := img.Labels[managedLabel]
		var named []string
		for _, t := range img.RepoTags {
			if yoloaiImageName(t) {
				named = append(named, t)
			}
		}
		if !labeled && len(named) == 0 {
			continue
		}
		display := ""
		removeRefs := []string{img.ID}
		switch {
		case len(named) > 0:
			display = named[0]
			if !labeled {
				removeRefs = named
			}
		case len(img.RepoTags) > 0:
			display = img.RepoTags[0]
		default:
			display = shortImageID(img.ID)
		}
		out = append(out, managedImageCandidate{id: img.ID, display: display, nameOnly: !labeled, removeRefs: removeRefs})
	}
	return out
}

// yoloaiImageName reports whether ref is a bare-local yoloai image name:
// `yoloai-<something>[:tag]` with no registry or repository path component, so
// a registry-qualified foreign image like ghcr.io/x/yoloai-base never
// matches. Every image yoloai has ever built is named this way — yoloai-base
// (config.BaseImage) and the InstancePrefix-derived profile tags, including
// the pre-D126 legacy yoloai-<profile> shape.
//
// Deprecated bridge: this predicate exists only so images built before
// managedLabel was stamped keep being reclaimed by --images. It retires on
// the schedule in docs/contributors/deprecations.md; the label is the
// authoritative marker.
func yoloaiImageName(ref string) bool {
	if strings.Contains(ref, "/") {
		return false
	}
	repo, _, _ := strings.Cut(ref, ":")
	return strings.HasPrefix(repo, "yoloai-")
}

// shortImageID renders a sha256 image ID in the familiar 12-char short form.
func shortImageID(id string) string {
	short := strings.TrimPrefix(id, "sha256:")
	if len(short) > 12 {
		short = short[:12]
	}
	return short
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
// Scoped to yoloai's own content: the stopped-container, volume, and network
// reclaim are label-filtered (com.yoloai.sandbox / com.yoloai.managed) so a
// shared daemon's foreign content is never removed (DF137). Two categories
// cannot be scoped and stay daemon-wide, because neither carries per-project
// attribution: the BuildKit build cache (always), and — only under
// includeImages — the unused-image sweep (see ImagesPrune below). Both force a
// rebuild/re-pull on a shared host but never lose data. Plain prune (the common
// case) touches neither the images nor a foreign container/volume/network.
//
// Returns bytes reclaimed, measured as the drop in this backend's own
// CacheUsage across the prune (before − after) rather than the SDK's
// SpaceReclaimed. SpaceReclaimed is unreliable on the docker-compat API: on the
// containerd image store it undercounts (it returns before GC frees the layers
// the now-pruned build cache had pinned), and Podman's docker-compat
// ImagesPrune reports the UN-deduplicated sum of every removed image's size —
// inflating a ~5 GiB shared-base footprint to ~140 GiB. CacheUsage already
// measures the deduplicated logical footprint accurately for both engines
// (Podman injects a dedup via SetImageBytesFunc), so its before/after delta is
// the truthful self-attributed reclaim: it counts only what THIS backend freed
// (no shared host statfs to absorb another backend's freeing) and reconciles
// with the doctor/disk figures by construction.
func (r *Runtime) PruneCache(ctx context.Context, includeImages, dryRun bool, output io.Writer) (int64, error) {
	if dryRun {
		return r.pruneCacheDryRun(ctx, includeImages, output), nil
	}

	before := r.reclaimableBytes(ctx, includeImages)

	// Containers first (stopped only), and only yoloai's own (label-scoped, like
	// the volumes below). Removing stopped yoloai containers releases holds on
	// otherwise-unreferenced yoloai images. Scoping by com.yoloai.sandbox leaves
	// a foreign stopped container on a shared daemon untouched — plain prune is
	// not a daemon-wide `docker container prune` (DF137).
	if _, err := r.client.ContainersPrune(ctx, filters.NewArgs(filters.Arg("label", runtime.LabelSandbox))); err != nil {
		fmt.Fprintf(output, "%s: containers prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	// BuildKit cache: usually the biggest single category on a heavy-build
	// host, and the pin that keeps image layers alive on the containerd image
	// store. Prune it before images so the layers they share are actually
	// freed by containerd GC. Podman's docker-compat API has no build cache
	// endpoint and returns 404; that's expected, so swallow Not Found silently.
	if _, err := r.client.BuildCachePrune(ctx, build.CachePruneOptions{All: true}); err != nil && !cerrdefs.IsNotFound(err) {
		fmt.Fprintf(output, "%s: build cache prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	// Volumes: only yoloai's own (label-scoped). On Docker, all=true so named
	// yoloai volumes are removed, not just anonymous ones; Podman's docker-compat
	// API rejects the "all" volume filter ("invalid volume filter") and prunes
	// all unused volumes by default, so it's omitted there. Scoping by label
	// keeps the user's unrelated volumes (e.g. a project database) untouched and
	// out of the reclaim accounting (see splitCacheBytes).
	volFilter := filters.NewArgs(filters.Arg("label", managedLabel))
	if r.binaryName != "podman" {
		volFilter.Add("all", "true")
	}
	if _, err := r.client.VolumesPrune(ctx, volFilter); err != nil {
		fmt.Fprintf(output, "%s: volumes prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	// Networks: only yoloai's own (label-scoped, like the volumes above). yoloai
	// creates no Docker networks today, so this reclaims nothing now; scoping by
	// the managed label keeps it from removing a foreign unused network on a
	// shared daemon (DF137) and stays correct if yoloai ever creates a labelled one.
	if _, err := r.client.NetworksPrune(ctx, filters.NewArgs(filters.Arg("label", managedLabel))); err != nil {
		fmt.Fprintf(output, "%s: networks prune failed: %v\n", r.binaryName, err) //nolint:errcheck
	}

	// Images last, and only when asked. This is what forces a rebuild, so it's
	// gated behind includeImages. Dangling images from prior builds are already
	// removed by the core Prune; here we drop the reusable base/profile images
	// too — but only yoloai's own (label ∪ the deprecated name bridge), never a
	// foreign unused image on a shared daemon. That reaping was the DF137-class
	// defect the earlier unscoped ImagesPrune(dangling=false) carried.
	if includeImages {
		r.pruneManagedImages(ctx, output)
	}

	reclaimed := int64(0)
	if after := r.reclaimableBytes(ctx, includeImages); before >= 0 && after >= 0 && before > after {
		reclaimed = before - after
	}
	fmt.Fprintf(output, "%s: reclaimed %s\n", r.binaryName, runtime.FormatBytes(reclaimed)) //nolint:errcheck
	return reclaimed, nil
}

// reclaimableBytes returns this backend's currently reclaimable footprint as
// CacheUsage measures it: build cache + volumes always, plus image layers when
// includeImages. Returns -1 if usage can't be read. PruneCache samples this
// before and after pruning; the drop is the reclaim it reports (see PruneCache).
func (r *Runtime) reclaimableBytes(ctx context.Context, includeImages bool) int64 {
	u, err := r.CacheUsage(ctx)
	if err != nil {
		return -1
	}
	total := u.CachedBytes
	if includeImages {
		total += u.ImageBytes
	}
	return total
}

// pruneCacheDryRun reports what PruneCache would remove and returns an estimate
// of the reclaimable bytes (build cache + volumes, plus images when
// includeImages). The Docker API has no dry-run prune, so this reads current
// disk usage instead of removing anything.
func (r *Runtime) pruneCacheDryRun(ctx context.Context, includeImages bool, output io.Writer) int64 {
	du, err := r.client.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		fmt.Fprintf(output, "%s: could not estimate reclaimable cache: %v\n", r.binaryName, err) //nolint:errcheck
		return 0
	}
	cached, images := r.splitCacheBytes(du)
	what := "unused volumes, build cache"
	estimate := cached
	if includeImages {
		// The byte figure still counts every unused image's layers (the docker
		// disk-usage API cannot attribute shared layers per image), but the
		// sweep itself only removes yoloai's own — so on a shared daemon this
		// "~" estimate is an upper bound.
		what = "unused yoloai images, volumes, build cache"
		estimate += images
	}
	// This runs as the CLI's internal scan phase on every prune — not only under
	// --dry-run — so it must not frame itself as a dry-run mode the user opted
	// into. Print the per-backend breakdown only when there's something to
	// reclaim; the aggregate banner and "Nothing to prune" cover the rest.
	if estimate > 0 {
		fmt.Fprintf(output, "%s: would remove %s (~%s)\n", r.binaryName, what, runtime.FormatBytes(estimate)) //nolint:errcheck
	}
	if includeImages {
		r.warnImageReclaimBlockers(du, output)
	}
	return estimate
}

// imageReclaimBlocker is one container that pins image layers against
// ImagesPrune. PruneCache removes stopped containers (exited/dead) before
// ImagesPrune, so a blocker is any container in a state ContainersPrune won't
// touch (created, running, paused, restarting, removing).
type imageReclaimBlocker struct {
	Name  string
	State container.ContainerState
	Image string
}

// imageReclaimBlockers identifies containers that ContainersPrune won't remove
// and that will therefore pin their image layers when ImagesPrune runs.
// Returns nil when image reclaim is unblocked. The dry-run estimate counts
// du.LayersSize regardless of in-use status, so without this warning the user
// sees a multi-GB promise and a "reclaimed 0 B" result with no explanation.
func imageReclaimBlockers(du types.DiskUsage) []imageReclaimBlocker {
	var blockers []imageReclaimBlocker
	for _, c := range du.Containers {
		if c == nil {
			continue
		}
		switch c.State {
		case container.StateExited, container.StateDead:
			continue
		}
		// Only a container pinning a yoloai image blocks the scoped sweep; a
		// foreign container pinning its own image is no longer our concern.
		// Recognize ours by the pinned image's name or the container's own
		// sandbox label (which covers a sandbox running an untagged/re-tagged
		// image whose name check would miss).
		if !yoloaiImageName(c.Image) {
			if _, ok := c.Labels[runtime.LabelSandbox]; !ok {
				continue
			}
		}
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		blockers = append(blockers, imageReclaimBlocker{
			Name:  name,
			State: c.State,
			Image: c.Image,
		})
	}
	return blockers
}

// warnImageReclaimBlockers prints a per-backend warning naming the containers
// that will keep ImagesPrune from freeing the layers the dry-run estimate just
// promised. No-op when nothing is blocking.
func (r *Runtime) warnImageReclaimBlockers(du types.DiskUsage, output io.Writer) {
	blockers := imageReclaimBlockers(du)
	if len(blockers) == 0 {
		return
	}
	fmt.Fprintf(output, "%s: image reclaim is blocked by %d active container(s) — stop or destroy them to reclaim image layers:\n", r.binaryName, len(blockers)) //nolint:errcheck
	for _, b := range blockers {
		fmt.Fprintf(output, "%s:   %s (%s) holds %s\n", r.binaryName, b.Name, b.State, b.Image) //nolint:errcheck
	}
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
	cached, images := r.splitCacheBytes(du)
	detail := fmt.Sprintf("%d images, %d containers, %d volumes, %d build-cache entries",
		len(du.Images), len(du.Containers), len(du.Volumes), len(du.BuildCache))
	return runtime.CacheUsage{CachedBytes: cached, ImageBytes: images, Detail: detail}, nil
}

// imageBytesFunc computes the rebuild-forcing image-layer total from a
// DiskUsage snapshot. Injected via SetImageBytesFunc so Podman can override
// the default (du.LayersSize) that its docker-compat API reports as 0.
type imageBytesFunc func(types.DiskUsage) int64

// SetImageBytesFunc overrides how image-layer bytes are computed from a
// DiskUsage snapshot. Used by the Podman backend, whose docker-compat
// /system/df returns LayersSize=0; it injects a per-image dedup instead.
func (r *Runtime) SetImageBytesFunc(fn imageBytesFunc) { r.imageBytesFn = fn }

// splitCacheBytes returns (cachedBytes, imageBytes): the no-rebuild-forcing
// reclaim (build cache + container writable layers + volumes) and the
// rebuild-forcing reclaim (image layers). The image portion defaults to
// du.LayersSize — the deduplicated layer-store total that `docker system df`
// reports — NOT the sum of each img.Size, which multiply-counts shared base
// layers (dozens of intermediate build stages sharing one 5 GiB base would
// otherwise read as ~130 GiB). Podman injects a replacement via
// SetImageBytesFunc because its API reports LayersSize=0.
func (r *Runtime) splitCacheBytes(du types.DiskUsage) (cached, images int64) {
	if r.imageBytesFn != nil {
		images = r.imageBytesFn(du)
	} else {
		images = du.LayersSize
	}
	for _, ct := range du.Containers {
		if ct == nil {
			continue
		}
		// Only yoloai's own: PruneCache's ContainersPrune is label-scoped (DF137),
		// so counting a foreign stopped container's writable layer would promise a
		// reclaim plain prune no longer delivers.
		if _, ok := ct.Labels[runtime.LabelSandbox]; !ok {
			continue
		}
		// Only stopped containers are reclaimable: ContainersPrune (in PruneCache)
		// removes containers in the created/exited/dead states and leaves running,
		// paused, and restarting ones alone. Counting a live container's writable
		// layer promised a reclaim the prune could never deliver — the estimate
		// stayed put and every run reported "reclaimed 0 B" (the same in-use
		// mismatch the image-layer blocker warning covers for du.LayersSize).
		switch ct.State {
		case container.StateCreated, container.StateExited, container.StateDead:
			cached += ct.SizeRw
		}
	}
	for _, v := range du.Volumes {
		if _, ok := v.Labels[managedLabel]; !ok {
			continue // not yoloai's — don't count the user's own volumes
		}
		if v.UsageData != nil && v.UsageData.Size > 0 {
			cached += v.UsageData.Size
		}
	}
	for _, bc := range du.BuildCache {
		cached += bc.Size
	}
	return cached, images
}
