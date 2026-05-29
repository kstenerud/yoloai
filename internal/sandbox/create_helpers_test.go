package sandbox

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
	dockerrt "github.com/kstenerud/yoloai/internal/runtime/docker"
	_ "github.com/kstenerud/yoloai/internal/runtime/seatbelt" // backend init() registers the descriptor for tests
	tartrt "github.com/kstenerud/yoloai/internal/runtime/tart"
	"github.com/kstenerud/yoloai/internal/sandbox/create"
)

// IsolationSnapshotter tests

func TestIsolationSnapshotter_VmEnhanced(t *testing.T) {
	assert.Equal(t, "devmapper", runtime.IsolationSnapshotter("vm-enhanced"))
}

func TestIsolationSnapshotter_Other(t *testing.T) {
	assert.Equal(t, "", runtime.IsolationSnapshotter("vm"))
	assert.Equal(t, "", runtime.IsolationSnapshotter("container"))
	assert.Equal(t, "", runtime.IsolationSnapshotter("container-enhanced"))
	assert.Equal(t, "", runtime.IsolationSnapshotter(""))
}

// IsolationContainerRuntime tests

func TestIsolationContainerRuntime_Container(t *testing.T) {
	assert.Equal(t, "", runtime.IsolationContainerRuntime("container"))
	assert.Equal(t, "", runtime.IsolationContainerRuntime(""))
}

func TestIsolationContainerRuntime_ContainerEnhanced(t *testing.T) {
	assert.Equal(t, "runsc", runtime.IsolationContainerRuntime("container-enhanced"))
}

func TestIsolationContainerRuntime_VM(t *testing.T) {
	assert.Equal(t, "io.containerd.kata.v2", runtime.IsolationContainerRuntime("vm"))
}

func TestIsolationContainerRuntime_VMEnhanced(t *testing.T) {
	assert.Equal(t, "io.containerd.kata-fc.v2", runtime.IsolationContainerRuntime("vm-enhanced"))
}

// BackendCaps tests — each backend declares its own capabilities.
// Read the static descriptor via the registry rather than instantiating the
// runtime; the backend packages register themselves at init().

// mustDescriptor returns the registered descriptor for name, failing the test
// if the backend isn't registered (e.g., test ran on an unsupported platform).
func mustDescriptor(t *testing.T, name runtime.BackendName) runtime.BackendDescriptor {
	t.Helper()
	desc, ok := runtime.Descriptor(name)
	require.True(t, ok, "backend %q not registered", name)
	return desc
}

func TestBackendCaps_Docker(t *testing.T) {
	caps := mustDescriptor(t, "docker").Capabilities
	assert.True(t, caps.NetworkIsolation)
	assert.True(t, caps.OverlayDirs)
	assert.True(t, caps.CapAdd)
}

func TestBackendCaps_Tart(t *testing.T) {
	caps := mustDescriptor(t, "tart").Capabilities
	assert.False(t, caps.NetworkIsolation)
	assert.False(t, caps.OverlayDirs)
	assert.False(t, caps.CapAdd)
}

func TestBackendCaps_Seatbelt(t *testing.T) {
	caps := mustDescriptor(t, "seatbelt").Capabilities
	assert.False(t, caps.NetworkIsolation)
	assert.False(t, caps.OverlayDirs)
	assert.False(t, caps.CapAdd)
}

// AgentProvisionedByBackend and ResolveCopyMount tests

func TestAgentProvisionedByBackend_Docker(t *testing.T) {
	assert.True(t, mustDescriptor(t, "docker").AgentProvisionedByBackend)
}

func TestAgentProvisionedByBackend_Tart(t *testing.T) {
	assert.True(t, mustDescriptor(t, "tart").AgentProvisionedByBackend)
}

func TestAgentProvisionedByBackend_Seatbelt(t *testing.T) {
	assert.False(t, mustDescriptor(t, "seatbelt").AgentProvisionedByBackend) // uses host native agent
}

func TestResolveCopyMount_Docker(t *testing.T) {
	// Docker doesn't implement CopyMountResolver — helper falls back to hostPath.
	rt := (*dockerrt.Runtime)(nil)
	assert.Equal(t, "/home/user/project", runtime.ResolveCopyMountFor(rt, "mysandbox", "/home/user/project"))
}

func TestResolveCopyMount_Tart(t *testing.T) {
	// Tart implements CopyMountResolver — returns local VM path.
	result := runtime.ResolveCopyMountFor((*tartrt.Runtime)(nil), "mysandbox", "/home/user/project")
	assert.Equal(t, "/Users/admin/yoloai-work/^shome^suser^sproject", result)
}

// CheckIsolationPrerequisites tests

// capsRuntime wraps mockRuntime and overrides RequiredCapabilities for testing.
type capsRuntime struct {
	mockRuntime
	capList []caps.HostCapability
}

func (c *capsRuntime) RequiredCapabilities(_ runtime.IsolationMode) []caps.HostCapability {
	return c.capList
}

func TestCheckIsolationPrerequisites_NoCaps(t *testing.T) {
	// mockRuntime returns nil from RequiredCapabilities — should be a no-op.
	rt := &mockRuntime{}
	err := create.CheckIsolationPrerequisites(context.Background(), rt, "container-enhanced")
	assert.NoError(t, err)
}

func TestCheckIsolationPrerequisites_AllCapsPass(t *testing.T) {
	rt := &capsRuntime{
		capList: []caps.HostCapability{
			{ID: "a", Summary: "Cap A", Check: func(_ context.Context) error { return nil }},
		},
	}
	err := create.CheckIsolationPrerequisites(context.Background(), rt, "vm")
	assert.NoError(t, err)
}

func TestCheckIsolationPrerequisites_CapFails(t *testing.T) {
	rt := &capsRuntime{
		capList: []caps.HostCapability{
			{ID: "kata-shim", Summary: "kata shim", Check: func(_ context.Context) error { return fmt.Errorf("kata shim not found") }},
		},
	}
	err := create.CheckIsolationPrerequisites(context.Background(), rt, "vm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kata shim")
}

func TestCheckIsolationPrerequisites_IsolationModeForwarded(t *testing.T) {
	rt := &capsRuntime{}
	// For this test we use the base capsRuntime which returns nil caps.
	// Just verify that CheckIsolationPrerequisites doesn't panic for any mode.
	for _, mode := range []runtime.IsolationMode{"container", "container-enhanced", "vm", "vm-enhanced", ""} {
		err := create.CheckIsolationPrerequisites(context.Background(), rt, mode)
		assert.NoError(t, err, "mode %q should not fail with nil caps", mode)
	}
}
