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
// ~5 GiB base read as ~130 GiB). cacheBytes must use the deduplicated
// LayersSize for the image portion and ignore per-image Size entirely.
func TestCacheBytes_UsesDeduplicatedLayersSize(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: 5 * gib,
		Images: []*image.Summary{
			{Size: 5 * gib},
			{Size: 5 * gib},
			{Size: 5 * gib},
		},
	}
	assert.Equal(t, 5*gib, cacheBytes(du))
}

// Containers' writable layers, volumes, and build cache live outside the image
// layer store, so they add on top of LayersSize.
func TestCacheBytes_AddsNonImageUsage(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: 5 * gib,
		Containers: []*container.Summary{{SizeRw: 100}, {SizeRw: 200}},
		Volumes:    []*volume.Volume{{UsageData: &volume.UsageData{Size: 50}}},
		BuildCache: []*build.CacheRecord{{Size: 25}},
	}
	assert.Equal(t, 5*gib+100+200+50+25, cacheBytes(du))
}

// A volume with unknown size (UsageData nil or -1) must not corrupt the total.
func TestCacheBytes_IgnoresUnknownVolumeSize(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: gib,
		Volumes: []*volume.Volume{
			{UsageData: nil},
			{UsageData: &volume.UsageData{Size: -1}},
			{UsageData: &volume.UsageData{Size: 500}},
		},
	}
	assert.Equal(t, gib+500, cacheBytes(du))
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
