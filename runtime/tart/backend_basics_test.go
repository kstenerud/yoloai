// ABOUTME: VM-free tart backend basics (constructor, descriptor, inspect/remove
// ABOUTME: on nonexistent VMs) against the real tart CLI. Untagged so every
// ABOUTME: macOS `make check` runs them; they skip off macOS and never clone or
// ABOUTME: boot a VM.

package tart

import (
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
	assert.Equal(t, "tart", string(desc.Type))
	assert.False(t, desc.Capabilities.HostFilesystem,
		"tart runs the agent inside a VM, not on the host filesystem")
	assert.False(t, desc.Capabilities.ContainerAttach,
		"tart has no docker-compatible container surface; VS Code Attach should be false")
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
