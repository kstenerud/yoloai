package apple

import (
	"context"
	"testing"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRegistered confirms the backend's init() registered a sane descriptor —
// vm-tier, darwin/arm64, with the verified capability flags.
func TestRegistered(t *testing.T) {
	d, ok := runtime.Descriptor(runtime.BackendApple)
	require.True(t, ok, "apple backend must be registered")
	assert.Equal(t, runtime.BackendApple, d.Type)
	assert.Equal(t, runtime.IsolationModeVM, d.BaseModeName, "apple is vm-tier, not container-slot")
	assert.Equal(t, []string{"darwin"}, d.Platforms)
	assert.Equal(t, []string{"arm64"}, d.Architectures)
	assert.True(t, d.Capabilities.OverlayDirs, "overlayfs verified")
	assert.True(t, d.Capabilities.NetworkIsolation, "in-guest iptables verified")
	assert.True(t, d.Capabilities.CapAdd)
	require.NotNil(t, d.Probe)
}

// TestProbe_TierOnThisHost checks the probe never returns Running (apple is
// started on demand) and is Absent off macOS/arm64. On a supported host it is
// Absent (CLI/version gate) or Installed — never Running.
func TestProbe_TierOnThisHost(t *testing.T) {
	status, reason := probe(context.Background(), nil)
	assert.NotEqual(t, runtime.ProbeRunning, status, "apple probe reports Installed at most; running is checked at point-of-use")

	if !isMacOS() || !isAppleSilicon() {
		assert.Equal(t, runtime.ProbeAbsent, status)
		assert.NotEmpty(t, reason)
		return
	}
	assert.Contains(t,
		[]runtime.ProbeStatus{runtime.ProbeAbsent, runtime.ProbeInstalled}, status,
		"supported host: Absent (gate) or Installed")
}
