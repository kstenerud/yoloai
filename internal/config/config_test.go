package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// configDir creates the defaults/ directory structure needed for config tests.
// Returns the defaults dir path and a Layout rooted at the temp home.
func configDir(t *testing.T) (string, Layout) {
	t.Helper()
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, ".yoloai", "defaults")
	require.NoError(t, os.MkdirAll(dir, 0750))
	return dir, NewLayout(filepath.Join(tmpDir, ".yoloai"))
}

// globalConfigDir creates the ~/.yoloai/ directory structure for global config tests.
// Returns the yoloai dir path and a Layout rooted at the temp home.
func globalConfigDir(t *testing.T) (string, Layout) {
	t.Helper()
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(dir, 0750))
	return dir, NewLayout(dir)
}

func TestLoadConfig_Default(t *testing.T) {
	dir, layout := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "claude", cfg.Agent) // DefaultConfigYAML sets agent: claude
}

func TestLoadGlobalConfig_WithTmuxConf(t *testing.T) {
	dir, layout := globalConfigDir(t)

	content := `tmux_conf: default+host
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadGlobalConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "default+host", cfg.TmuxConf)
}

func TestLoadConfig_AgentDefault(t *testing.T) {
	dir, layout := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "claude", cfg.Agent) // DefaultConfigYAML sets agent: claude
}

func TestLoadConfig_AgentOverride(t *testing.T) {
	dir, layout := configDir(t)

	content := "agent: gemini\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "gemini", cfg.Agent)
}

func TestLoadConfig_ModelDefault(t *testing.T) {
	dir, layout := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Empty(t, cfg.Model) // Not set in file; empty means agent's default
}

func TestLoadConfig_ModelOverride(t *testing.T) {
	dir, layout := configDir(t)

	content := "model: o3\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "o3", cfg.Model)
}

func TestLoadConfig_EnvMap(t *testing.T) {
	dir, layout := configDir(t)

	content := "env:\n  OLLAMA_API_BASE: http://host.docker.internal:11434\n  CUSTOM_VAR: myvalue\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	require.Len(t, cfg.Env, 2)
	assert.Equal(t, "http://host.docker.internal:11434", cfg.Env["OLLAMA_API_BASE"])
	assert.Equal(t, "myvalue", cfg.Env["CUSTOM_VAR"])
}

func TestLoadConfig_EnvExpansion(t *testing.T) {
	dir, layout := configDir(t)
	// Only allowlisted interpolation vars (HOME/USER/LANG/TZ/LC_*) resolve in
	// config values now — see BREAKING-CHANGES v0.4.0.
	layout = layout.WithEnv(map[string]string{"HOME": "/home/tester"})

	content := "env:\n  PROJECT_DIR: ${HOME}/project\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	require.Len(t, cfg.Env, 1)
	assert.Equal(t, "/home/tester/project", cfg.Env["PROJECT_DIR"])
}

func TestLoadConfig_NonAllowlistedEnvVarNotInterpolated(t *testing.T) {
	dir, layout := configDir(t)
	// A var present in the host snapshot but NOT on the interpolation allowlist
	// must not resolve — config interpolation can't pull arbitrary host vars
	// (e.g. secrets) into config values. See BREAKING-CHANGES v0.4.0.
	layout = layout.WithEnv(map[string]string{"MY_SECRET": "s3cr3t"})

	content := "env:\n  LEAK: ${MY_SECRET}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig(layout)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MY_SECRET")
}

func TestLoadConfig_EnvExpansionError(t *testing.T) {
	dir, layout := configDir(t)

	content := "env:\n  BAD_VAR: ${DEFINITELY_NOT_SET}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig(layout)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "env.BAD_VAR")
}

func TestLoadConfig_EnvEmpty(t *testing.T) {
	dir, layout := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	// DefaultConfigYAML has env: {} which parses to an empty (non-nil) map
	assert.Empty(t, cfg.Env)
}

func TestLoadGlobalConfig_ModelAliases(t *testing.T) {
	dir, layout := globalConfigDir(t)

	content := "model_aliases:\n  sonnet: claude-sonnet-4-20250514\n  fast: claude-haiku-4-latest\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadGlobalConfig(layout)
	require.NoError(t, err)
	require.Len(t, cfg.ModelAliases, 2)
	assert.Equal(t, "claude-sonnet-4-20250514", cfg.ModelAliases["sonnet"])
	assert.Equal(t, "claude-haiku-4-latest", cfg.ModelAliases["fast"])
}

func TestLoadGlobalConfig_ModelAliasesEmpty(t *testing.T) {
	dir, layout := globalConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultGlobalConfigYAML), 0600))

	cfg, err := LoadGlobalConfig(layout)
	require.NoError(t, err)
	assert.Nil(t, cfg.ModelAliases)
}

func TestLoadConfig_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	layout := NewLayout(filepath.Join(tmpDir, ".yoloai"))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Empty(t, cfg.Agent)
}

func TestUpdateGlobalConfigFields_TmuxConf(t *testing.T) {
	dir, layout := globalConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultGlobalConfigYAML), 0600))

	err := UpdateGlobalConfigFields(layout, map[string]string{
		"tmux_conf": "default+host",
	})
	require.NoError(t, err)

	cfg, err := LoadGlobalConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "default+host", cfg.TmuxConf)
}

func TestUpdateConfigFields_PreservesComments(t *testing.T) {
	dir, layout := configDir(t)

	content := `# yoloai configuration
agent: claude # my preferred agent
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	err := UpdateConfigFields(layout, map[string]string{
		"container_backend": "tart",
	})
	require.NoError(t, err)

	// Re-read raw content to verify comment is preserved
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.Contains(t, string(data), "my preferred agent")
}

func TestDeleteConfigField_Scalar(t *testing.T) {
	dir, layout := configDir(t)
	content := "container_backend: tart\nagent: gemini\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	require.NoError(t, DeleteConfigField(layout, "container_backend"))

	// backend should be gone from file, agent preserved
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.NotContains(t, string(data), "container_backend")
	assert.Contains(t, string(data), "agent: gemini")

	// GetConfigValue should fall back to default (empty string — auto-detect)
	val, found, err := GetConfigValue(layout, "container_backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "", val)
}

func TestDeleteConfigField_MapEntry(t *testing.T) {
	dir, layout := configDir(t)
	content := "env:\n  FOO: bar\n  BAZ: qux\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	require.NoError(t, DeleteConfigField(layout, "env.FOO"))

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.NotContains(t, string(data), "FOO")
	assert.Contains(t, string(data), "BAZ: qux")
}

func TestDeleteConfigField_EntireSection(t *testing.T) {
	dir, layout := configDir(t)
	content := "env:\n  FOO: bar\ncontainer_backend: tart\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	require.NoError(t, DeleteConfigField(layout, "env"))

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml")) //nolint:gosec // G304: test code
	require.NoError(t, err)
	assert.NotContains(t, string(data), "FOO")
	assert.NotContains(t, string(data), "env")
	assert.Contains(t, string(data), "container_backend: tart")
}

func TestDeleteConfigField_NonexistentKey(t *testing.T) {
	dir, layout := configDir(t)
	content := "container_backend: docker\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	// Should not error on missing key
	require.NoError(t, DeleteConfigField(layout, "nonexistent"))
}

func TestDeleteConfigField_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	layout := NewLayout(filepath.Join(tmpDir, ".yoloai"))

	// Should not error when config file doesn't exist
	require.NoError(t, DeleteConfigField(layout, "container_backend"))
}

func TestLoadConfig_ExpandsEnvVars(t *testing.T) {
	dir, layout := configDir(t)
	// Scalar-field handler expands an allowlisted interpolation var (USER).
	layout = layout.WithEnv(map[string]string{"USER": "claude"})

	content := "agent: ${USER}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "claude", cfg.Agent)
}

func TestLoadConfig_UnsetEnvVarError(t *testing.T) {
	dir, layout := configDir(t)

	content := "agent: ${YOLOAI_DEFINITELY_NOT_SET}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig(layout)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent")
	assert.Contains(t, err.Error(), "YOLOAI_DEFINITELY_NOT_SET")
}

func TestLoadConfig_UnclosedBraceError(t *testing.T) {
	dir, layout := configDir(t)

	content := "container_backend: ${UNCLOSED\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig(layout)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container_backend")
	assert.Contains(t, err.Error(), "unclosed")
}

func TestLoadConfig_BareVarNotExpanded(t *testing.T) {
	dir, layout := configDir(t)
	// bare $VAR is never expanded regardless of env; env map not needed

	content := "agent: $YOLOAI_TEST_VAR\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "$YOLOAI_TEST_VAR", cfg.Agent, "bare $VAR must not be expanded")
}

func TestSplitDottedPath(t *testing.T) {
	assert.Equal(t, []string{"a"}, splitDottedPath("a"))
	assert.Equal(t, []string{"a", "b"}, splitDottedPath("a.b"))
	assert.Equal(t, []string{"tart", "image"}, splitDottedPath("tart.image"))
}

func TestDefaultsConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	layout := NewLayout(filepath.Join(tmpDir, ".yoloai"))

	p := layout.DefaultsConfigPath()
	assert.Equal(t, filepath.Join(tmpDir, ".yoloai", "defaults", "config.yaml"), p)
}

func TestReadConfigRaw_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	layout := NewLayout(filepath.Join(tmpDir, ".yoloai"))

	data, err := ReadConfigRaw(layout)
	require.NoError(t, err)
	assert.Nil(t, data)
}

func TestReadConfigRaw_ExistingFile(t *testing.T) {
	dir, layout := configDir(t)
	content := "container_backend: docker\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	data, err := ReadConfigRaw(layout)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

func TestGetConfigValue_Scalar(t *testing.T) {
	dir, layout := configDir(t)
	content := "container_backend: seatbelt\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	val, found, err := GetConfigValue(layout, "container_backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "seatbelt", val)
}

func TestGetConfigValue_GlobalKey(t *testing.T) {
	dir, layout := globalConfigDir(t)
	content := "tmux_conf: default+host\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	val, found, err := GetConfigValue(layout, "tmux_conf")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "default+host", val)
}

func TestGetConfigValue_Mapping(t *testing.T) {
	dir, layout := configDir(t)
	content := "tart:\n  image: myimage\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	val, found, err := GetConfigValue(layout, "tart")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Contains(t, val, "image: myimage")
}

func TestGetConfigValue_NotFound(t *testing.T) {
	dir, layout := configDir(t)
	content := "container_backend: docker\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	// Unknown key: not found
	_, found, err := GetConfigValue(layout, "nonexistent")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestGetConfigValue_FallsBackToDefault(t *testing.T) {
	dir, layout := configDir(t)
	content := "agent: claude\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	// Known key not in file returns its default (empty string for container_backend)
	val, found, err := GetConfigValue(layout, "container_backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "", val)
}

func TestGetConfigValue_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	layout := NewLayout(filepath.Join(tmpDir, ".yoloai"))

	// Unknown key with no file: not found
	_, found, err := GetConfigValue(layout, "anything")
	require.NoError(t, err)
	assert.False(t, found)

	// Known key with no file: returns default (empty string for container_backend)
	val, found, err := GetConfigValue(layout, "container_backend")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "", val)
}

func TestGetEffectiveConfig_Defaults(t *testing.T) {
	tmpDir := t.TempDir()
	layout := NewLayout(filepath.Join(tmpDir, ".yoloai"))

	out, err := GetEffectiveConfig(layout)
	require.NoError(t, err)
	assert.Contains(t, out, "container_backend:")
	assert.Contains(t, out, "image:")
	assert.Contains(t, out, "tmux_conf:")        // global default
	assert.Contains(t, out, "model_aliases: {}") // global default
	assert.Contains(t, out, "agent: claude")
	assert.Contains(t, out, "env: {}")
}

func TestGetEffectiveConfig_WithOverrides(t *testing.T) {
	dir, layout := configDir(t)
	content := "container_backend: tart\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	out, err := GetEffectiveConfig(layout)
	require.NoError(t, err)
	assert.Contains(t, out, "container_backend: tart")
	// Defaults for unset keys still present
	assert.Contains(t, out, "image:")
	assert.Contains(t, out, "tmux_conf:") // from global defaults
}

func TestGetEffectiveConfig_MalformedYAMLIsError(t *testing.T) {
	dir, layout := configDir(t)
	// A config file the user wrote but that fails to parse must surface as an
	// error, not be silently dropped from the effective view.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("container_backend: [unterminated\n"), 0600))

	_, err := GetEffectiveConfig(layout)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse config YAML")
}

func TestGetEffectiveConfig_ExtraKeys(t *testing.T) {
	dir, layout := configDir(t)
	content := "custom_key: myvalue\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	out, err := GetEffectiveConfig(layout)
	require.NoError(t, err)
	// Custom keys from the file should appear too
	assert.Contains(t, out, "custom_key: myvalue")
	// And defaults still present
	assert.Contains(t, out, "container_backend:")
}

func TestLoadConfig_Resources(t *testing.T) {
	dir, layout := configDir(t)

	content := "resources:\n  cpus: \"4\"\n  memory: 8g\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	require.NotNil(t, cfg.Resources)
	assert.Equal(t, "4", cfg.Resources.CPUs)
	assert.Equal(t, "8g", cfg.Resources.Memory)
}

func TestLoadConfig_ResourcesPartial(t *testing.T) {
	dir, layout := configDir(t)

	content := "resources:\n  memory: 4g\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	require.NotNil(t, cfg.Resources)
	assert.Empty(t, cfg.Resources.CPUs)
	assert.Equal(t, "4g", cfg.Resources.Memory)
}

func TestLoadConfig_ResourcesEmpty(t *testing.T) {
	dir, layout := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	// DefaultConfigYAML has resources block with empty cpus/memory — parses to non-nil struct
	require.NotNil(t, cfg.Resources)
	assert.Empty(t, cfg.Resources.CPUs)
	assert.Empty(t, cfg.Resources.Memory)
}

func TestLoadGlobalConfig_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	layout := NewLayout(filepath.Join(tmpDir, ".yoloai"))

	cfg, err := LoadGlobalConfig(layout)
	require.NoError(t, err)
	// A missing config materializes the declared default from globalKnownSettings.
	assert.Equal(t, "default+host", cfg.TmuxConf)
	assert.Nil(t, cfg.ModelAliases)
}

func TestLoadGlobalConfig_Default(t *testing.T) {
	dir, layout := globalConfigDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultGlobalConfigYAML), 0600))

	cfg, err := LoadGlobalConfig(layout)
	require.NoError(t, err)
	// The default template leaves tmux_conf unset, so it materializes to the
	// declared default rather than empty.
	assert.Equal(t, "default+host", cfg.TmuxConf)
	assert.Nil(t, cfg.ModelAliases)
}

func TestLoadGlobalConfig_EnvExpansion(t *testing.T) {
	dir, layout := globalConfigDir(t)
	layout = layout.WithEnv(map[string]string{"HOME": "/home/tester"})

	content := "tmux_conf: ${HOME}/.tmux.conf\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadGlobalConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "/home/tester/.tmux.conf", cfg.TmuxConf)
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

func TestIsKnownConfigPath(t *testing.T) {
	valid := []string{
		"agent",
		"container_backend",
		"tart",
		"tart.image",
		"resources",
		"resources.cpus",
		"network",
		"network.allow",
		"agent_files",
		"env",
		"env.OLLAMA_API_BASE",
		"agent_args.claude",
		"model_aliases.fast",
		"tmux_conf",
	}
	for _, path := range valid {
		assert.True(t, IsKnownConfigPath(path), path)
	}

	invalid := []string{
		"",
		"backend",
		"tart.foo",
		"resources.gpu",
		"network.foo",
		"mounts.foo",
		"env.",
		"model_aliases.fast.extra",
	}
	for _, path := range invalid {
		assert.False(t, IsKnownConfigPath(path), path)
	}
}

func TestIsSettableConfigPath(t *testing.T) {
	valid := []string{
		"agent",
		"container_backend",
		"tart.image",
		"resources.cpus",
		"network.isolated",
		"agent_files",
		"env.OLLAMA_API_BASE",
		"agent_args.claude",
		"model_aliases.fast",
		"tmux_conf",
	}
	for _, path := range valid {
		assert.True(t, IsSettableConfigPath(path), path)
	}

	invalid := []string{
		"",
		"backend",
		"tart",
		"resources",
		"network",
		"network.allow",
		"mounts",
		"env.",
		"model_aliases.fast.extra",
	}
	for _, path := range invalid {
		assert.False(t, IsSettableConfigPath(path), path)
	}
}

func TestDeleteGlobalConfigField(t *testing.T) {
	dir, layout := globalConfigDir(t)
	content := "tmux_conf: default\nmodel_aliases:\n  fast: haiku\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	require.NoError(t, DeleteGlobalConfigField(layout, "tmux_conf"))

	cfg, err := LoadGlobalConfig(layout)
	require.NoError(t, err)
	// With tmux_conf deleted the next load falls back to the declared default.
	assert.Equal(t, "default+host", cfg.TmuxConf)
	assert.Equal(t, "haiku", cfg.ModelAliases["fast"])
}

func TestDeleteGlobalConfigField_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	layout := NewLayout(filepath.Join(tmpDir, ".yoloai"))

	require.NoError(t, DeleteGlobalConfigField(layout, "tmux_conf"))
}

func TestReadGlobalConfigRaw_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	layout := NewLayout(filepath.Join(tmpDir, ".yoloai"))

	data, err := ReadGlobalConfigRaw(layout)
	require.NoError(t, err)
	assert.Nil(t, data)
}

func TestReadGlobalConfigRaw_ExistingFile(t *testing.T) {
	dir, layout := globalConfigDir(t)
	content := "tmux_conf: default\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	data, err := ReadGlobalConfigRaw(layout)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

func TestLoadConfig_AgentFilesString(t *testing.T) {
	dir, layout := configDir(t)

	content := "agent_files: ~/my-agent-configs\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	require.NotNil(t, cfg.AgentFiles)
	assert.True(t, cfg.AgentFiles.IsStringForm())
	// Paths are stored raw; call ExpandAgentFiles to expand
	assert.Equal(t, "~/my-agent-configs", cfg.AgentFiles.BaseDir)
	assert.Nil(t, cfg.AgentFiles.Files)

	// Verify ExpandAgentFiles expands correctly
	home := layout.HomeDir
	expanded, err := ExpandAgentFiles(cfg.AgentFiles, home, nil)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "my-agent-configs"), expanded.BaseDir)
}

func TestLoadConfig_AgentFilesList(t *testing.T) {
	dir, layout := configDir(t)

	content := "agent_files:\n  - ~/file1.json\n  - ~/file2.json\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	require.NotNil(t, cfg.AgentFiles)
	assert.False(t, cfg.AgentFiles.IsStringForm())
	assert.Empty(t, cfg.AgentFiles.BaseDir)
	// Paths are stored raw; call ExpandAgentFiles to expand
	require.Len(t, cfg.AgentFiles.Files, 2)
	assert.Equal(t, "~/file1.json", cfg.AgentFiles.Files[0])
	assert.Equal(t, "~/file2.json", cfg.AgentFiles.Files[1])

	// Verify ExpandAgentFiles expands correctly
	home := layout.HomeDir
	expanded, err := ExpandAgentFiles(cfg.AgentFiles, home, nil)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "file1.json"), expanded.Files[0])
	assert.Equal(t, filepath.Join(home, "file2.json"), expanded.Files[1])
}

func TestLoadConfig_AgentFilesEnvExpansion(t *testing.T) {
	dir, layout := configDir(t)

	content := "agent_files: ${YOLOAI_AGENT_DIR}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	require.NotNil(t, cfg.AgentFiles)
	// Env vars are expanded via ExpandAgentFiles; pass explicit env map instead of relying on process env.
	env := map[string]string{"YOLOAI_AGENT_DIR": "/custom/path"}
	expanded, err := ExpandAgentFiles(cfg.AgentFiles, layout.HomeDir, env)
	require.NoError(t, err)
	assert.Equal(t, "/custom/path", expanded.BaseDir)
}

func TestLoadConfig_RecipeFields(t *testing.T) {
	dir, layout := configDir(t)

	content := "cap_add:\n  - NET_ADMIN\n  - SYS_PTRACE\ndevices:\n  - /dev/net/tun\nsetup:\n  - tailscale up --authkey=abc123\n  - echo done\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, []string{"NET_ADMIN", "SYS_PTRACE"}, cfg.CapAdd)
	assert.Equal(t, []string{"/dev/net/tun"}, cfg.Devices)
	assert.Equal(t, []string{"tailscale up --authkey=abc123", "echo done"}, cfg.Setup)
}

func TestLoadConfig_RecipeFieldsEnvExpansion(t *testing.T) {
	dir, layout := configDir(t)
	// Sequence-field handler expands an allowlisted interpolation var (HOME).
	layout = layout.WithEnv(map[string]string{"HOME": "/home/tester"})

	content := "setup:\n  - cp ${HOME}/.netrc /tmp/\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, []string{"cp /home/tester/.netrc /tmp/"}, cfg.Setup)
}

func TestLoadConfig_Ports(t *testing.T) {
	dir, layout := configDir(t)

	content := "ports:\n  - \"8080:8080\"\n  - \"3000:3000\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, []string{"8080:8080", "3000:3000"}, cfg.Ports)
}

func TestLoadConfig_PortsEmpty(t *testing.T) {
	dir, layout := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Nil(t, cfg.Ports)
}

func TestLoadConfig_RecipeFieldsEmpty(t *testing.T) {
	dir, layout := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Nil(t, cfg.CapAdd)
	assert.Nil(t, cfg.Devices)
	assert.Nil(t, cfg.Setup)
}

func TestLoadConfig_AgentFilesOmitted(t *testing.T) {
	dir, layout := configDir(t)

	content := "agent: claude\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Nil(t, cfg.AgentFiles)
}

func TestLoadConfig_AutoCommitIntervalDefault(t *testing.T) {
	dir, layout := configDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(DefaultConfigYAML), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, 0, cfg.AutoCommitInterval)
}

func TestLoadConfig_AutoCommitIntervalExplicit(t *testing.T) {
	dir, layout := configDir(t)

	content := "auto_commit_interval: 60\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	cfg, err := LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, 60, cfg.AutoCommitInterval)
}

func TestLoadConfig_AutoCommitIntervalInvalid(t *testing.T) {
	dir, layout := configDir(t)

	content := "auto_commit_interval: abc\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600))

	_, err := LoadConfig(layout)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auto_commit_interval")
}

func TestGetConfigValue_AutoCommitInterval(t *testing.T) {
	tmpDir := t.TempDir()
	layout := NewLayout(filepath.Join(tmpDir, ".yoloai"))

	// Known key with no file: returns default "0"
	val, found, err := GetConfigValue(layout, "auto_commit_interval")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "0", val)
}
