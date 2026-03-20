package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// configDir creates the defaults/ directory structure needed for config tests.
func configDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dir := filepath.Join(tmpDir, ".yoloai", "defaults")
	require.NoError(t, os.MkdirAll(dir, 0750))
	return dir
}

// globalConfigDir creates the ~/.yoloai/ directory structure for global config tests.
func globalConfigDir(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(dir, 0750))
	return dir
}

func TestLoadConfig_Default(t *testing.T) {
	dir := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "claude", cfg.Agent) // DefaultConfigYAML sets agent: claude
}

func TestLoadGlobalConfig_WithTmuxConf(t *testing.T) {
	dir := globalConfigDir(t)

	content := `tmux_conf: default+host
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "default+host", cfg.TmuxConf)
}

func TestLoadConfig_AgentDefault(t *testing.T) {
	dir := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "claude", cfg.Agent) // DefaultConfigYAML sets agent: claude
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

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
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	// DefaultConfigYAML has env: {} which parses to an empty (non-nil) map
	assert.Empty(t, cfg.Env)
}

func TestLoadGlobalConfig_ModelAliases(t *testing.T) {
	dir := globalConfigDir(t)

	content := "model_aliases:\n  sonnet: claude-sonnet-4-20250514\n  fast: claude-haiku-4-latest\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadGlobalConfig()
	require.NoError(t, err)
	require.Len(t, cfg.ModelAliases, 2)
	assert.Equal(t, "claude-sonnet-4-20250514", cfg.ModelAliases["sonnet"])
	assert.Equal(t, "claude-haiku-4-latest", cfg.ModelAliases["fast"])
}

func TestLoadGlobalConfig_ModelAliasesEmpty(t *testing.T) {
	dir := globalConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultGlobalConfigYAML), 0600))

	cfg, err := LoadGlobalConfig()
	require.NoError(t, err)
	assert.Nil(t, cfg.ModelAliases)
}

func TestLoadConfig_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.Agent)
}

func TestUpdateGlobalConfigFields_TmuxConf(t *testing.T) {
	dir := globalConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultGlobalConfigYAML), 0600))

	err := UpdateGlobalConfigFields(map[string]string{
		"tmux_conf": "default+host",
	})
	require.NoError(t, err)

	cfg, err := LoadGlobalConfig()
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
		"container_backend": "tart",
	})
	require.NoError(t, err)

	// Re-read raw content to verify comment is preserved
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Contains(t, string(data), "my preferred agent")
}

func TestDeleteConfigField_Scalar(t *testing.T) {
	dir := configDir(t)
	content := "container_backend: tart\nagent: gemini\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	require.NoError(t, DeleteConfigField("container_backend"))

	// backend should be gone from file, agent preserved
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.NotContains(t, string(data), "container_backend")
	assert.Contains(t, string(data), "agent: gemini")

	// GetConfigValue should fall back to default (empty string — auto-detect)
	val, found, err := GetConfigValue("container_backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "", val)
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
	content := "env:\n  FOO: bar\ncontainer_backend: tart\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	require.NoError(t, DeleteConfigField("env"))

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.NotContains(t, string(data), "FOO")
	assert.NotContains(t, string(data), "env")
	assert.Contains(t, string(data), "container_backend: tart")
}

func TestDeleteConfigField_NonexistentKey(t *testing.T) {
	dir := configDir(t)
	content := "container_backend: docker\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	// Should not error on missing key
	require.NoError(t, DeleteConfigField("nonexistent"))
}

func TestDeleteConfigField_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Should not error when config file doesn't exist
	require.NoError(t, DeleteConfigField("container_backend"))
}

func TestLoadConfig_ExpandsEnvVars(t *testing.T) {
	dir := configDir(t)
	t.Setenv("YOLOAI_TEST_AGENT", "gemini")
	t.Setenv("YOLOAI_TEST_BACKEND", "tart")

	content := "agent: ${YOLOAI_TEST_AGENT}\ncontainer_backend: ${YOLOAI_TEST_BACKEND}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "gemini", cfg.Agent)
	assert.Equal(t, "tart", cfg.ContainerBackend)
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

	content := "container_backend: ${UNCLOSED\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container_backend")
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

	p := ConfigPath()
	assert.Equal(t, filepath.Join(tmpDir, ".yoloai", "defaults", "config.yaml"), p)
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
	content := "container_backend: docker\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	data, err := ReadConfigRaw()
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

func TestGetConfigValue_Scalar(t *testing.T) {
	dir := configDir(t)
	content := "container_backend: seatbelt\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	val, found, err := GetConfigValue("container_backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "seatbelt", val)
}

func TestGetConfigValue_GlobalKey(t *testing.T) {
	dir := globalConfigDir(t)
	content := "tmux_conf: default+host\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	val, found, err := GetConfigValue("tmux_conf")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "default+host", val)
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
	content := "container_backend: docker\n"
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

	// Known key not in file returns its default (empty string for container_backend)
	val, found, err := GetConfigValue("container_backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "", val)
}

func TestGetConfigValue_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Unknown key with no file: not found
	_, found, err := GetConfigValue("anything")
	require.NoError(t, err)
	assert.False(t, found)

	// Known key with no file: returns default (empty string for container_backend)
	val, found, err := GetConfigValue("container_backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "", val)
}

func TestGetEffectiveConfig_Defaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	out, err := GetEffectiveConfig()
	require.NoError(t, err)
	assert.Contains(t, out, "container_backend:")
	assert.Contains(t, out, "image:")
	assert.Contains(t, out, "tmux_conf:")        // global default
	assert.Contains(t, out, "model_aliases: {}") // global default
	assert.Contains(t, out, "agent: claude")
	assert.Contains(t, out, "env: {}")
}

func TestGetEffectiveConfig_WithOverrides(t *testing.T) {
	dir := configDir(t)
	content := "container_backend: tart\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	out, err := GetEffectiveConfig()
	require.NoError(t, err)
	assert.Contains(t, out, "container_backend: tart")
	// Defaults for unset keys still present
	assert.Contains(t, out, "image:")
	assert.Contains(t, out, "tmux_conf:") // from global defaults
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
	assert.Contains(t, out, "container_backend:")
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	// DefaultConfigYAML has resources block with empty cpus/memory — parses to non-nil struct
	require.NotNil(t, cfg.Resources)
	assert.Empty(t, cfg.Resources.CPUs)
	assert.Empty(t, cfg.Resources.Memory)
}

func TestGlobalConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	p := GlobalConfigPath()
	assert.Equal(t, filepath.Join(tmpDir, ".yoloai", "config.yaml"), p)
}

func TestLoadGlobalConfig_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg, err := LoadGlobalConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.TmuxConf)
	assert.Nil(t, cfg.ModelAliases)
}

func TestLoadGlobalConfig_Default(t *testing.T) {
	dir := globalConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultGlobalConfigYAML), 0600))

	cfg, err := LoadGlobalConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.TmuxConf)
	assert.Nil(t, cfg.ModelAliases)
}

func TestLoadGlobalConfig_EnvExpansion(t *testing.T) {
	dir := globalConfigDir(t)
	t.Setenv("YOLOAI_TEST_TMUX", "default+host")

	content := "tmux_conf: ${YOLOAI_TEST_TMUX}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "default+host", cfg.TmuxConf)
}

func TestIsGlobalKey(t *testing.T) {
	assert.True(t, IsGlobalKey("tmux_conf"))
	assert.True(t, IsGlobalKey("model_aliases"))
	assert.True(t, IsGlobalKey("model_aliases.fast"))
	assert.False(t, IsGlobalKey("agent"))
	assert.False(t, IsGlobalKey("container_backend"))
	assert.False(t, IsGlobalKey("env"))
	assert.False(t, IsGlobalKey("env.FOO"))
}

func TestDeleteGlobalConfigField(t *testing.T) {
	dir := globalConfigDir(t)
	content := "tmux_conf: default\nmodel_aliases:\n  fast: haiku\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	require.NoError(t, DeleteGlobalConfigField("tmux_conf"))

	cfg, err := LoadGlobalConfig()
	require.NoError(t, err)
	assert.Empty(t, cfg.TmuxConf)
	assert.Equal(t, "haiku", cfg.ModelAliases["fast"])
}

func TestDeleteGlobalConfigField_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	require.NoError(t, DeleteGlobalConfigField("tmux_conf"))
}

func TestReadGlobalConfigRaw_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	data, err := ReadGlobalConfigRaw()
	require.NoError(t, err)
	assert.Nil(t, data)
}

func TestReadGlobalConfigRaw_ExistingFile(t *testing.T) {
	dir := globalConfigDir(t)
	content := "tmux_conf: default\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	data, err := ReadGlobalConfigRaw()
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

func TestLoadConfig_AgentFilesString(t *testing.T) {
	dir := configDir(t)
	home := filepath.Dir(filepath.Dir(dir)) // strip .yoloai/defaults to get HOME

	content := "agent_files: ~/my-agent-configs\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg.AgentFiles)
	assert.True(t, cfg.AgentFiles.IsStringForm())
	assert.Equal(t, filepath.Join(home, "my-agent-configs"), cfg.AgentFiles.BaseDir)
	assert.Nil(t, cfg.AgentFiles.Files)
}

func TestLoadConfig_AgentFilesList(t *testing.T) {
	dir := configDir(t)
	home := filepath.Dir(filepath.Dir(dir)) // strip .yoloai/defaults to get HOME

	content := "agent_files:\n  - ~/file1.json\n  - ~/file2.json\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg.AgentFiles)
	assert.False(t, cfg.AgentFiles.IsStringForm())
	assert.Empty(t, cfg.AgentFiles.BaseDir)
	require.Len(t, cfg.AgentFiles.Files, 2)
	assert.Equal(t, filepath.Join(home, "file1.json"), cfg.AgentFiles.Files[0])
	assert.Equal(t, filepath.Join(home, "file2.json"), cfg.AgentFiles.Files[1])
}

func TestLoadConfig_AgentFilesEnvExpansion(t *testing.T) {
	dir := configDir(t)
	t.Setenv("YOLOAI_AGENT_DIR", "/custom/path")

	content := "agent_files: ${YOLOAI_AGENT_DIR}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg.AgentFiles)
	assert.Equal(t, "/custom/path", cfg.AgentFiles.BaseDir)
}

func TestLoadConfig_RecipeFields(t *testing.T) {
	dir := configDir(t)

	content := "cap_add:\n  - NET_ADMIN\n  - SYS_PTRACE\ndevices:\n  - /dev/net/tun\nsetup:\n  - tailscale up --authkey=abc123\n  - echo done\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, []string{"NET_ADMIN", "SYS_PTRACE"}, cfg.CapAdd)
	assert.Equal(t, []string{"/dev/net/tun"}, cfg.Devices)
	assert.Equal(t, []string{"tailscale up --authkey=abc123", "echo done"}, cfg.Setup)
}

func TestLoadConfig_RecipeFieldsEnvExpansion(t *testing.T) {
	dir := configDir(t)
	t.Setenv("YOLOAI_TEST_KEY", "mykey123")

	content := "setup:\n  - tailscale up --authkey=${YOLOAI_TEST_KEY}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, []string{"tailscale up --authkey=mykey123"}, cfg.Setup)
}

func TestLoadConfig_Ports(t *testing.T) {
	dir := configDir(t)

	content := "ports:\n  - \"8080:8080\"\n  - \"3000:3000\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, []string{"8080:8080", "3000:3000"}, cfg.Ports)
}

func TestLoadConfig_PortsEmpty(t *testing.T) {
	dir := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Nil(t, cfg.Ports)
}

func TestLoadConfig_RecipeFieldsEmpty(t *testing.T) {
	dir := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Nil(t, cfg.CapAdd)
	assert.Nil(t, cfg.Devices)
	assert.Nil(t, cfg.Setup)
}

func TestLoadConfig_AgentFilesOmitted(t *testing.T) {
	dir := configDir(t)

	content := "agent: claude\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Nil(t, cfg.AgentFiles)
}

func TestLoadConfig_AutoCommitIntervalDefault(t *testing.T) {
	dir := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, 0, cfg.AutoCommitInterval)
}

func TestLoadConfig_AutoCommitIntervalExplicit(t *testing.T) {
	dir := configDir(t)

	content := "auto_commit_interval: 60\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, 60, cfg.AutoCommitInterval)
}

func TestLoadConfig_AutoCommitIntervalInvalid(t *testing.T) {
	dir := configDir(t)

	content := "auto_commit_interval: abc\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auto_commit_interval")
}

func TestGetConfigValue_AutoCommitInterval(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Known key with no file: returns default "0"
	val, found, err := GetConfigValue("auto_commit_interval")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "0", val)
}

// Helper to parse YAML and return the root mapping node.
func parseYAMLMapping(t *testing.T, input string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(input), &doc))
	require.Equal(t, yaml.DocumentNode, doc.Kind)
	require.NotEmpty(t, doc.Content)
	require.Equal(t, yaml.MappingNode, doc.Content[0].Kind)
	return doc.Content[0]
}

func TestFindYAMLValue_Found(t *testing.T) {
	root := parseYAMLMapping(t, "foo: bar\nbaz: qux\n")

	node := FindYAMLValue(root, "foo")
	require.NotNil(t, node)
	assert.Equal(t, "bar", node.Value)
}

func TestFindYAMLValue_NotFound(t *testing.T) {
	root := parseYAMLMapping(t, "foo: bar\n")

	node := FindYAMLValue(root, "missing")
	assert.Nil(t, node)
}

func TestFindYAMLValue_EmptyMapping(t *testing.T) {
	root := &yaml.Node{Kind: yaml.MappingNode}

	node := FindYAMLValue(root, "anything")
	assert.Nil(t, node)
}

func TestFindYAMLValue_CorrectNode(t *testing.T) {
	root := parseYAMLMapping(t, "a: 1\nb: 2\nc: 3\n")

	node := FindYAMLValue(root, "b")
	require.NotNil(t, node)
	assert.Equal(t, "2", node.Value)
}
