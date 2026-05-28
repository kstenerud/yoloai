//go:build integration

// ABOUTME: Tart backend integration tests. Cheap tests run on every Apple
// ABOUTME: Silicon machine with tart installed; full VM-lifecycle tests are
// ABOUTME: opt-in via YOLOAI_TEST_TART_VM=1 because they clone a multi-GB base.

package tart

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTart_New_ReturnsRuntime(t *testing.T) {
	rt, _ := tartSetup(t)
	require.NotNil(t, rt)
	assert.NotEmpty(t, rt.tartBin, "should have located tart CLI")
	assert.NotEmpty(t, rt.layout.SandboxesDir(), "should have set sandbox base dir")
}

func TestTart_Descriptor_AdvertisesVMCapabilities(t *testing.T) {
	rt, _ := tartSetup(t)
	desc := rt.Descriptor()
	assert.Equal(t, "tart", string(desc.Name))
	assert.False(t, desc.Capabilities.HostFilesystem,
		"tart runs the agent inside a VM, not on the host filesystem")
	assert.False(t, desc.Capabilities.ContainerAttach,
		"tart has no docker-compatible container surface; VS Code Attach should be false")
	assert.False(t, desc.Capabilities.OverlayDirs,
		"tart does not support :overlay (no overlayfs in macOS VMs)")
}

func TestTart_InspectNotFound(t *testing.T) {
	rt, ctx := tartSetup(t)
	info, err := rt.Inspect(ctx, "does-not-exist-"+t.Name())
	// Contract: Inspect on a nonexistent VM should either return an error
	// or an InstanceInfo with Running=false. Either is acceptable; the
	// caller checks Running, not whether the inspect succeeded.
	if err == nil {
		assert.False(t, info.Running,
			"a nonexistent VM cannot be running")
	}
}

func TestTart_RemoveIdempotent_NonexistentVM(t *testing.T) {
	rt, ctx := tartSetup(t)
	// Remove on a never-created VM should not error. Tart's "delete" is
	// idempotent enough that the runtime should not surface a failure
	// when the target wasn't there to begin with.
	err := rt.Remove(ctx, "never-existed-"+t.Name())
	assert.NoError(t, err, "Remove on nonexistent VM should be idempotent")
}

// TestTart_FullVMLifecycle is the heavyweight smoke test: clones the base
// image, creates a VM, inspects it, removes it. Gated behind
// YOLOAI_TEST_TART_VM=1 because:
//
//   - Clone takes minutes and consumes multi-GB of disk.
//   - It requires the user has run `yoloai build` to produce the base image.
//   - The current Tart runtime has a known issue with :copy workdir symlinks
//     for temp directories (see docs/dev/plans/TODO.md §Tart Runtime).
//
// On an Apple Silicon machine with the base image present, run with:
//
//	YOLOAI_TEST_TART_VM=1 go test -tags=integration -timeout=20m \
//	    -run TestTart_FullVMLifecycle ./runtime/tart/
func TestTart_FullVMLifecycle(t *testing.T) {
	if os.Getenv("YOLOAI_TEST_TART_VM") != "1" {
		t.Skip("skipping Tart full-VM lifecycle (set YOLOAI_TEST_TART_VM=1 to enable)")
	}
	// Placeholder — the symlink TODO needs to land first. Once fixed,
	// this body should mirror TestSeatbelt_CreateInspectRemove: build an
	// InstanceConfig, call Create, verify the VM exists in `tart list`,
	// call Remove, verify it's gone.
	t.Skip("Tart full lifecycle test pending docs/dev/plans/TODO.md §Tart Runtime fix")
}
