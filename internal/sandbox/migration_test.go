package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateIfNeeded_NoOldLayout(t *testing.T) {
	tmpDir := t.TempDir()
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	// No old files — should be a no-op
	require.NoError(t, MigrateIfNeeded(yoloaiDir))

	// profiles/base/ should not be created
	_, err := os.Stat(filepath.Join(yoloaiDir, "profiles", "base"))
	assert.True(t, os.IsNotExist(err))
}

func TestMigrateIfNeeded_DetectsDockerfileBase(t *testing.T) {
	tmpDir := t.TempDir()
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	// Create old-style Dockerfile.base
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "Dockerfile.base"), []byte("FROM debian:slim\n"), 0600))

	require.NoError(t, MigrateIfNeeded(yoloaiDir))

	// Dockerfile.base should have moved to profiles/base/Dockerfile
	_, err := os.Stat(filepath.Join(yoloaiDir, "Dockerfile.base"))
	assert.True(t, os.IsNotExist(err))

	data, err := os.ReadFile(filepath.Join(yoloaiDir, "profiles", "base", "Dockerfile")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Equal(t, "FROM debian:slim\n", string(data))
}

func TestMigrateIfNeeded_DetectsOldConfigKeys(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	oldConfig := "setup_complete: true\ndefaults:\n  backend: tart\n  agent: gemini\n  tmux_conf: default+host\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(oldConfig), 0600))

	require.NoError(t, MigrateIfNeeded(yoloaiDir))

	// Old config.yaml should be gone
	_, err := os.Stat(filepath.Join(yoloaiDir, "config.yaml"))
	assert.True(t, os.IsNotExist(err))

	// state.yaml should have setup_complete
	stateData, err := os.ReadFile(filepath.Join(yoloaiDir, "state.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Contains(t, string(stateData), "setup_complete: true")

	// New config should have flattened keys
	newConfig, err := os.ReadFile(filepath.Join(yoloaiDir, "profiles", "base", "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	configStr := string(newConfig)
	assert.Contains(t, configStr, "backend: tart")
	assert.Contains(t, configStr, "agent: gemini")
	assert.Contains(t, configStr, "tmux_conf: default+host")
	assert.NotContains(t, configStr, "setup_complete")
	assert.NotContains(t, configStr, "defaults:")
}

func TestMigrateIfNeeded_MovesResourceFiles(t *testing.T) {
	tmpDir := t.TempDir()
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	// Create old-style files
	files := map[string]string{
		"Dockerfile.base":      "FROM debian\n",
		"entrypoint.sh":        "#!/bin/bash\n",
		"tmux.conf":            "set -g mouse on\n",
		".last-build-checksum": "abc123",
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, name), []byte(content), 0600))
	}

	require.NoError(t, MigrateIfNeeded(yoloaiDir))

	baseDir := filepath.Join(yoloaiDir, "profiles", "base")

	// Old files should be gone
	for name := range files {
		_, err := os.Stat(filepath.Join(yoloaiDir, name))
		assert.True(t, os.IsNotExist(err), "%s should not exist at old location", name)
	}

	// New files should exist
	data, err := os.ReadFile(filepath.Join(baseDir, "Dockerfile")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Equal(t, "FROM debian\n", string(data))

	data, err = os.ReadFile(filepath.Join(baseDir, "entrypoint.sh")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/bash\n", string(data))

	data, err = os.ReadFile(filepath.Join(baseDir, "tmux.conf")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Equal(t, "set -g mouse on\n", string(data))
}

func TestMigrateIfNeeded_MovesConflictFiles(t *testing.T) {
	tmpDir := t.TempDir()
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	// Old-style files + .new conflict
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "Dockerfile.base"), []byte("old\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "Dockerfile.base.new"), []byte("new\n"), 0600))

	require.NoError(t, MigrateIfNeeded(yoloaiDir))

	baseDir := filepath.Join(yoloaiDir, "profiles", "base")
	data, err := os.ReadFile(filepath.Join(baseDir, "Dockerfile.new")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(data))
}

func TestMigrateIfNeeded_TransformsChecksums(t *testing.T) {
	tmpDir := t.TempDir()
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	// Trigger migration with Dockerfile.base
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "Dockerfile.base"), []byte("FROM debian\n"), 0600))

	checksums := map[string]string{
		"Dockerfile.base": "abc123",
		"entrypoint.sh":   "def456",
	}
	data, err := json.Marshal(checksums)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, ".resource-checksums"), data, 0600))

	require.NoError(t, MigrateIfNeeded(yoloaiDir))

	// Old checksums should be gone
	_, err = os.Stat(filepath.Join(yoloaiDir, ".resource-checksums"))
	assert.True(t, os.IsNotExist(err))

	// New checksums should have Dockerfile key (not Dockerfile.base)
	newData, err := os.ReadFile(filepath.Join(yoloaiDir, "profiles", "base", ".resource-checksums")) //nolint:gosec // G304: test code
	require.NoError(t, err)

	var newChecksums map[string]string
	require.NoError(t, json.Unmarshal(newData, &newChecksums))
	assert.Equal(t, "abc123", newChecksums["Dockerfile"])
	assert.Equal(t, "def456", newChecksums["entrypoint.sh"])
	_, hasOld := newChecksums["Dockerfile.base"]
	assert.False(t, hasOld)
}

func TestMigrateIfNeeded_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	// Set up old layout
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "Dockerfile.base"), []byte("FROM debian\n"), 0600))
	oldConfig := "setup_complete: true\ndefaults:\n  backend: docker\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(oldConfig), 0600))

	// First migration
	require.NoError(t, MigrateIfNeeded(yoloaiDir))

	// Second migration should be a no-op
	require.NoError(t, MigrateIfNeeded(yoloaiDir))

	// Files should still be in new location
	_, err := os.Stat(filepath.Join(yoloaiDir, "profiles", "base", "Dockerfile"))
	require.NoError(t, err)
}

func TestMigrateIfNeeded_PreservesExtraKeys(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	oldConfig := "setup_complete: true\ncustom_key: myvalue\ndefaults:\n  agent: claude\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(oldConfig), 0600))

	require.NoError(t, MigrateIfNeeded(yoloaiDir))

	newConfig, err := os.ReadFile(filepath.Join(yoloaiDir, "profiles", "base", "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	configStr := string(newConfig)
	assert.Contains(t, configStr, "custom_key: myvalue")
	assert.Contains(t, configStr, "agent: claude")
	assert.NotContains(t, configStr, "setup_complete")
}
