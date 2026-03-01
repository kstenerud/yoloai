package cli

// ABOUTME: Tests for the config get/set CLI commands.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigGet_EffectiveConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	content := "setup_complete: true\ndefaults:\n  backend: seatbelt\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	cmd := newConfigGetCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	out := buf.String()
	// File override should appear
	assert.Contains(t, out, "backend: seatbelt")
	// Defaults for unset keys should appear
	assert.Contains(t, out, "image:")
	assert.Contains(t, out, "tmux_conf:")
}

func TestConfigGet_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := newConfigGetCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	out := buf.String()
	// Should still show all defaults
	assert.Contains(t, out, "setup_complete: false")
	assert.Contains(t, out, "backend: docker")
	assert.Contains(t, out, "agent: claude")
}

func TestConfigGet_ScalarKey(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	content := "setup_complete: true\ndefaults:\n  backend: seatbelt\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	cmd := newConfigGetCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"defaults.backend"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "seatbelt\n", buf.String())
}

func TestConfigSet_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	content := "# my config\nsetup_complete: false\ndefaults:\n  backend: docker\n"
	configPath := filepath.Join(yoloaiDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0600))

	cmd := newConfigSetCmd()
	cmd.SetArgs([]string{"defaults.backend", "seatbelt"})
	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: test code with temp dir path
	require.NoError(t, err)
	assert.Contains(t, string(data), "backend: seatbelt")
	assert.Contains(t, string(data), "my config")
}

func TestConfigSet_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := newConfigSetCmd()
	cmd.SetArgs([]string{"defaults.backend", "tart"})
	require.NoError(t, cmd.Execute())

	configPath := filepath.Join(tmpDir, ".yoloai", "config.yaml")
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: test code with temp dir path
	require.NoError(t, err)
	assert.Contains(t, string(data), "backend: tart")
}

func TestConfigSet_Agent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	content := "setup_complete: true\ndefaults:\n  backend: docker\n"
	configPath := filepath.Join(yoloaiDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0600))

	cmd := newConfigSetCmd()
	cmd.SetArgs([]string{"defaults.agent", "gemini"})
	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: test code with temp dir path
	require.NoError(t, err)
	assert.Contains(t, string(data), "agent: gemini")

	// Verify via get
	getCmd := newConfigGetCmd()
	buf := new(bytes.Buffer)
	getCmd.SetOut(buf)
	getCmd.SetArgs([]string{"defaults.agent"})
	require.NoError(t, getCmd.Execute())
	assert.Equal(t, "gemini\n", buf.String())
}
