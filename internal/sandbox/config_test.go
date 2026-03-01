package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// configDir creates the profiles/base/ directory structure needed for config tests.
func configDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dir := filepath.Join(tmpDir, ".yoloai", "profiles", "base")
	require.NoError(t, os.MkdirAll(dir, 0750))
	return dir
}

func TestLoadConfig_Default(t *testing.T) {
	dir := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.TmuxConf)
}

func TestLoadConfig_WithTmuxConf(t *testing.T) {
	dir := configDir(t)

	content := `tmux_conf: default+host
agent: claude
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "default+host", cfg.TmuxConf)
	assert.Equal(t, "claude", cfg.Agent)
}

func TestLoadConfig_AgentDefault(t *testing.T) {
	dir := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.Agent) // Not set in file; caller uses knownSettings default
}

func TestLoadConfig_AgentOverride(t *testing.T) {
	dir := configDir(t)

	content := "agent: gemini\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "gemini", cfg.Agent)
}

func TestLoadConfig_ModelDefault(t *testing.T) {
	dir := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.Model) // Not set in file; empty means agent's default
}

func TestLoadConfig_ModelOverride(t *testing.T) {
	dir := configDir(t)

	content := "model: o3\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "o3", cfg.Model)
}

func TestLoadConfig_EnvMap(t *testing.T) {
	dir := configDir(t)

	content := "env:\n  OLLAMA_API_BASE: http://host.docker.internal:11434\n  CUSTOM_VAR: myvalue\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Len(t, cfg.Env, 2)
	assert.Equal(t, "http://host.docker.internal:11434", cfg.Env["OLLAMA_API_BASE"])
	assert.Equal(t, "myvalue", cfg.Env["CUSTOM_VAR"])
}

func TestLoadConfig_EnvExpansion(t *testing.T) {
	dir := configDir(t)
	t.Setenv("YOLOAI_TEST_HOST", "localhost")

	content := "env:\n  API_BASE: http://${YOLOAI_TEST_HOST}:11434\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Len(t, cfg.Env, 1)
	assert.Equal(t, "http://localhost:11434", cfg.Env["API_BASE"])
}

func TestLoadConfig_EnvExpansionError(t *testing.T) {
	dir := configDir(t)

	content := "env:\n  BAD_VAR: ${DEFINITELY_NOT_SET}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "env.BAD_VAR")
}

func TestLoadConfig_EnvEmpty(t *testing.T) {
	dir := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Nil(t, cfg.Env)
}

func TestLoadConfig_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.TmuxConf)
}

func TestUpdateConfigFields_TmuxConf(t *testing.T) {
	dir := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	err := UpdateConfigFields(map[string]string{
		"tmux_conf": "default+host",
	})
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "default+host", cfg.TmuxConf)
}

func TestUpdateConfigFields_PreservesComments(t *testing.T) {
	dir := configDir(t)

	content := `# yoloai configuration
agent: claude # my preferred agent
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	err := UpdateConfigFields(map[string]string{
		"backend": "tart",
	})
	require.NoError(t, err)

	// Re-read raw content to verify comment is preserved
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Contains(t, string(data), "my preferred agent")
}

func TestDeleteConfigField_Scalar(t *testing.T) {
	dir := configDir(t)
	content := "backend: tart\nagent: gemini\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	require.NoError(t, DeleteConfigField("backend"))

	// backend should be gone from file, agent preserved
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.NotContains(t, string(data), "backend")
	assert.Contains(t, string(data), "agent: gemini")

	// GetConfigValue should fall back to default
	val, found, err := GetConfigValue("backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "docker", val)
}

func TestDeleteConfigField_MapEntry(t *testing.T) {
	dir := configDir(t)
	content := "env:\n  FOO: bar\n  BAZ: qux\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	require.NoError(t, DeleteConfigField("env.FOO"))

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.NotContains(t, string(data), "FOO")
	assert.Contains(t, string(data), "BAZ: qux")
}

func TestDeleteConfigField_EntireSection(t *testing.T) {
	dir := configDir(t)
	content := "env:\n  FOO: bar\nbackend: tart\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	require.NoError(t, DeleteConfigField("env"))

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.NotContains(t, string(data), "FOO")
	assert.NotContains(t, string(data), "env")
	assert.Contains(t, string(data), "backend: tart")
}

func TestDeleteConfigField_NonexistentKey(t *testing.T) {
	dir := configDir(t)
	content := "backend: docker\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	// Should not error on missing key
	require.NoError(t, DeleteConfigField("nonexistent"))
}

func TestDeleteConfigField_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Should not error when config file doesn't exist
	require.NoError(t, DeleteConfigField("backend"))
}

func TestLoadConfig_ExpandsEnvVars(t *testing.T) {
	dir := configDir(t)
	t.Setenv("YOLOAI_TEST_AGENT", "gemini")
	t.Setenv("YOLOAI_TEST_BACKEND", "tart")

	content := "agent: ${YOLOAI_TEST_AGENT}\nbackend: ${YOLOAI_TEST_BACKEND}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "gemini", cfg.Agent)
	assert.Equal(t, "tart", cfg.Backend)
}

func TestLoadConfig_UnsetEnvVarError(t *testing.T) {
	dir := configDir(t)

	content := "agent: ${YOLOAI_DEFINITELY_NOT_SET}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent")
	assert.Contains(t, err.Error(), "YOLOAI_DEFINITELY_NOT_SET")
}

func TestLoadConfig_UnclosedBraceError(t *testing.T) {
	dir := configDir(t)

	content := "backend: ${UNCLOSED\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend")
	assert.Contains(t, err.Error(), "unclosed")
}

func TestLoadConfig_BareVarNotExpanded(t *testing.T) {
	dir := configDir(t)
	t.Setenv("YOLOAI_TEST_VAR", "should-not-appear")

	content := "agent: $YOLOAI_TEST_VAR\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "$YOLOAI_TEST_VAR", cfg.Agent, "bare $VAR must not be expanded")
}

func TestSplitDottedPath(t *testing.T) {
	assert.Equal(t, []string{"a"}, splitDottedPath("a"))
	assert.Equal(t, []string{"a", "b"}, splitDottedPath("a.b"))
	assert.Equal(t, []string{"tart", "image"}, splitDottedPath("tart.image"))
}

func TestConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	p, err := ConfigPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(tmpDir, ".yoloai", "profiles", "base", "config.yaml"), p)
}

func TestReadConfigRaw_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	data, err := ReadConfigRaw()
	require.NoError(t, err)
	assert.Nil(t, data)
}

func TestReadConfigRaw_ExistingFile(t *testing.T) {
	dir := configDir(t)
	content := "backend: docker\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	data, err := ReadConfigRaw()
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

func TestGetConfigValue_Scalar(t *testing.T) {
	dir := configDir(t)
	content := "backend: seatbelt\ntmux_conf: default+host\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	val, found, err := GetConfigValue("backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "seatbelt", val)
}

func TestGetConfigValue_Mapping(t *testing.T) {
	dir := configDir(t)
	content := "tart:\n  image: myimage\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	val, found, err := GetConfigValue("tart")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Contains(t, val, "image: myimage")
}

func TestGetConfigValue_NotFound(t *testing.T) {
	dir := configDir(t)
	content := "backend: docker\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	// Unknown key: not found
	_, found, err := GetConfigValue("nonexistent")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestGetConfigValue_FallsBackToDefault(t *testing.T) {
	dir := configDir(t)
	content := "agent: claude\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	// Known key not in file returns its default
	val, found, err := GetConfigValue("backend")
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
	val, found, err := GetConfigValue("backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "docker", val)
}

func TestGetEffectiveConfig_Defaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	out, err := GetEffectiveConfig()
	require.NoError(t, err)
	assert.Contains(t, out, "backend: docker")
	assert.Contains(t, out, "image:")
	assert.Contains(t, out, "tmux_conf:")
	assert.Contains(t, out, "agent: claude")
	assert.Contains(t, out, "env: {}")
}

func TestGetEffectiveConfig_WithOverrides(t *testing.T) {
	dir := configDir(t)
	content := "backend: tart\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	out, err := GetEffectiveConfig()
	require.NoError(t, err)
	assert.Contains(t, out, "backend: tart")
	// Defaults for unset keys still present
	assert.Contains(t, out, "image:")
	assert.Contains(t, out, "tmux_conf:")
}

func TestGetEffectiveConfig_ExtraKeys(t *testing.T) {
	dir := configDir(t)
	content := "custom_key: myvalue\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	out, err := GetEffectiveConfig()
	require.NoError(t, err)
	// Custom keys from the file should appear too
	assert.Contains(t, out, "custom_key: myvalue")
	// And defaults still present
	assert.Contains(t, out, "backend: docker")
}

func TestLoadConfig_Resources(t *testing.T) {
	dir := configDir(t)

	content := "resources:\n  cpus: \"4\"\n  memory: 8g\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg.Resources)
	assert.Equal(t, "4", cfg.Resources.CPUs)
	assert.Equal(t, "8g", cfg.Resources.Memory)
}

func TestLoadConfig_ResourcesPartial(t *testing.T) {
	dir := configDir(t)

	content := "resources:\n  memory: 4g\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg.Resources)
	assert.Empty(t, cfg.Resources.CPUs)
	assert.Equal(t, "4g", cfg.Resources.Memory)
}

func TestLoadConfig_ResourcesEmpty(t *testing.T) {
	dir := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Nil(t, cfg.Resources)
}
