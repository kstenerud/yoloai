package cli

// ABOUTME: Tests for the config get/set/reset CLI commands.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cliConfigDir creates the defaults/ directory structure for CLI config tests.
func cliConfigDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dir := filepath.Join(tmpDir, ".yoloai", "defaults")
	require.NoError(t, os.MkdirAll(dir, 0750))
	return dir
}

func TestConfigGet_EffectiveConfig(t *testing.T) {
	dir := cliConfigDir(t)
	content := "container_backend: seatbelt\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cmd := newConfigGetCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	out := buf.String()
	// File override should appear
	assert.Contains(t, out, "container_backend: seatbelt")
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
	assert.Contains(t, out, "container_backend:")
	assert.Contains(t, out, "agent: claude")
}

func TestConfigGet_ScalarKey(t *testing.T) {
	dir := cliConfigDir(t)
	content := "container_backend: seatbelt\ntmux_conf: default+host\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cmd := newConfigGetCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"container_backend"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "seatbelt\n", buf.String())
}

func TestConfigSet_ExistingFile(t *testing.T) {
	dir := cliConfigDir(t)
	content := "# my config\ncontainer_backend: docker\n"
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0600))

	cmd := newConfigSetCmd()
	cmd.SetArgs([]string{"container_backend", "seatbelt"})
	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: test code with temp dir path
	require.NoError(t, err)
	assert.Contains(t, string(data), "container_backend: seatbelt")
	assert.Contains(t, string(data), "my config")
}

func TestConfigSet_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := newConfigSetCmd()
	cmd.SetArgs([]string{"container_backend", "tart"})
	require.NoError(t, cmd.Execute())

	configPath := filepath.Join(tmpDir, ".yoloai", "defaults", "config.yaml")
	data, err := os.ReadFile(configPath) //nolint:gosec // G304: test code with temp dir path
	require.NoError(t, err)
	assert.Contains(t, string(data), "container_backend: tart")
}

func TestConfigSet_Agent(t *testing.T) {
	dir := cliConfigDir(t)
	content := "container_backend: docker\n"
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0600))

	cmd := newConfigSetCmd()
	cmd.SetArgs([]string{"agent", "gemini"})
	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(configPath) //nolint:gosec // G304: test code with temp dir path
	require.NoError(t, err)
	assert.Contains(t, string(data), "agent: gemini")

	// Verify via get
	getCmd := newConfigGetCmd()
	buf := new(bytes.Buffer)
	getCmd.SetOut(buf)
	getCmd.SetArgs([]string{"agent"})
	require.NoError(t, getCmd.Execute())
	assert.Equal(t, "gemini\n", buf.String())
}

func TestConfigReset_RemovesKey(t *testing.T) {
	dir := cliConfigDir(t)
	content := "container_backend: tart\nagent: gemini\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cmd := newConfigResetCmd()
	cmd.SetArgs([]string{"container_backend"})
	require.NoError(t, cmd.Execute())

	// Verify via get — should show default
	getCmd := newConfigGetCmd()
	buf := new(bytes.Buffer)
	getCmd.SetOut(buf)
	getCmd.SetArgs([]string{"container_backend"})
	require.NoError(t, getCmd.Execute())
	// container_backend default is "" (auto-detect); reset removes the key so get returns empty.
	assert.Equal(t, "\n", buf.String())
}

func TestConfigReset_RequiresArg(t *testing.T) {
	cmd := newConfigResetCmd()
	cmd.SetArgs([]string{})
	assert.Error(t, cmd.Execute())
}
