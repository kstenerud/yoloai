package docker

import (
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/volume"
	"github.com/stretchr/testify/assert"
)

const gib = int64(1024 * 1024 * 1024)

// Regression: many intermediate build stages share one base image, so summing
// each img.Size multiply-counts the shared layers (a real podman cache of one
// ~5 GiB base read as ~130 GiB). The image tier must use the deduplicated
// LayersSize and ignore per-image Size entirely.
func TestSplitCacheBytes_ImagesUseDeduplicatedLayersSize(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: 5 * gib,
		Images: []*image.Summary{
			{Size: 5 * gib},
			{Size: 5 * gib},
			{Size: 5 * gib},
		},
	}
	cached, images := (&Runtime{}).splitCacheBytes(du)
	assert.Equal(t, int64(0), cached)
	assert.Equal(t, 5*gib, images)
}

// When an imageBytesFn is injected (the Podman path, whose API reports
// LayersSize=0), it replaces du.LayersSize for the image tier; the cached tier
// is unaffected.
func TestSplitCacheBytes_ImageBytesFuncOverride(t *testing.T) {
	r := &Runtime{}
	r.SetImageBytesFunc(func(types.DiskUsage) int64 { return 7 * gib })
	du := types.DiskUsage{
		LayersSize: 0, // would be the image total without the override
		Containers: []*container.Summary{{SizeRw: 42}},
	}
	cached, images := r.splitCacheBytes(du)
	assert.Equal(t, int64(42), cached)
	assert.Equal(t, 7*gib, images)
}

// yoloaiVol returns a yoloai-labeled volume of the given size — only such
// volumes count toward the reclaimable cached tier.
func yoloaiVol(size int64) *volume.Volume {
	return &volume.Volume{
		Labels:    map[string]string{managedLabel: "true"},
		UsageData: &volume.UsageData{Size: size},
	}
}

// Containers' writable layers, yoloai volumes, and build cache live outside the
// image layer store — they're the no-rebuild "cached" tier, separate from images.
func TestSplitCacheBytes_NonImageUsageIsCachedTier(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: 5 * gib,
		Containers: []*container.Summary{{SizeRw: 100}, {SizeRw: 200}},
		Volumes:    []*volume.Volume{yoloaiVol(50)},
		BuildCache: []*build.CacheRecord{{Size: 25}},
	}
	cached, images := (&Runtime{}).splitCacheBytes(du)
	assert.Equal(t, int64(100+200+50+25), cached)
	assert.Equal(t, 5*gib, images)
}

// Regression: volumes yoloai didn't create (no managedLabel) — e.g. a user's
// project database — must NOT be counted as reclaimable. Counting them
// over-promised a reclaim that the (label-scoped) prune never delivered, and
// risked listing the user's data as "no longer needed".
func TestSplitCacheBytes_ExcludesUnlabeledVolumes(t *testing.T) {
	du := types.DiskUsage{
		Volumes: []*volume.Volume{
			{UsageData: &volume.UsageData{Size: 500 * 1024 * 1024}},                                                     // user's postgres DB — no label
			{Labels: map[string]string{"com.docker.compose.project": "foley"}, UsageData: &volume.UsageData{Size: 100}}, // someone else's label
			yoloaiVol(42),
		},
	}
	cached, _ := (&Runtime{}).splitCacheBytes(du)
	assert.Equal(t, int64(42), cached)
}

// A yoloai volume with unknown size (UsageData nil or -1) must not corrupt the total.
func TestSplitCacheBytes_IgnoresUnknownVolumeSize(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: gib,
		Volumes: []*volume.Volume{
			{Labels: map[string]string{managedLabel: "true"}, UsageData: nil},
			{Labels: map[string]string{managedLabel: "true"}, UsageData: &volume.UsageData{Size: -1}},
			yoloaiVol(500),
		},
	}
	cached, images := (&Runtime{}).splitCacheBytes(du)
	assert.Equal(t, int64(500), cached)
	assert.Equal(t, gib, images)
}

// PruneCache removes stopped containers (exited/dead) before ImagesPrune, so
// those don't block image reclaim. Every other state pins the image.
func TestImageReclaimBlockers_FlagsNonRemovableStates(t *testing.T) {
	du := types.DiskUsage{
		Containers: []*container.Summary{
			{Names: []string{"/run-1"}, State: container.StateRunning, Image: "yoloai-base"},
			{Names: []string{"/paused-1"}, State: container.StatePaused, Image: "yoloai-base"},
			{Names: []string{"/created-1"}, State: container.StateCreated, Image: "other"},
			{Names: []string{"/restarting-1"}, State: container.StateRestarting, Image: "other"},
			{Names: []string{"/removing-1"}, State: container.StateRemoving, Image: "other"},
			{Names: []string{"/exited-1"}, State: container.StateExited, Image: "other"},
			{Names: []string{"/dead-1"}, State: container.StateDead, Image: "other"},
		},
	}
	got := imageReclaimBlockers(du)
	assert.Len(t, got, 5)
	names := make([]string, 0, len(got))
	for _, b := range got {
		names = append(names, b.Name)
	}
	assert.ElementsMatch(t, []string{"run-1", "paused-1", "created-1", "restarting-1", "removing-1"}, names)
}

// Container names from the docker API carry a leading "/" that must be trimmed
// before display so the warning reads naturally.
func TestImageReclaimBlockers_StripsLeadingSlashFromName(t *testing.T) {
	du := types.DiskUsage{
		Containers: []*container.Summary{
			{Names: []string{"/yoloai-x"}, State: container.StateRunning, Image: "yoloai-base"},
		},
	}
	got := imageReclaimBlockers(du)
	assert.Len(t, got, 1)
	assert.Equal(t, "yoloai-x", got[0].Name)
	assert.Equal(t, "yoloai-base", got[0].Image)
}

// When everything is stopped, ImagesPrune will be free to remove image layers,
// so the warning must stay silent.
func TestImageReclaimBlockers_EmptyWhenAllStopped(t *testing.T) {
	du := types.DiskUsage{
		Containers: []*container.Summary{
			{Names: []string{"/exited-1"}, State: container.StateExited, Image: "yoloai-base"},
			{Names: []string{"/dead-1"}, State: container.StateDead, Image: "yoloai-base"},
		},
	}
	assert.Nil(t, imageReclaimBlockers(du))
}
