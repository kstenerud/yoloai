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

	cfg, err := LoadConfig()
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

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default+host", cfg.TmuxConf)
	assert.Equal(t, "claude", cfg.Agent)
}

func TestLoadConfig_AgentDefault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.Agent) // Not set in file; caller uses knownSettings default
}

func TestLoadConfig_AgentOverride(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	content := "setup_complete: true\ndefaults:\n  agent: gemini\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "gemini", cfg.Agent)
}

func TestLoadConfig_ModelDefault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.Model) // Not set in file; empty means agent's default
}

func TestLoadConfig_ModelOverride(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	content := "setup_complete: true\ndefaults:\n  model: o3\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "o3", cfg.Model)
}

func TestLoadConfig_EnvMap(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	content := "defaults:\n  env:\n    OLLAMA_API_BASE: http://host.docker.internal:11434\n    CUSTOM_VAR: myvalue\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Len(t, cfg.Env, 2)
	assert.Equal(t, "http://host.docker.internal:11434", cfg.Env["OLLAMA_API_BASE"])
	assert.Equal(t, "myvalue", cfg.Env["CUSTOM_VAR"])
}

func TestLoadConfig_EnvExpansion(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("YOLOAI_TEST_HOST", "localhost")

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	content := "defaults:\n  env:\n    API_BASE: http://${YOLOAI_TEST_HOST}:11434\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Len(t, cfg.Env, 1)
	assert.Equal(t, "http://localhost:11434", cfg.Env["API_BASE"])
}

func TestLoadConfig_EnvExpansionError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	content := "defaults:\n  env:\n    BAD_VAR: ${DEFINITELY_NOT_SET}\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "defaults.env.BAD_VAR")
}

func TestLoadConfig_EnvEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Nil(t, cfg.Env)
}

func TestLoadConfig_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg, err := LoadConfig()
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

	err := UpdateConfigFields(map[string]string{
		"setup_complete": "true",
	})
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
}

func TestUpdateConfigFields_TmuxConf(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	err := UpdateConfigFields(map[string]string{
		"defaults.tmux_conf": "default+host",
		"setup_complete":     "true",
	})
	require.NoError(t, err)

	cfg, err := LoadConfig()
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

	err := UpdateConfigFields(map[string]string{
		"setup_complete": "true",
	})
	require.NoError(t, err)

	// Re-read raw content to verify comment is preserved
	data, err := os.ReadFile(filepath.Join(yoloaiDir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Contains(t, string(data), "my preferred agent")
}

func TestLoadConfig_ExpandsEnvVars(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("YOLOAI_TEST_AGENT", "gemini")
	t.Setenv("YOLOAI_TEST_BACKEND", "tart")

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	content := "defaults:\n  agent: ${YOLOAI_TEST_AGENT}\n  backend: ${YOLOAI_TEST_BACKEND}\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "gemini", cfg.Agent)
	assert.Equal(t, "tart", cfg.Backend)
}

func TestLoadConfig_UnsetEnvVarError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	content := "defaults:\n  agent: ${YOLOAI_DEFINITELY_NOT_SET}\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "defaults.agent")
	assert.Contains(t, err.Error(), "YOLOAI_DEFINITELY_NOT_SET")
}

func TestLoadConfig_UnclosedBraceError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	content := "defaults:\n  backend: ${UNCLOSED\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "defaults.backend")
	assert.Contains(t, err.Error(), "unclosed")
}

func TestLoadConfig_BareVarNotExpanded(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("YOLOAI_TEST_VAR", "should-not-appear")

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))

	content := "defaults:\n  agent: $YOLOAI_TEST_VAR\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "$YOLOAI_TEST_VAR", cfg.Agent, "bare $VAR must not be expanded")
}

func TestSplitDottedPath(t *testing.T) {
	assert.Equal(t, []string{"a"}, splitDottedPath("a"))
	assert.Equal(t, []string{"a", "b"}, splitDottedPath("a.b"))
	assert.Equal(t, []string{"defaults", "tmux_conf"}, splitDottedPath("defaults.tmux_conf"))
}

func TestConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	p, err := ConfigPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmpDir, ".yoloai", "config.yaml"), p)
}

func TestReadConfigRaw_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	data, err := ReadConfigRaw()
	require.NoError(t, err)
	assert.Nil(t, data)
}

func TestReadConfigRaw_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	content := "setup_complete: true\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	data, err := ReadConfigRaw()
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

func TestGetConfigValue_Scalar(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	content := "setup_complete: true\ndefaults:\n  backend: seatbelt\n  tmux_conf: default+host\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	val, found, err := GetConfigValue("setup_complete")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "true", val)

	val, found, err = GetConfigValue("defaults.backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "seatbelt", val)
}

func TestGetConfigValue_Mapping(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	content := "defaults:\n  backend: docker\n  tmux_conf: default\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	val, found, err := GetConfigValue("defaults")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Contains(t, val, "backend: docker")
	assert.Contains(t, val, "tmux_conf: default")
}

func TestGetConfigValue_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	content := "setup_complete: true\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	// Unknown key: not found
	_, found, err := GetConfigValue("nonexistent")
	require.NoError(t, err)
	assert.False(t, found)

	_, found, err = GetConfigValue("defaults.nonexistent")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestGetConfigValue_FallsBackToDefault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	content := "setup_complete: true\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	// Known key not in file returns its default
	val, found, err := GetConfigValue("defaults.backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "docker", val)
}

func TestGetConfigValue_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Unknown key with no file: not found
	_, found, err := GetConfigValue("anything")
	require.NoError(t, err)
	assert.False(t, found)

	// Known key with no file: returns default
	val, found, err := GetConfigValue("defaults.backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "docker", val)
}

func TestGetEffectiveConfig_Defaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	out, err := GetEffectiveConfig()
	require.NoError(t, err)
	assert.Contains(t, out, "setup_complete: false")
	assert.Contains(t, out, "backend: docker")
	assert.Contains(t, out, "image:")
	assert.Contains(t, out, "tmux_conf:")
	assert.Contains(t, out, "agent: claude")
	assert.Contains(t, out, "env: {}")
}

func TestGetEffectiveConfig_WithOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	content := "setup_complete: true\ndefaults:\n  backend: tart\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	out, err := GetEffectiveConfig()
	require.NoError(t, err)
	assert.Contains(t, out, "setup_complete: true")
	assert.Contains(t, out, "backend: tart")
	// Defaults for unset keys still present
	assert.Contains(t, out, "image:")
	assert.Contains(t, out, "tmux_conf:")
}

func TestGetEffectiveConfig_ExtraKeys(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	content := "setup_complete: true\ncustom_key: myvalue\n"
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(content), 0600))

	out, err := GetEffectiveConfig()
	require.NoError(t, err)
	// Custom keys from the file should appear too
	assert.Contains(t, out, "custom_key: myvalue")
	// And defaults still present
	assert.Contains(t, out, "backend: docker")
}
