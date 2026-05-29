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

// Containers' writable layers, volumes, and build cache live outside the image
// layer store — they're the no-rebuild "cached" tier, separate from images.
func TestSplitCacheBytes_NonImageUsageIsCachedTier(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: 5 * gib,
		Containers: []*container.Summary{{SizeRw: 100}, {SizeRw: 200}},
		Volumes:    []*volume.Volume{{UsageData: &volume.UsageData{Size: 50}}},
		BuildCache: []*build.CacheRecord{{Size: 25}},
	}
	cached, images := (&Runtime{}).splitCacheBytes(du)
	assert.Equal(t, int64(100+200+50+25), cached)
	assert.Equal(t, 5*gib, images)
}

// A volume with unknown size (UsageData nil or -1) must not corrupt the total.
func TestSplitCacheBytes_IgnoresUnknownVolumeSize(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: gib,
		Volumes: []*volume.Volume{
			{UsageData: nil},
			{UsageData: &volume.UsageData{Size: -1}},
			{UsageData: &volume.UsageData{Size: 500}},
		},
	}
	cached, images := (&Runtime{}).splitCacheBytes(du)
	assert.Equal(t, int64(500), cached)
	assert.Equal(t, gib, images)
}

func TestFormatBytes_Bytes(t *testing.T) {
	assert.Equal(t, "500 B", formatBytes(500))
	assert.Equal(t, "0 B", formatBytes(0))
	assert.Equal(t, "1023 B", formatBytes(1023))
}

func TestFormatBytes_MB(t *testing.T) {
	assert.Equal(t, "1.0 MB", formatBytes(1024*1024))
	assert.Equal(t, "5.0 MB", formatBytes(5*1024*1024))
	assert.Equal(t, "1.5 MB", formatBytes(1024*1024+512*1024))
}

func TestFormatBytes_GB(t *testing.T) {
	assert.Equal(t, "1.00 GB", formatBytes(1024*1024*1024))
	assert.Equal(t, "2.50 GB", formatBytes(2*1024*1024*1024+512*1024*1024))
}
