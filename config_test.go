// ABOUTME: Tests for SystemClient.Config() sub-handle: Effective / Get / Set /
// ABOUTME: Reset. Filesystem-backed; uses t.TempDir.

package yoloai

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Effective ---

func TestConfig_Effective_ReturnsBakedInDefaults(t *testing.T) {
	c := newTestClient(t)
	out, err := c.Config().Effective(context.Background())
	require.NoError(t, err)
	// Baked-in defaults contain at least an agent line.
	assert.Contains(t, out, "agent:")
}

func TestConfig_Effective_OverlaysProfileDefaults(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	require.NoError(t, c.Config().Set(ctx, "backend", "podman"))
	out, err := c.Config().Effective(ctx)
	require.NoError(t, err)
	assert.Contains(t, out, "backend: podman")
}

// --- Get ---

func TestConfig_Get_BakedInValue(t *testing.T) {
	c := newTestClient(t)
	// DefaultConfigYAML sets agent: claude — verifies the read path
	// resolves through the merged config even with no user overrides.
	value, err := c.Config().Get(context.Background(), "agent")
	require.NoError(t, err)
	assert.Equal(t, "claude", value)
}

func TestConfig_Get_AfterSet(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	require.NoError(t, c.Config().Set(ctx, "backend", "podman"))
	value, err := c.Config().Get(ctx, "backend")
	require.NoError(t, err)
	assert.Equal(t, "podman", value)
}

func TestConfig_Get_MissingKey_TypedError(t *testing.T) {
	c := newTestClient(t)
	_, err := c.Config().Get(context.Background(), "this_key_does_not_exist")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrConfigKeyNotFound),
		"missing key must report via ErrConfigKeyNotFound so embedders can errors.Is")
	assert.Contains(t, err.Error(), "this_key_does_not_exist")
}

// --- Set ---

func TestConfig_Set_GlobalKey_WritesGlobalConfig(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// tmux_conf is one of the IsGlobalKey() keys → lands in
	// ~/.yoloai/config.yaml, not ~/.yoloai/defaults/config.yaml.
	require.NoError(t, c.Config().Set(ctx, "tmux_conf", "host"))

	globalPath := c.layout.GlobalConfigPath()
	require.FileExists(t, globalPath)
	data, err := os.ReadFile(globalPath) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(data), "tmux_conf")
	assert.Contains(t, string(data), "host")

	// And NOT in the profile-defaults file.
	defaultsPath := c.layout.DefaultsConfigPath()
	if _, err := os.Stat(defaultsPath); err == nil {
		body, _ := os.ReadFile(defaultsPath) //nolint:gosec // test path
		assert.NotContains(t, string(body), "tmux_conf",
			"global keys must not leak into defaults/config.yaml")
	}
}

func TestConfig_Set_ProfileKey_WritesDefaultsConfig(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	require.NoError(t, c.Config().Set(ctx, "backend", "podman"))

	defaultsPath := c.layout.DefaultsConfigPath()
	require.FileExists(t, defaultsPath)
	data, err := os.ReadFile(defaultsPath) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(data), "backend: podman")
}

func TestConfig_Set_CreatesParentDir(t *testing.T) {
	c := newTestClient(t)
	// Fresh DataDir; no defaults/ subdir exists yet. Set must create it.
	require.NoError(t, c.Config().Set(context.Background(), "backend", "podman"))
	require.FileExists(t, c.layout.DefaultsConfigPath())
}

// --- Reset ---

func TestConfig_Reset_RemovesUserOverride(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	require.NoError(t, c.Config().Set(ctx, "backend", "podman"))
	value, err := c.Config().Get(ctx, "backend")
	require.NoError(t, err)
	require.Equal(t, "podman", value)

	require.NoError(t, c.Config().Reset(ctx, "backend"))

	// After reset the user override is gone. The baked-in default
	// has no `backend` set, so Get should report not-found.
	_, err = c.Config().Get(ctx, "backend")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrConfigKeyNotFound))
}

func TestConfig_Reset_GlobalKey(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	require.NoError(t, c.Config().Set(ctx, "tmux_conf", "host"))
	value, err := c.Config().Get(ctx, "tmux_conf")
	require.NoError(t, err)
	require.Equal(t, "host", value, "Set followed by Get round-trips through the global config layer")

	require.NoError(t, c.Config().Reset(ctx, "tmux_conf"))

	// After reset the user override is gone; tmux_conf has a known
	// default of "" in globalKnownSettings, so Get returns the
	// baked-in default rather than ErrConfigKeyNotFound. Either
	// outcome is fine in principle, but we lock in the existing
	// "reset returns the user to the baked-in default" semantics so
	// embedders can predict it.
	value, err = c.Config().Get(ctx, "tmux_conf")
	require.NoError(t, err)
	assert.Equal(t, "", value)
}

func TestConfig_Reset_NonexistentKey_NoError(t *testing.T) {
	c := newTestClient(t)
	// Reset is idempotent: clearing a never-set key is a no-op.
	assert.NoError(t, c.Config().Reset(context.Background(), "backend"))
}
