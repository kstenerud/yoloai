package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/runtime"
	dockerrt "github.com/kstenerud/yoloai/internal/runtime/docker"
	_ "github.com/kstenerud/yoloai/internal/runtime/seatbelt" // backend init() registers the descriptor for tests
	tartrt "github.com/kstenerud/yoloai/internal/runtime/tart"
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
func mustDescriptor(t *testing.T, name runtime.BackendType) runtime.BackendDescriptor {
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
