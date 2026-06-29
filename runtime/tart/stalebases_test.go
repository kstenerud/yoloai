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

// DF24: When tart.image is pinned to a non-base image (e.g. an -xcode flavor),
// the host-matched -base image co-exists with the override and must NOT be
// swept as superseded. Only bases from different codenames are flagged.
func TestStaleBaseImages_NonBaseOverrideProtectsHostMatchedBase(t *testing.T) {
	const (
		xcodeOverride = "ghcr.io/cirruslabs/macos-tahoe-xcode:26.5"
		tahoeBaseTag  = "ghcr.io/cirruslabs/macos-tahoe-base:latest"
		tahoeDigest   = "ghcr.io/cirruslabs/macos-tahoe-base@sha256:aaaa"
		sonoTag       = "ghcr.io/cirruslabs/macos-sonoma-base:latest"
		sonoDigest    = "ghcr.io/cirruslabs/macos-sonoma-base@sha256:cccc"
	)
	entries := []fakeEntry{
		{name: provisionedImageName, source: "local", sizeGB: 29},
		{name: tahoeBaseTag, source: "OCI", sizeGB: 30},
		{name: tahoeDigest, source: "OCI", sizeGB: 30},
		{name: sonoTag, source: "OCI", sizeGB: 31},
		{name: sonoDigest, source: "OCI", sizeGB: 31},
	}
	// fakeTartEntries sets hostMajor=26 (tahoe), so host-matched base = macos-tahoe-base.
	r, _ := fakeTartEntries(t, entries)
	r.baseImageOverride = xcodeOverride // non-base override pinned in config

	stale, err := r.staleBaseImages(context.Background())
	require.NoError(t, err)

	var repos []string
	for _, s := range stale {
		repos = append(repos, s.Repo)
	}
	require.NotContains(t, repos, "ghcr.io/cirruslabs/macos-tahoe-base",
		"host-matched base must be protected when tart.image is a non-base override")
	require.Contains(t, repos, "ghcr.io/cirruslabs/macos-sonoma-base",
		"older base from a different codename is still superseded")
}

// No-override behavior is unchanged: the current (host-matched) base is not
// stale; a base from a different codename is.
func TestStaleBaseImages_NoOverrideCurrentBaseNotStale(t *testing.T) {
	const (
		tahoeBaseTag    = "ghcr.io/cirruslabs/macos-tahoe-base:latest"
		tahoeBaseDigest = "ghcr.io/cirruslabs/macos-tahoe-base@sha256:aaaa"
		sonoTag2        = "ghcr.io/cirruslabs/macos-sonoma-base:latest"
		sonoDigest2     = "ghcr.io/cirruslabs/macos-sonoma-base@sha256:cccc"
	)
	entries := []fakeEntry{
		{name: provisionedImageName, source: "local", sizeGB: 29},
		{name: tahoeBaseTag, source: "OCI", sizeGB: 30},
		{name: tahoeBaseDigest, source: "OCI", sizeGB: 30},
		{name: sonoTag2, source: "OCI", sizeGB: 31},
		{name: sonoDigest2, source: "OCI", sizeGB: 31},
	}
	// No override — hostMajor=26 resolves to tahoe, which is the current base.
	r, _ := fakeTartEntries(t, entries)

	stale, err := r.staleBaseImages(context.Background())
	require.NoError(t, err)

	require.Len(t, stale, 1, "only the sonoma base is superseded")
	require.Equal(t, "ghcr.io/cirruslabs/macos-sonoma-base", stale[0].Repo)
}

// When tart.image is pinned to a -base image, the protection logic is not
// triggered: the override IS the current repo. Any other base (including the
// host-matched one) is flagged as stale via the normal codename-mismatch check.
func TestStaleBaseImages_BaseOverrideNoExtraProtection(t *testing.T) {
	const sonomaOverride = "ghcr.io/cirruslabs/macos-sonoma-base:latest"
	tahoeBaseTag2 := "ghcr.io/cirruslabs/macos-tahoe-base:latest"
	tahoeDigest2 := "ghcr.io/cirruslabs/macos-tahoe-base@sha256:aaaa"
	entries := []fakeEntry{
		{name: provisionedImageName, source: "local", sizeGB: 29},
		{name: sonomaOverride, source: "OCI", sizeGB: 30},
		{name: baseImageRepo(sonomaOverride) + "@sha256:dddd", source: "OCI", sizeGB: 30},
		{name: tahoeBaseTag2, source: "OCI", sizeGB: 31},
		{name: tahoeDigest2, source: "OCI", sizeGB: 31},
	}
	r, _ := fakeTartEntries(t, entries)
	r.baseImageOverride = sonomaOverride // -base override: no extra protection triggered

	stale, err := r.staleBaseImages(context.Background())
	require.NoError(t, err)

	require.Len(t, stale, 1,
		"when override is itself a -base image, the other base is flagged normally")
	require.Equal(t, "ghcr.io/cirruslabs/macos-tahoe-base", stale[0].Repo)
}
