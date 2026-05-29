package tart

// ABOUTME: Reports tart's on-disk image footprint via `tart list` for the
// ABOUTME: yoloai-owned base images (provisioned VM + pulled OCI base).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// Compile-time check: tart reports image disk usage.
var _ runtime.DiskUsageReporter = (*Runtime)(nil)

// bytesPerGB converts tart's `list` Size field (whole GB, decimal) to bytes.
// tart reports the disk-image footprint rounded to the nearest GB, so this
// figure is coarse (±~0.5 GB per image) — accurate enough for a "should I
// prune?" signal, not a byte-exact accounting.
const bytesPerGB = 1_000_000_000

// tartListEntry is one row of `tart list --format json`. Size is the on-disk
// footprint in whole GB; Source is "local" (cloned/provisioned VMs) or "OCI"
// (pulled registry images).
type tartListEntry struct {
	Name   string `json:"Name"`
	Source string `json:"Source"`
	Size   int64  `json:"Size"`
}

// listEntries returns every VM and OCI image tart tracks.
func (r *Runtime) listEntries(ctx context.Context) ([]tartListEntry, error) {
	out, err := r.runTart(ctx, "list", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("list VMs: %w", err)
	}
	var entries []tartListEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		return nil, fmt.Errorf("parse tart list: %w", err)
	}
	return entries, nil
}

// CacheUsage reports the disk footprint of the yoloai-owned base images: the
// provisioned local VM (yoloai-base) plus the pulled OCI base image. Both are
// rebuild-forcing, so the whole figure lands in ImageBytes; tart has no
// no-rebuild cache, so CachedBytes is always 0.
//
// Scope is deliberately yoloai's base images, NOT every VM tart tracks (unlike
// docker/podman, which report the whole daemon store). tart is the user's
// general-purpose VM tool — counting unrelated personal VMs as "reclaimable"
// would imply `prune --images` deletes them, which it never does. This keeps
// the IMAGES column reconcilable with what prune actually frees.
//
// Live sandbox clones (yoloai-<name>) are excluded for the same reason: they
// are instances removed by Remove / the orphan Prune sweep, not by prune
// --images.
func (r *Runtime) CacheUsage(ctx context.Context) (runtime.CacheUsage, error) {
	entries, err := r.listEntries(ctx)
	if err != nil {
		return runtime.CacheUsage{CachedBytes: 0, ImageBytes: -1}, err
	}

	repo := baseImageRepo(r.resolveBaseImage(""))
	var localGB, ociGB int64
	var localCount, ociCount int
	for _, e := range entries {
		switch {
		case e.Name == provisionedImageName && e.Source == "local":
			localGB += e.Size
			localCount++
		case e.Source == "OCI" && baseImageRepo(e.Name) == repo:
			// tart lists one pulled image twice — once by tag (:latest) and
			// once by digest (@sha256:…) — but both reference a single on-disk
			// copy. Count the repo once (max), never per-row.
			if e.Size > ociGB {
				ociGB = e.Size
			}
			ociCount++
		}
	}

	detail := fmt.Sprintf("%d provisioned VM, %d OCI base image rows", localCount, ociCount)
	return runtime.CacheUsage{
		CachedBytes: 0,
		ImageBytes:  (localGB + ociGB) * bytesPerGB,
		Detail:      detail,
	}, nil
}

// ownedImageBytes returns the rebuild-forcing footprint CacheUsage measures, or
// -1 if tart can't be queried. PruneCache samples this before/after to report
// reclaim as a self-attributed before−after delta (mirrors docker/podman, D37).
func (r *Runtime) ownedImageBytes(ctx context.Context) int64 {
	u, err := r.CacheUsage(ctx)
	if err != nil {
		return -1
	}
	return u.ImageBytes
}

// ownedImageRefs returns the tart names PruneCache must delete to reclaim the
// base-image footprint: the provisioned local VM plus every OCI row for the
// base repo (both the :latest tag and the @sha256 digest — deleting only the
// tag leaves the digest row pinning the on-disk copy, so nothing frees).
func (r *Runtime) ownedImageRefs(ctx context.Context) []string {
	entries, err := r.listEntries(ctx)
	if err != nil {
		return nil
	}
	repo := baseImageRepo(r.resolveBaseImage(""))
	var refs []string
	for _, e := range entries {
		switch {
		case e.Name == provisionedImageName && e.Source == "local":
			refs = append(refs, e.Name)
		case e.Source == "OCI" && baseImageRepo(e.Name) == repo:
			refs = append(refs, e.Name)
		}
	}
	return refs
}

// baseImageRepo strips the tag and/or digest from an OCI reference, leaving the
// bare repository path. "ghcr.io/cirruslabs/macos-sequoia-base:latest" and
// "ghcr.io/cirruslabs/macos-sequoia-base@sha256:…" both reduce to
// "ghcr.io/cirruslabs/macos-sequoia-base". Plain VM names pass through.
func baseImageRepo(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	// A tag is a trailing ":<tag>" whose value has no "/" (so a registry
	// host:port prefix isn't mistaken for a tag).
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i+1:], "/") {
		ref = ref[:i]
	}
	return ref
}
