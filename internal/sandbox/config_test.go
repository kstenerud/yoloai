package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Default(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.False(t, cfg.SetupComplete)
	assert.Empty(t, cfg.TmuxConf)
}

func TestLoadConfig_WithTmuxConf(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	content := `setup_complete: true
defaults:
  tmux_conf: default+host
  agent: claude
`
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default+host", cfg.TmuxConf)
}

func TestLoadConfig_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.False(t, cfg.SetupComplete)
	assert.Empty(t, cfg.TmuxConf)
}

func TestUpdateConfigFields_SetupComplete(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	err := updateConfigFields(map[string]string{
		"setup_complete": "true",
	})
	require.NoError(t, err)

	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
}

func TestUpdateConfigFields_TmuxConf(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	err := updateConfigFields(map[string]string{
		"defaults.tmux_conf": "default+host",
		"setup_complete":     "true",
	})
	require.NoError(t, err)

	cfg, err := loadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default+host", cfg.TmuxConf)
}

func TestUpdateConfigFields_PreservesComments(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	content := `# yoloai configuration
setup_complete: false
defaults:
  agent: claude # my preferred agent
`
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	err := updateConfigFields(map[string]string{
		"setup_complete": "true",
	})
	require.NoError(t, err)

	// Re-read raw content to verify comment is preserved
	data, err := os.ReadFile(filepath.Join(yoloaiDir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Contains(t, string(data), "my preferred agent")
}

func TestSplitDottedPath(t *testing.T) {
	assert.Equal(t, []string{"a"}, splitDottedPath("a"))
	assert.Equal(t, []string{"a", "b"}, splitDottedPath("a.b"))
	assert.Equal(t, []string{"defaults", "tmux_conf"}, splitDottedPath("defaults.tmux_conf"))
}
