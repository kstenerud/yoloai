// ABOUTME: Tests for the netpolicy.json Save/Load round-trip and missing-file default.
package netpolicycfg_test

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// TestSaveTo_WritesExactlyWhereTold: the path-taking writer resolves nothing —
// it writes where told and creates no host/ tier, which is what lets a pre-v6
// migrator write a flat-era record (DF164).
func TestSaveTo_WritesExactlyWhereTold(t *testing.T) {
	dir := t.TempDir()
	flat := filepath.Join(dir, "netpolicy.json") // literal: a pre-tier record

	require.NoError(t, netpolicycfg.SaveTo(flat, &netpolicycfg.Netpolicy{Mode: "isolated", Allow: []string{"a.example"}}))

	require.FileExists(t, flat)
	_, err := os.Stat(filepath.Join(dir, "host"))
	assert.True(t, os.IsNotExist(err), "a path-taking writer must not create the host/ tier")

	data, err := os.ReadFile(flat) //nolint:gosec // G304: trusted t.TempDir() subpath
	require.NoError(t, err)
	var got netpolicycfg.Netpolicy
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "isolated", got.Mode)
	assert.Equal(t, []string{"a.example"}, got.Allow)
}
