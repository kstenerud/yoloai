//go:build linux

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstenerud/yoloai/runtime"
	containerdrt "github.com/kstenerud/yoloai/runtime/containerd"
)

func TestBackendCaps_Containerd(t *testing.T) {
	caps := (*containerdrt.Runtime)(nil).Descriptor().Capabilities
	assert.True(t, caps.NetworkIsolation)
	assert.False(t, caps.OverlayDirs) // overlayfs not supported inside Kata VMs
	assert.True(t, caps.CapAdd)
}

func TestAgentProvisionedByBackend_Containerd(t *testing.T) {
	assert.True(t, (*containerdrt.Runtime)(nil).Descriptor().AgentProvisionedByBackend)
}

func TestResolveCopyMount_Containerd(t *testing.T) {
	// containerd doesn't implement CopyMountResolver — helper falls back to hostPath.
	rt := (*containerdrt.Runtime)(nil)
	assert.Equal(t, "/home/user/project", runtime.ResolveCopyMountFor(rt, "mysandbox", "/home/user/project"))
}
