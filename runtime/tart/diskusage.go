package tart

// ABOUTME: Reports tart's on-disk image footprint via `tart list` for the
// ABOUTME: yoloai-owned base images (provisioned VM + pulled OCI base).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
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

	var staleBytes int64
	for _, s := range staleBaseImagesFrom(entries, repo, r.protectedBaseRepos()...) {
		staleBytes += s.Bytes
	}

	detail := fmt.Sprintf("%d provisioned VM, %d OCI base image rows", localCount, ociCount)
	return runtime.CacheUsage{
		CachedBytes: 0,
		ImageBytes:  (localGB + ociGB) * bytesPerGB,
		StaleBytes:  staleBytes,
		Detail:      detail,
	}, nil
}

// baseImageFamilyPrefix and baseImageFamilySuffix bound the Cirrus macOS base
// repos yoloai pulls: ghcr.io/cirruslabs/macos-<codename>-base. The "-xcode"
// flavors and unrelated VMs are excluded, so a stale-base sweep only ever
// targets images yoloai itself could have created.
const (
	baseImageFamilyPrefix = "ghcr.io/cirruslabs/macos-"
	baseImageFamilySuffix = "-base"
)

// isBaseImageFamily reports whether a bare repo path is a Cirrus macOS base repo.
func isBaseImageFamily(repo string) bool {
	return strings.HasPrefix(repo, baseImageFamilyPrefix) &&
		strings.HasSuffix(repo, baseImageFamilySuffix)
}

// staleBaseImage is a superseded Cirrus base repo still on disk: the bare repo,
// its deduped on-disk size in bytes, and the tart names to delete (a pulled
// image is listed twice — by :latest tag and by @sha256 digest — and both must
// go or the remaining row pins the on-disk copy).
type staleBaseImage struct {
	Repo  string
	Bytes int64
	Refs  []string
}

// protectedBaseRepos returns any base repos that must not be swept as stale,
// even when they differ from the currently resolved base repo.
//
// When the user pins tart.image to a non-base image (e.g. an -xcode flavor),
// the override is built on top of the host-matched base. That base co-exists
// with the override and is still wanted — sweeping it as "superseded" would
// delete something the user needs. Only non-base overrides trigger this: if the
// override is itself a -base repo, it IS the current repo and is already
// excluded by staleBaseImagesFrom's currentRepo check.
func (r *Runtime) protectedBaseRepos() []string {
	if r.baseImageOverride == "" {
		return nil
	}
	if isBaseImageFamily(baseImageRepo(r.baseImageOverride)) {
		return nil
	}
	// Override is a non-base image (e.g. -xcode). The host-matched base must
	// survive the stale sweep.
	return []string{baseImageRepo(hostMatchedBaseImage(r.hostMajor))}
}

// staleBaseImages returns the Cirrus base repos on disk that differ from the
// currently resolved base — superseded bases left behind by a host-macOS (and
// thus codename) change. When a non-base tart.image override is active, the
// host-matched base is additionally protected (see protectedBaseRepos).
func (r *Runtime) staleBaseImages(ctx context.Context) ([]staleBaseImage, error) {
	entries, err := r.listEntries(ctx)
	if err != nil {
		return nil, err
	}
	return staleBaseImagesFrom(entries, baseImageRepo(r.resolveBaseImage("")), r.protectedBaseRepos()...), nil
}

// staleBaseImagesFrom is the pure core of staleBaseImages: given the tart-list
// rows, the current base repo, and any additionally protected repos (repos that
// must survive even though they differ from currentRepo), group every other
// Cirrus base repo's OCI rows (tag + digest) into one entry, sizing it once
// (max row, since both rows share a single on-disk copy — mirrors
// CacheUsage's dedup).
//
// protectedRepos is used when a non-base tart.image override is active: the
// host-matched base co-exists with the override image and must not be swept.
func staleBaseImagesFrom(entries []tartListEntry, currentRepo string, protectedRepos ...string) []staleBaseImage {
	protected := make(map[string]bool, len(protectedRepos))
	for _, p := range protectedRepos {
		protected[p] = true
	}
	byRepo := make(map[string]*staleBaseImage)
	var order []string
	for _, e := range entries {
		if e.Source != "OCI" {
			continue
		}
		repo := baseImageRepo(e.Name)
		if repo == currentRepo || !isBaseImageFamily(repo) || protected[repo] {
			continue
		}
		s, ok := byRepo[repo]
		if !ok {
			s = &staleBaseImage{Repo: repo}
			byRepo[repo] = s
			order = append(order, repo)
		}
		s.Refs = append(s.Refs, e.Name)
		if gb := e.Size * bytesPerGB; gb > s.Bytes {
			s.Bytes = gb
		}
	}
	out := make([]staleBaseImage, 0, len(order))
	for _, repo := range order {
		out = append(out, *byRepo[repo])
	}
	return out
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
