package cli

// ABOUTME: Unit tests for sandbox network shared helpers: loadIsolatedMeta,
// ABOUTME: saveNetworkAllowlist.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createNetworkSandbox creates a sandbox directory with meta.json and config.json
// suitable for network command testing. Returns the sandbox directory path.
func createNetworkSandbox(t *testing.T, name, networkMode string, domains []string) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	sandboxDir := filepath.Join(tmpHome, ".yoloai", "sandboxes", name)
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	meta := &sandbox.Meta{
		Name:         name,
		Agent:        "test",
		Backend:      "docker",
		NetworkMode:  networkMode,
		NetworkAllow: domains,
		Workdir:      sandbox.WorkdirMeta{HostPath: "/tmp/test", MountPath: "/tmp/test", Mode: "copy"},
	}
	require.NoError(t, sandbox.SaveMeta(sandboxDir, meta))

	// Write minimal config.json that PatchConfigAllowedDomains can parse
	cfg := map[string]any{
		"host_uid":        1000,
		"host_gid":        1000,
		"agent_command":   "bash",
		"working_dir":     "/tmp/test",
		"allowed_domains": domains,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sandboxDir, "config.json"), data, 0600))

	return sandboxDir
}

// --- loadIsolatedMeta tests ---

func TestLoadIsolatedMeta_Isolated(t *testing.T) {
	createNetworkSandbox(t, "net-ok", "isolated", []string{"api.example.com"})

	dir, meta, err := loadIsolatedMeta("net-ok")
	require.NoError(t, err)
	assert.NotEmpty(t, dir)
	assert.Equal(t, "isolated", meta.NetworkMode)
	assert.Equal(t, []string{"api.example.com"}, meta.NetworkAllow)
}

func TestLoadIsolatedMeta_None(t *testing.T) {
	createNetworkSandbox(t, "net-none", "none", nil)

	_, _, err := loadIsolatedMeta("net-none")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--network-none")
}

func TestLoadIsolatedMeta_Open(t *testing.T) {
	createNetworkSandbox(t, "net-open", "", nil)

	_, _, err := loadIsolatedMeta("net-open")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not using network isolation")
}

func TestLoadIsolatedMeta_UnrecognizedMode(t *testing.T) {
	createNetworkSandbox(t, "net-bogus", "bogus", nil)

	_, _, err := loadIsolatedMeta("net-bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not using network isolation")
}

func TestLoadIsolatedMeta_NoSandbox(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	_, _, err := loadIsolatedMeta("nonexistent")
	require.Error(t, err)
}

func TestLoadIsolatedMeta_NoMeta(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	sandboxDir := filepath.Join(tmpHome, ".yoloai", "sandboxes", "no-meta")
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))

	_, _, err := loadIsolatedMeta("no-meta")
	require.Error(t, err)
}

// --- saveNetworkAllowlist tests ---

func TestSaveNetworkAllowlist_UpdatesBothFiles(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "save-both", "isolated", []string{"old.example.com"})

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	meta.NetworkAllow = []string{"old.example.com", "new.example.com"}

	require.NoError(t, saveNetworkAllowlist(sandboxDir, meta))

	// Verify meta.json
	reloaded, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"old.example.com", "new.example.com"}, reloaded.NetworkAllow)

	// Verify config.json
	data, err := os.ReadFile(filepath.Join(sandboxDir, "config.json")) //nolint:gosec // test path
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(data, &cfg))
	domains := cfg["allowed_domains"].([]any)
	assert.Len(t, domains, 2)
	assert.Equal(t, "old.example.com", domains[0])
	assert.Equal(t, "new.example.com", domains[1])
}

func TestSaveNetworkAllowlist_EmptyList(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "save-empty", "isolated", []string{"was.here.com"})

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	meta.NetworkAllow = nil

	require.NoError(t, saveNetworkAllowlist(sandboxDir, meta))

	reloaded, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	assert.Nil(t, reloaded.NetworkAllow)
}

func TestSaveNetworkAllowlist_NoConfigJSON(t *testing.T) {
	sandboxDir := createNetworkSandbox(t, "save-nocfg", "isolated", []string{"x.com"})
	require.NoError(t, os.Remove(filepath.Join(sandboxDir, "config.json")))

	meta, err := sandbox.LoadMeta(sandboxDir)
	require.NoError(t, err)
	meta.NetworkAllow = []string{"y.com"}

	err = saveNetworkAllowlist(sandboxDir, meta)
	require.Error(t, err, "should fail when config.json is missing")
}
