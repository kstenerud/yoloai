//go:build linux

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"

	containerdrt "github.com/kstenerud/yoloai/runtime/containerd"
)

func TestBackendCaps_Containerd(t *testing.T) {
	caps := (*containerdrt.Runtime)(nil).Capabilities()
	assert.True(t, caps.NetworkIsolation)
	assert.False(t, caps.OverlayDirs) // overlayfs not supported inside Kata VMs
	assert.True(t, caps.CapAdd)
}

func TestAgentProvisionedByBackend_Containerd(t *testing.T) {
	assert.True(t, (*containerdrt.Runtime)(nil).AgentProvisionedByBackend())
}

func TestResolveCopyMount_Containerd(t *testing.T) {
	assert.Equal(t, "/home/user/project", (*containerdrt.Runtime)(nil).ResolveCopyMount("mysandbox", "/home/user/project"))
}
