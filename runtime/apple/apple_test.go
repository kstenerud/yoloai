package apple

import (
	"context"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOrphanInstances locks the Prune sweep's filter: only this-principal
// (prefix-matching) names that aren't known are orphans; non-prefixed names,
// known names, and blank/whitespace lines are skipped.
func TestOrphanInstances(t *testing.T) {
	list := "yoloai-alpha\nyoloai-beta\nother-thing\n\n  yoloai-gamma  \n"
	got := orphanInstances(list, "yoloai-", []string{"yoloai-beta"})
	assert.Equal(t, []string{"yoloai-alpha", "yoloai-gamma"}, got)

	// Everything known or non-matching → no orphans.
	assert.Empty(t, orphanInstances("yoloai-x\nother\n", "yoloai-", []string{"yoloai-x"}))
	assert.Empty(t, orphanInstances("", "yoloai-", nil))
}

// TestRegistered confirms the backend's init() registered a sane descriptor —
// vm-tier, darwin/arm64, with the verified capability flags.
func TestRegistered(t *testing.T) {
	d, ok := runtime.Descriptor(runtime.BackendApple)
	require.True(t, ok, "apple backend must be registered")
	assert.Equal(t, runtime.BackendApple, d.Type)
	assert.Equal(t, runtime.IsolationModeVM, d.BaseModeName, "apple is vm-tier, not container-slot")
	assert.Equal(t, []string{"darwin"}, d.Platforms)
	assert.Equal(t, []string{"arm64"}, d.Architectures)
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

// TestSelectBackend_DarwinPrefersApple verifies the macOS-host routing: when
// apple is installed it's the default and the --isolation vm target, but
// --isolation container and an explicit container preference stay in the
// container slot. Runs only on a Mac with the container CLI installed; this test
// binary registers both apple and — via apple's import — docker.
func TestSelectBackend_DarwinPrefersApple(t *testing.T) {
	ctx := context.Background()
	if !isMacOS() {
		t.Skip("apple routing only applies on a macOS host")
	}
	if installed, _ := runtime.Installed(ctx, runtime.BackendApple, nil); !installed {
		t.Skip("apple backend not installed on this host")
	}

	got, _ := runtime.SelectBackend(ctx, "", runtime.IsolationModeDefault, "", nil)
	assert.Equal(t, runtime.BackendApple, got, "macOS default prefers apple")

	got, _ = runtime.SelectBackend(ctx, "", runtime.IsolationModeVM, "", nil)
	assert.Equal(t, runtime.BackendApple, got, "--isolation vm routes to apple")

	got, _ = runtime.SelectBackend(ctx, "", runtime.IsolationModeContainer, "", nil)
	assert.NotEqual(t, runtime.BackendApple, got, "--isolation container is not apple")

	got, _ = runtime.SelectBackend(ctx, runtime.BackendDocker, runtime.IsolationModeDefault, "", nil)
	assert.NotEqual(t, runtime.BackendApple, got, "explicit container_backend wins over the apple default")

	got, _ = runtime.SelectBackend(ctx, runtime.BackendApple, runtime.IsolationModeDefault, "", nil)
	assert.Equal(t, runtime.BackendApple, got, "container_backend=apple is honored")
}
