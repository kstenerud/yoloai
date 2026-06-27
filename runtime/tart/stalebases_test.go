//go:build !windows

// ABOUTME: Unit tests for stale-base detection (superseded Cirrus base repos)
// ABOUTME: and the opt-in PruneStaleBases removal, driven by the fake tart binary.

package tart

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	seqTag    = "ghcr.io/cirruslabs/macos-sequoia-base:latest"
	seqDigest = "ghcr.io/cirruslabs/macos-sequoia-base@sha256:bbbb"
	tahoeTag  = "ghcr.io/cirruslabs/macos-tahoe-base:latest" // == defaultBaseImage (current)
)

// staleInventory: provisioned VM + current (tahoe) base + a superseded (sequoia)
// base + noise (an -xcode flavor and an unrelated OCI image) that must be ignored.
func staleInventory() []fakeEntry {
	return []fakeEntry{
		{name: provisionedImageName, source: "local", sizeGB: 29},
		{name: tahoeTag, source: "OCI", sizeGB: 30},
		{name: baseImageRepo(tahoeTag) + "@sha256:aaaa", source: "OCI", sizeGB: 30},
		{name: seqTag, source: "OCI", sizeGB: 31},
		{name: seqDigest, source: "OCI", sizeGB: 31},
		{name: "ghcr.io/cirruslabs/macos-sonoma-xcode:16", source: "OCI", sizeGB: 50}, // -xcode flavor, not a base
		{name: "ghcr.io/other/tool:latest", source: "OCI", sizeGB: 7},                 // unrelated
	}
}

func TestStaleBaseImages_DetectsSupersededOnly(t *testing.T) {
	r, _ := fakeTartEntries(t, staleInventory())

	stale, err := r.staleBaseImages(context.Background())
	require.NoError(t, err)

	require.Len(t, stale, 1, "only the sequoia base is superseded; current/xcode/unrelated excluded")
	require.Equal(t, "ghcr.io/cirruslabs/macos-sequoia-base", stale[0].Repo)
	// Tag + digest are one on-disk copy: sized once (31 GB), not 62.
	require.Equal(t, int64(31)*bytesPerGB, stale[0].Bytes)
	require.ElementsMatch(t, []string{seqTag, seqDigest}, stale[0].Refs)
}

func TestCacheUsage_ReportsStaleBytes(t *testing.T) {
	r, _ := fakeTartEntries(t, staleInventory())

	u, err := r.CacheUsage(context.Background())
	require.NoError(t, err)

	// Current footprint: 29 (provisioned VM) + 30 (tahoe base, deduped) GB.
	require.Equal(t, int64(59)*bytesPerGB, u.ImageBytes)
	// Stale: the 31 GB sequoia base, reclaimable only via --stale-bases.
	require.Equal(t, int64(31)*bytesPerGB, u.StaleBytes)
}

func TestPruneStaleBases_RemovesSupersededOnly(t *testing.T) {
	r, deleteLog := fakeTartEntries(t, staleInventory())

	removed, reclaimed, err := r.PruneStaleBases(context.Background(), false /*dryRun*/, os.Stderr)
	require.NoError(t, err)
	require.Equal(t, []string{"ghcr.io/cirruslabs/macos-sequoia-base"}, removed)
	require.Equal(t, int64(31)*bytesPerGB, reclaimed)

	deleted := deletedNames(t, deleteLog)
	require.ElementsMatch(t, []string{seqTag, seqDigest}, deleted)
	// Current base, provisioned VM, and unrelated images are untouched.
	require.NotContains(t, deleted, tahoeTag)
	require.NotContains(t, deleted, provisionedImageName)
	require.NotContains(t, deleted, "ghcr.io/other/tool:latest")
}

func TestPruneStaleBases_DryRunDeletesNothing(t *testing.T) {
	r, deleteLog := fakeTartEntries(t, staleInventory())

	removed, reclaimed, err := r.PruneStaleBases(context.Background(), true /*dryRun*/, os.Stderr)
	require.NoError(t, err)
	require.Equal(t, []string{"ghcr.io/cirruslabs/macos-sequoia-base"}, removed)
	require.Equal(t, int64(31)*bytesPerGB, reclaimed)
	require.Empty(t, deletedNames(t, deleteLog), "dry run must not delete")
}

func TestPruneStaleBases_NoneWhenOnlyCurrentBase(t *testing.T) {
	r, deleteLog := fakeTartEntries(t, []fakeEntry{
		{name: provisionedImageName, source: "local", sizeGB: 29},
		{name: tahoeTag, source: "OCI", sizeGB: 30},
	})

	removed, reclaimed, err := r.PruneStaleBases(context.Background(), false, os.Stderr)
	require.NoError(t, err)
	require.Empty(t, removed)
	require.Zero(t, reclaimed)
	require.Empty(t, deletedNames(t, deleteLog))
}
