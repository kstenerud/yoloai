//go:build !windows

// ABOUTME: Unit tests for tart CacheUsage / image-byte accounting and the
// ABOUTME: OCI tag+digest dedup, driven by the fake tart binary.

package tart

import (
	"context"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaseImageRepo(t *testing.T) {
	repo := "ghcr.io/cirruslabs/macos-sequoia-base"
	require.Equal(t, repo, baseImageRepo(repo+":latest"))
	require.Equal(t, repo, baseImageRepo(repo+"@sha256:cae08899"))
	require.Equal(t, repo, baseImageRepo(repo)) // already bare
	require.Equal(t, "yoloai-base", baseImageRepo("yoloai-base"))
}

// baseImg returns OCI rows for the default base repo (tag + digest), which tart
// lists as two entries backed by a single on-disk copy.
func baseImg(sizeGB int) []fakeEntry {
	return []fakeEntry{
		{name: defaultBaseImage, source: "OCI", sizeGB: sizeGB},
		{name: baseImageRepo(defaultBaseImage) + "@sha256:cae08899", source: "OCI", sizeGB: sizeGB},
	}
}

func TestCacheUsageCountsOwnedImagesDedupingOCI(t *testing.T) {
	entries := append([]fakeEntry{
		{name: provisionedImageName, source: "local", sizeGB: 29},
		{name: "yoloai-mybox", source: "local", sizeGB: 40},           // live clone — excluded
		{name: "ghcr.io/other/tool:latest", source: "OCI", sizeGB: 7}, // unrelated — excluded
	}, baseImg(31)...)
	r, _ := fakeTartEntries(t, entries)

	u, err := r.CacheUsage(context.Background())
	require.NoError(t, err)

	// 29 (provisioned VM) + 31 (base OCI counted once, not 31+31) GB.
	require.Equal(t, int64(60)*bytesPerGB, u.ImageBytes)
	require.Equal(t, int64(0), u.CachedBytes)
}

// PruneCache reclaim is the before−after drop in CacheUsage; the fake removes
// deleted rows so the after-sample reflects the deletion.
func TestPruneCacheReportsReclaimDelta(t *testing.T) {
	entries := append([]fakeEntry{
		{name: provisionedImageName, source: "local", sizeGB: 29},
		{name: "ghcr.io/other/tool:latest", source: "OCI", sizeGB: 7}, // not ours — survives
	}, baseImg(31)...)
	r, deleteLog := fakeTartEntries(t, entries)

	cache, err := r.PruneCache(context.Background(), true /*includeImages*/, false /*dryRun*/)
	require.NoError(t, err)
	require.Equal(t, int64(60)*bytesPerGB, cache.BytesReclaimed)

	deleted := deletedNames(t, deleteLog)
	require.Contains(t, deleted, provisionedImageName)
	require.Contains(t, deleted, defaultBaseImage)
	require.NotContains(t, deleted, "ghcr.io/other/tool:latest")

	// After the prune only the unrelated OCI image remains; yoloai's footprint is 0.
	u, err := r.CacheUsage(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), u.ImageBytes)
}

func TestPruneCacheDryRunReturnsEstimate(t *testing.T) {
	entries := append([]fakeEntry{
		{name: provisionedImageName, source: "local", sizeGB: 29},
	}, baseImg(31)...)
	r, deleteLog := fakeTartEntries(t, entries)

	cache, err := r.PruneCache(context.Background(), true /*includeImages*/, true /*dryRun*/)
	require.NoError(t, err)
	require.Equal(t, int64(60)*bytesPerGB, cache.BytesReclaimed)
	require.Empty(t, deletedNames(t, deleteLog)) // nothing actually removed
	// A dry run names what it would remove rather than only totalling it, so a
	// caller can show the list without re-deriving it.
	for _, item := range cache.Items {
		assert.Equal(t, runtime.PruneActionWouldRemove, item.Action)
	}
	assert.Len(t, cache.Items, len(r.ownedImageRefs(context.Background())))
}
