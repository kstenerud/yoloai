// ABOUTME: Disk-usage tiering for `system prune`: deduplicated image-layer
// ABOUTME: sizing (avoids multiply-counting shared base layers), which
// ABOUTME: container/volume bytes count as reclaimable cache, and image-reclaim
// ABOUTME: blockers.
package docker

import (
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/volume"
	"github.com/kstenerud/yoloai/runtime"
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
		Containers: []*container.Summary{yoloaiCtr(42, container.StateExited)},
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

// yoloaiCtr returns a yoloai-labeled container of the given writable-layer size
// and state — only such containers count toward the reclaimable cached tier,
// since PruneCache's ContainersPrune is label-scoped (DF137).
func yoloaiCtr(sizeRw int64, state container.ContainerState) *container.Summary {
	return &container.Summary{
		SizeRw: sizeRw,
		State:  state,
		Labels: map[string]string{runtime.LabelSandbox: "somebox"},
	}
}

// Containers' writable layers, yoloai volumes, and build cache live outside the
// image layer store — they're the no-rebuild "cached" tier, separate from images.
func TestSplitCacheBytes_NonImageUsageIsCachedTier(t *testing.T) {
	du := types.DiskUsage{
		LayersSize: 5 * gib,
		Containers: []*container.Summary{
			yoloaiCtr(100, container.StateExited),
			yoloaiCtr(200, container.StateDead),
		},
		Volumes:    []*volume.Volume{yoloaiVol(50)},
		BuildCache: []*build.CacheRecord{{Size: 25}},
	}
	cached, images := (&Runtime{}).splitCacheBytes(du)
	assert.Equal(t, int64(100+200+50+25), cached)
	assert.Equal(t, 5*gib, images)
}

// Regression: a running (or paused/restarting) container's writable layer must
// NOT count toward the reclaimable estimate — ContainersPrune leaves it alone,
// so counting it promised a reclaim the prune never delivered and every run
// reported "reclaimed 0 B" against an estimate that never dropped.
func TestSplitCacheBytes_ExcludesLiveContainers(t *testing.T) {
	du := types.DiskUsage{
		Containers: []*container.Summary{
			yoloaiCtr(1000, container.StateRunning),
			yoloaiCtr(2000, container.StatePaused),
			yoloaiCtr(3000, container.StateRestarting),
			yoloaiCtr(42, container.StateExited), // only this one is reclaimable
		},
	}
	cached, _ := (&Runtime{}).splitCacheBytes(du)
	assert.Equal(t, int64(42), cached)
}

// Regression: a foreign stopped container yoloai didn't create (no
// com.yoloai.sandbox label) must NOT count as reclaimable. PruneCache's
// ContainersPrune is label-scoped (DF137), so counting it would promise a
// reclaim plain prune never delivers — and plain prune must not touch a shared
// daemon's foreign containers at all.
func TestSplitCacheBytes_ExcludesUnlabeledContainers(t *testing.T) {
	du := types.DiskUsage{
		Containers: []*container.Summary{
			{SizeRw: 5000, State: container.StateExited},                                                                 // foreign — no yoloai label
			{Labels: map[string]string{"com.docker.compose.project": "foley"}, SizeRw: 6000, State: container.StateDead}, // someone else's label
			yoloaiCtr(42, container.StateExited),
		},
	}
	cached, _ := (&Runtime{}).splitCacheBytes(du)
	assert.Equal(t, int64(42), cached)
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
	sandboxLabels := map[string]string{runtime.LabelSandbox: "x"}
	du := types.DiskUsage{
		Containers: []*container.Summary{
			{Names: []string{"/run-1"}, State: container.StateRunning, Image: "yoloai-base"},
			{Names: []string{"/paused-1"}, State: container.StatePaused, Image: "yoloai-base"},
			{Names: []string{"/created-1"}, State: container.StateCreated, Image: "other", Labels: sandboxLabels},
			{Names: []string{"/restarting-1"}, State: container.StateRestarting, Image: "other", Labels: sandboxLabels},
			{Names: []string{"/removing-1"}, State: container.StateRemoving, Image: "other", Labels: sandboxLabels},
			{Names: []string{"/exited-1"}, State: container.StateExited, Image: "other", Labels: sandboxLabels},
			{Names: []string{"/dead-1"}, State: container.StateDead, Image: "other", Labels: sandboxLabels},
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

// The sweep is scoped to yoloai's images, so a foreign container pinning its
// own image is not a blocker — warning about it would tell the user to stop a
// container whose image the sweep would never touch anyway.
func TestImageReclaimBlockers_SkipsForeignContainers(t *testing.T) {
	du := types.DiskUsage{
		Containers: []*container.Summary{
			{Names: []string{"/postgres"}, State: container.StateRunning, Image: "postgres:16"},
			{Names: []string{"/registry-yoloai"}, State: container.StateRunning, Image: "ghcr.io/x/yoloai-base"},
		},
	}
	assert.Nil(t, imageReclaimBlockers(du))
}

// A sandbox running an untagged or re-tagged image would slip the name check;
// the container's own sandbox label still marks it as a blocker.
func TestImageReclaimBlockers_SandboxLabelBlocksRegardlessOfImageName(t *testing.T) {
	du := types.DiskUsage{
		Containers: []*container.Summary{
			{Names: []string{"/sb"}, State: container.StateRunning, Image: "sha256:abc",
				Labels: map[string]string{runtime.LabelSandbox: "x"}},
		},
	}
	got := imageReclaimBlockers(du)
	assert.Len(t, got, 1)
	assert.Equal(t, "sb", got[0].Name)
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

// The name bridge must match every name yoloai has ever produced (yoloai-base,
// principal-scoped and pre-D126 legacy profile tags) and nothing
// registry-qualified or merely yoloai-ish.
func TestYoloaiImageName(t *testing.T) {
	matches := []string{
		"yoloai-base", "yoloai-base:latest",
		"yoloai-cli-go-dev", "yoloai-cli-go-dev:latest",
		"yoloai-go-dev", // pre-D126 legacy profile tag
	}
	for _, ref := range matches {
		assert.True(t, yoloaiImageName(ref), "should match %q", ref)
	}
	nonMatches := []string{
		"yoloai",                  // no dash: not a name we ever emit
		"myyoloai-base",           // prefix must anchor at the start
		"ghcr.io/x/yoloai-base",   // registry-qualified is foreign
		"someuser/yoloai-base",    // repository-qualified is foreign
		"alpine:3.22", "postgres", // plainly foreign
		"", "<none>:<none>", // dangling placeholder
	}
	for _, ref := range nonMatches {
		assert.False(t, yoloaiImageName(ref), "should not match %q", ref)
	}
}

// Selection contract for the scoped --images sweep: an unused image is a
// candidate iff it carries the managed label OR (deprecated bridge) a
// bare-local yoloai- name; anything in use, and every foreign image, is
// spared. nameOnly marks bridge matches for the settling-period log line.
func TestManagedImageCandidates(t *testing.T) {
	managed := map[string]string{managedLabel: "true"}
	imgs := []image.Summary{
		{ID: "sha256:aaa", RepoTags: []string{"yoloai-base:latest"}, Labels: managed},
		{ID: "sha256:bbb", RepoTags: []string{"yoloai-cli-go:latest"}},               // pre-label: bridge match
		{ID: "sha256:ccc", RepoTags: []string{"my-agent:latest"}, Labels: managed},   // derived, renamed: label match
		{ID: "sha256:ddd", RepoTags: []string{"alpine:3.22"}},                        // foreign: spared
		{ID: "sha256:eee", RepoTags: []string{"yoloai-base:old"}, Labels: managed},   // in use: spared
		{ID: "sha256:fff", Labels: managed},                                          // labeled dangling: reclaimed
		{ID: "sha256:ggg", RepoTags: []string{"alpine:edge", "yoloai-retag:latest"}}, // foreign re-tagged with our name
	}
	inUse := map[string]bool{"sha256:eee": true}

	got := managedImageCandidates(imgs, inUse)

	byID := map[string]managedImageCandidate{}
	for _, c := range got {
		byID[c.id] = c
	}
	assert.Len(t, got, 5)
	assert.Contains(t, byID, "sha256:aaa")
	assert.False(t, byID["sha256:aaa"].nameOnly, "labeled image is not a bridge match")
	assert.Equal(t, "yoloai-base:latest", byID["sha256:aaa"].display)
	assert.Equal(t, []string{"sha256:aaa"}, byID["sha256:aaa"].removeRefs,
		"a labeled image is removed whole, by ID")
	assert.True(t, byID["sha256:bbb"].nameOnly, "unlabeled yoloai-named image rides the bridge")
	assert.Equal(t, []string{"yoloai-cli-go:latest"}, byID["sha256:bbb"].removeRefs,
		"a bridge match is removed by its yoloai tags, not by ID")
	assert.False(t, byID["sha256:ccc"].nameOnly)
	assert.Equal(t, "my-agent:latest", byID["sha256:ccc"].display,
		"a renamed derived image displays its own tag")
	assert.NotContains(t, byID, "sha256:ddd", "foreign unused image must be spared")
	assert.NotContains(t, byID, "sha256:eee", "in-use image must be spared")
	assert.Equal(t, "fff", byID["sha256:fff"].display, "untagged image displays its short ID")
	assert.Equal(t, []string{"yoloai-retag:latest"}, byID["sha256:ggg"].removeRefs,
		"only the yoloai tag is removed from a re-tagged foreign image; its own tag and the image survive")
}
