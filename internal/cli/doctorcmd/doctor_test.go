// ABOUTME: Tests for the doctor advisory render helpers and JSON builder — the
// ABOUTME: pure projections of a dry-run PruneResult + DiskUsage into output.

package doctorcmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai"
	"github.com/stretchr/testify/assert"
)

func samplePrune() *yoloai.PruneResult {
	return &yoloai.PruneResult{
		RemovedItems: []yoloai.PruneItem{
			{Backend: "docker", Kind: yoloai.PruneItemKind("container"), Name: "yoloai-old"},
			{Kind: yoloai.PruneKindLockFile, Name: "stale"},
			{Kind: yoloai.PruneKindSandboxDir, Name: "neverinit"},
		},
		RefusedDataBearing: []yoloai.RefusedSandbox{
			{Name: "dirty", Path: "/sb/dirty", Detail: "overlay upper has changes"},
		},
		TrashContents: yoloai.TrashSummary{Count: 2, Bytes: 4096},
	}
}

func TestRenderReclaimableNow(t *testing.T) {
	var b bytes.Buffer
	renderReclaimableNow(&b, samplePrune())
	out := b.String()
	assert.Contains(t, out, "Reclaimable now:")
	assert.Contains(t, out, "docker/container: yoloai-old")
	assert.Contains(t, out, "lock_file: stale")
	assert.Contains(t, out, "sandbox_dir: neverinit")
	assert.Contains(t, out, "yoloai system prune")
}

func TestRenderReclaimableNow_EmptyIsSilent(t *testing.T) {
	var b bytes.Buffer
	renderReclaimableNow(&b, &yoloai.PruneResult{})
	assert.Empty(t, b.String())
	renderReclaimableNow(&b, nil)
	assert.Empty(t, b.String())
}

func TestRenderReclaimableSpace(t *testing.T) {
	var b bytes.Buffer
	renderReclaimableSpace(&b, &yoloai.DiskUsage{
		PerBackend: []yoloai.BackendDiskUsage{
			{Name: "docker", CachedBytes: 2 << 30, ImageBytes: 1 << 30},
			{Name: "tart", CachedBytes: 0, ImageBytes: 0},    // skipped: nothing in either tier
			{Name: "podman", CachedBytes: 0, ImageBytes: -1}, // skipped: unknown image sentinel, no cache
			{Name: "seatbelt", Err: assert.AnError},          // skipped: error
		},
	})
	out := b.String()
	// Both tiers should render with their distinct headers and commands.
	assert.Contains(t, out, "Reclaimable cached data that's no longer needed:")
	assert.Contains(t, out, "Reclaimable images (these will need to be regenerated to use yoloAI):")
	assert.Contains(t, out, "docker:")
	assert.NotContains(t, out, "tart:")
	assert.NotContains(t, out, "podman:")
	assert.NotContains(t, out, "seatbelt:")
	// The unknown sentinel must never surface as a literal negative size.
	assert.NotContains(t, out, "-1 B")
	assert.Contains(t, out, "yoloai system prune\n")
	assert.Contains(t, out, "yoloai system prune --images")
}

func TestRenderReclaimableSpace_OnlyCacheTier(t *testing.T) {
	var b bytes.Buffer
	renderReclaimableSpace(&b, &yoloai.DiskUsage{
		PerBackend: []yoloai.BackendDiskUsage{
			{Name: "docker", CachedBytes: 1 << 30, ImageBytes: 0},
		},
	})
	out := b.String()
	assert.Contains(t, out, "Reclaimable cached data that's no longer needed:")
	assert.Contains(t, out, "yoloai system prune")
	// No image tier when nothing image-reclaimable.
	assert.NotContains(t, out, "Reclaimable images")
	assert.NotContains(t, out, "--images")
}

func TestRenderReclaimableSpace_AllZeroIsSilent(t *testing.T) {
	var b bytes.Buffer
	renderReclaimableSpace(&b, &yoloai.DiskUsage{
		PerBackend: []yoloai.BackendDiskUsage{{Name: "docker", CachedBytes: 0, ImageBytes: 0}},
	})
	assert.Empty(t, b.String())
}

func TestRenderUnreviewedWork(t *testing.T) {
	var b bytes.Buffer
	renderUnreviewedWork(&b, samplePrune())
	out := b.String()
	assert.Contains(t, out, "dirty — overlay upper has changes")
	assert.Contains(t, out, "yoloai diff dirty")
	assert.Contains(t, out, "yoloai destroy dirty")
}

func TestRenderTrash(t *testing.T) {
	var b bytes.Buffer
	renderTrash(&b, samplePrune())
	out := b.String()
	assert.Contains(t, out, "Trash holds 2 item(s)")
	assert.Contains(t, out, "Recover with mv")
}

func TestBuildDoctorJSON(t *testing.T) {
	rep := buildDoctorJSON(nil, samplePrune(), &yoloai.DiskUsage{
		PerBackend: []yoloai.BackendDiskUsage{{Name: "docker", CachedBytes: 2048, ImageBytes: 4096}},
	}, nil)
	assert.Len(t, rep.ReclaimableNow, 3)
	assert.Len(t, rep.ReclaimableSpace, 1)
	assert.Len(t, rep.UnreviewedWork, 1)
	assert.Equal(t, 2, rep.Trash.Count)
	assert.Equal(t, int64(4096), rep.Trash.Bytes)
	assert.Equal(t, "docker", rep.ReclaimableSpace[0].Backend)
	assert.Equal(t, int64(2048), rep.ReclaimableSpace[0].CachedBytes)
	assert.Equal(t, int64(4096), rep.ReclaimableSpace[0].ImageBytes)
}

func TestBuildDoctorJSON_NilProbesYieldEmptySlices(t *testing.T) {
	rep := buildDoctorJSON(nil, nil, nil, nil)
	// Non-nil empty slices so the JSON document carries [] rather than null.
	assert.NotNil(t, rep.ReclaimableNow)
	assert.NotNil(t, rep.ReclaimableSpace)
	assert.NotNil(t, rep.UnreviewedWork)
	assert.Empty(t, rep.ReclaimableNow)
	assert.Zero(t, rep.Trash.Count)
}

func TestRenderReclaimableNow_CapsPreview(t *testing.T) {
	var items []yoloai.PruneItem
	for i := 0; i < 15; i++ {
		items = append(items, yoloai.PruneItem{Kind: yoloai.PruneKindTempDir, Name: "t"})
	}
	var b bytes.Buffer
	renderReclaimableNow(&b, &yoloai.PruneResult{RemovedItems: items})
	out := b.String()
	assert.Contains(t, out, "... and 5 more")
	assert.Equal(t, reclaimPreviewMax, strings.Count(out, "temp_dir: t"))
}
