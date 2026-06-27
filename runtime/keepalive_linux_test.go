//go:build linux

// ABOUTME: Linux-only keepalive assertion for the containerd backend, whose
// ABOUTME: containerd Go client deps build only on linux (so it registers only there).

package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
	// containerd is linux-only; this side-effect import (and so this whole file)
	// is build-tagged linux, mirroring the binary's runtime_imports_linux.go. The
	// cross-platform backends are asserted in keepalive_test.go.
	_ "github.com/kstenerud/yoloai/runtime/containerd"
)

// TestKeepAliveModelOf_Containerd asserts the containerd backend's KeepAliveModel
// (Kata VMs → guest-OS init). Kept here, not in the neutral per-backend table,
// because containerd registers only on linux.
func TestKeepAliveModelOf_Containerd(t *testing.T) {
	desc, ok := runtime.Descriptor(runtime.BackendContainerd)
	require.True(t, ok, "containerd backend not registered")
	assert.Equal(t, runtime.KeepAliveGuestOSInit, desc.Capabilities.KeepAliveModel)
}
