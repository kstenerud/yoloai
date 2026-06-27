// ABOUTME: Tests for the netpolicy.json Save/Load round-trip and missing-file default.
package netpolicycfg_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/netpolicycfg"
)

func TestNetpolicy_Roundtrip(t *testing.T) {
	dir := t.TempDir()

	np := &netpolicycfg.Netpolicy{Mode: "isolated", Allow: []string{"api.example.com", "cdn.example.com"}}
	require.NoError(t, netpolicycfg.Save(dir, np))

	loaded, err := netpolicycfg.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "isolated", loaded.Mode)
	assert.Equal(t, []string{"api.example.com", "cdn.example.com"}, loaded.Allow)
	assert.Equal(t, 1, loaded.Version)
}

func TestNetpolicy_MissingFile(t *testing.T) {
	dir := t.TempDir()

	loaded, err := netpolicycfg.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "", loaded.Mode, "missing netpolicy.json should return zero Mode")
	assert.Empty(t, loaded.Allow, "missing netpolicy.json should return nil Allow")
}

func TestNetpolicy_ModeNone(t *testing.T) {
	dir := t.TempDir()

	np := &netpolicycfg.Netpolicy{Mode: "none"}
	require.NoError(t, netpolicycfg.Save(dir, np))

	loaded, err := netpolicycfg.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "none", loaded.Mode)
	assert.Empty(t, loaded.Allow)
}
