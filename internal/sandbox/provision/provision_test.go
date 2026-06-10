package provision

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// HasAnyAPIKey tests

func TestHasAnyAPIKey_Set(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	hostEnv := config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test-123"})

	assert.True(t, HasAnyAPIKey(agentDef, hostEnv))
}

func TestHasAnyAPIKey_Unset(t *testing.T) {
	agentDef := agent.GetAgent("claude")

	assert.False(t, HasAnyAPIKey(agentDef, config.Layout{}))
}

func TestHasAnyAPIKey_EmptyList(t *testing.T) {
	agentDef := agent.GetAgent("test")
	assert.True(t, HasAnyAPIKey(agentDef, config.Layout{})) // no API key required = always true
}

// HasAnyAuthFile tests

func TestHasAnyAuthFile_Exists(t *testing.T) {
	tmpDir := t.TempDir()

	agentDef := agent.GetAgent("claude")

	// Create the credentials file
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{}`), 0600))

	assert.True(t, HasAnyAuthFile(agentDef, tmpDir))
}

func TestHasAnyAuthFile_Missing(t *testing.T) {
	tmpDir := t.TempDir()

	agentDef := agent.GetAgent("claude")
	assert.False(t, HasAnyAuthFile(agentDef, tmpDir))
}

func TestHasAnyAuthFile_NoAuthFiles(t *testing.T) {
	agentDef := agent.GetAgent("test")
	assert.False(t, HasAnyAuthFile(agentDef, "/home/user"))
}

func TestHasAnyAuthFile_KeychainFallback(t *testing.T) {
	tmpDir := t.TempDir()

	// No credentials file on disk
	agentDef := agent.GetAgent("claude")

	// Override KeychainReader to return credentials
	origReader := KeychainReader
	KeychainReader = func(service string) ([]byte, error) {
		if service == "Claude Code-credentials" {
			return []byte(`{"token":"from-keychain"}`), nil
		}
		return nil, fmt.Errorf("not found")
	}
	defer func() { KeychainReader = origReader }()

	assert.True(t, HasAnyAuthFile(agentDef, tmpDir))
}

func TestHasAnyAuthFile_KeychainFallbackFails(t *testing.T) {
	tmpDir := t.TempDir()

	agentDef := agent.GetAgent("claude")

	// Override KeychainReader to always fail
	origReader := KeychainReader
	KeychainReader = func(_ string) ([]byte, error) {
		return nil, fmt.Errorf("not found")
	}
	defer func() { KeychainReader = origReader }()

	assert.False(t, HasAnyAuthFile(agentDef, tmpDir))
}

// DescribeSeedAuthFiles tests

func TestDescribeSeedAuthFiles_Claude(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	desc := DescribeSeedAuthFiles(agentDef)
	assert.Contains(t, desc, ".credentials.json")
}

func TestDescribeSeedAuthFiles_NoAuthFiles(t *testing.T) {
	agentDef := agent.GetAgent("test")
	assert.Empty(t, DescribeSeedAuthFiles(agentDef))
}

// CreateSecretsDir tests

func TestCreateSecretsDir_WithKey(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	hostEnv := config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test-secret"})

	dir, err := CreateSecretsDir(agentDef, nil, hostEnv, "")
	require.NoError(t, err)
	require.NotEmpty(t, dir)
	defer os.RemoveAll(dir) //nolint:errcheck

	content, err := os.ReadFile(filepath.Join(dir, "ANTHROPIC_API_KEY")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "sk-test-secret", string(content))
}

func TestCreateSecretsDir_NoKey(t *testing.T) {
	agentDef := agent.GetAgent("claude")

	dir, err := CreateSecretsDir(agentDef, nil, config.Layout{}, "")
	require.NoError(t, err)
	assert.Empty(t, dir)
}

func TestCreateSecretsDir_NoEnvVars(t *testing.T) {
	agentDef := agent.GetAgent("test")

	dir, err := CreateSecretsDir(agentDef, nil, config.Layout{}, "")
	require.NoError(t, err)
	assert.Empty(t, dir)
}

func TestCreateSecretsDir_WithEnvVars(t *testing.T) {
	agentDef := agent.GetAgent("test") // no API keys
	envVars := map[string]string{
		"OLLAMA_API_BASE": "http://host.docker.internal:11434",
		"CUSTOM_VAR":      "myvalue",
	}

	dir, err := CreateSecretsDir(agentDef, envVars, config.Layout{}, "")
	require.NoError(t, err)
	require.NotEmpty(t, dir)
	defer os.RemoveAll(dir) //nolint:errcheck

	content, err := os.ReadFile(filepath.Join(dir, "OLLAMA_API_BASE")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "http://host.docker.internal:11434", string(content))

	content, err = os.ReadFile(filepath.Join(dir, "CUSTOM_VAR")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "myvalue", string(content))
}

func TestCreateSecretsDir_APIKeyOverridesEnv(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	envVars := map[string]string{
		"ANTHROPIC_API_KEY": "should-be-overwritten",
	}
	hostEnv := config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-real-key"})

	dir, err := CreateSecretsDir(agentDef, envVars, hostEnv, "")
	require.NoError(t, err)
	require.NotEmpty(t, dir)
	defer os.RemoveAll(dir) //nolint:errcheck

	content, err := os.ReadFile(filepath.Join(dir, "ANTHROPIC_API_KEY")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "sk-real-key", string(content), "API key should override env var")
}

func TestCreateSecretsDir_EmptyBoth(t *testing.T) {
	agentDef := agent.GetAgent("test")

	dir, err := CreateSecretsDir(agentDef, map[string]string{}, config.Layout{}, "")
	require.NoError(t, err)
	assert.Empty(t, dir)
}

func TestCreateSecretsDir_HonorsStagingRoot(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	hostEnv := config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test-secret"})
	stagingRoot := t.TempDir()

	dir, err := CreateSecretsDir(agentDef, nil, hostEnv, stagingRoot)
	require.NoError(t, err)
	require.NotEmpty(t, dir)
	defer os.RemoveAll(dir) //nolint:errcheck

	rootResolved, err := filepath.EvalSymlinks(stagingRoot)
	require.NoError(t, err)
	dirResolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	assert.Equal(t, rootResolved, filepath.Dir(dirResolved),
		"secrets dir must be created under the injected staging root")
}

// CopySeedFiles tests

func TestCopySeedFiles_CopiesExistingFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create seed files on host
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"s":1}`), 0600))

	// Create sandbox dir structure
	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")
	copied, err := CopySeedFiles(agentDef, sandboxDir, true, tmpDir, config.Layout{})
	require.NoError(t, err)
	assert.False(t, copied) // copied only tracks auth-only files; settings.json is not auth-only

	// settings.json should be in agent-runtime (not auth-only)
	assert.FileExists(t, filepath.Join(sandboxDir, store.AgentRuntimeDir, "settings.json"))
}

func TestCopySeedFiles_SkipsAuthWhenAPIKeySet(t *testing.T) {
	tmpDir := t.TempDir()

	// Create auth file
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{}`), 0600))

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")
	_, err := CopySeedFiles(agentDef, sandboxDir, true, tmpDir, config.Layout{}) // hasAPIKey=true
	require.NoError(t, err)

	// Auth-only file should NOT be copied when API key is set
	assert.NoFileExists(t, filepath.Join(sandboxDir, store.AgentRuntimeDir, ".credentials.json"))
}

func TestCopySeedFiles_CopiesAuthWhenNoAPIKey(t *testing.T) {
	tmpDir := t.TempDir()

	// Create auth file
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{"token":"x"}`), 0600))

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")
	copied, err := CopySeedFiles(agentDef, sandboxDir, false, tmpDir, config.Layout{}) // hasAPIKey=false
	require.NoError(t, err)
	assert.True(t, copied)

	assert.FileExists(t, filepath.Join(sandboxDir, store.AgentRuntimeDir, ".credentials.json"))
}

func TestCopySeedFiles_HomeDirFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create home-dir seed file
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".claude.json"), []byte(`{"install":"native"}`), 0600))

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")
	_, err := CopySeedFiles(agentDef, sandboxDir, true, tmpDir, config.Layout{})
	require.NoError(t, err)

	// HomeDir=true file should go to home-seed/
	assert.FileExists(t, filepath.Join(sandboxDir, "home-seed", ".claude.json"))
}

func TestCopySeedFiles_SkipsMissingFiles(t *testing.T) {
	tmpDir := t.TempDir()

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")
	copied, err := CopySeedFiles(agentDef, sandboxDir, true, tmpDir, config.Layout{})
	require.NoError(t, err)
	assert.False(t, copied)
}

func TestCopySeedFiles_KeychainFallback(t *testing.T) {
	tmpDir := t.TempDir()

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")

	// Override KeychainReader to return credentials
	origReader := KeychainReader
	KeychainReader = func(service string) ([]byte, error) {
		if service == "Claude Code-credentials" {
			return []byte(`{"token":"from-keychain"}`), nil
		}
		return nil, fmt.Errorf("not found")
	}
	defer func() { KeychainReader = origReader }()

	copied, err := CopySeedFiles(agentDef, sandboxDir, false, tmpDir, config.Layout{}) // hasAPIKey=false
	require.NoError(t, err)
	assert.True(t, copied)

	// Credentials from keychain should be written to agent-runtime
	data, err := os.ReadFile(filepath.Join(sandboxDir, store.AgentRuntimeDir, ".credentials.json")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, `{"token":"from-keychain"}`, string(data))
}

func TestCopySeedFiles_KeychainSkippedWhenFileExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the credentials file on disk
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{"token":"from-file"}`), 0600))

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")

	// Override KeychainReader — should NOT be called since file exists
	origReader := KeychainReader
	keychainCalled := false
	KeychainReader = func(_ string) ([]byte, error) {
		keychainCalled = true
		return []byte(`{"token":"from-keychain"}`), nil
	}
	defer func() { KeychainReader = origReader }()

	copied, err := CopySeedFiles(agentDef, sandboxDir, false, tmpDir, config.Layout{})
	require.NoError(t, err)
	assert.True(t, copied)
	assert.False(t, keychainCalled, "KeychainReader should not be called when file exists")

	// Should have the file contents, not keychain
	data, err := os.ReadFile(filepath.Join(sandboxDir, store.AgentRuntimeDir, ".credentials.json")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, `{"token":"from-file"}`, string(data))
}

// EnsureContainerSettings tests

func TestEnsureContainerSettings_SetsSkipPermissions(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))

	agentDef := agent.GetAgent("claude")
	require.NoError(t, EnsureContainerSettings(agentDef, sandboxDir, ""))

	settings, err := fileutil.ReadJSONMap(filepath.Join(sandboxDir, store.AgentRuntimeDir, "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, true, settings["skipDangerousModePermissionPrompt"])
}

func TestEnsureContainerSettings_NoopForTestAgent(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))

	agentDef := agent.GetAgent("test")
	require.NoError(t, EnsureContainerSettings(agentDef, sandboxDir, ""))

	// No settings file should be created for test agent
	assert.NoFileExists(t, filepath.Join(sandboxDir, store.AgentRuntimeDir, "settings.json"))
}

func TestEnsureContainerSettings_PreservesExisting(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))

	// Pre-populate settings
	settingsPath := filepath.Join(sandboxDir, store.AgentRuntimeDir, "settings.json")
	require.NoError(t, fileutil.WriteJSONMap(settingsPath, map[string]any{"customKey": "customValue"}))

	agentDef := agent.GetAgent("claude")
	require.NoError(t, EnsureContainerSettings(agentDef, sandboxDir, ""))

	settings, err := fileutil.ReadJSONMap(settingsPath)
	require.NoError(t, err)
	assert.Equal(t, "customValue", settings["customKey"])
	assert.Equal(t, true, settings["skipDangerousModePermissionPrompt"])
}

func TestEnsureContainerSettings_GeminiDisablesFolderTrust(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))

	agentDef := agent.GetAgent("gemini")
	require.NoError(t, EnsureContainerSettings(agentDef, sandboxDir, ""))

	settings, err := fileutil.ReadJSONMap(filepath.Join(sandboxDir, store.AgentRuntimeDir, "settings.json"))
	require.NoError(t, err)

	security, ok := settings["security"].(map[string]any)
	require.True(t, ok)
	folderTrust, ok := security["folderTrust"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, folderTrust["enabled"])
}

func TestEnsureContainerSettings_GeminiPreservesAuthSettings(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))

	// Pre-populate settings with auth config (as would come from seed file)
	settingsPath := filepath.Join(sandboxDir, store.AgentRuntimeDir, "settings.json")
	require.NoError(t, fileutil.WriteJSONMap(settingsPath, map[string]any{
		"security": map[string]any{
			"auth": map[string]any{"selectedType": "oauth-personal"},
		},
	}))

	agentDef := agent.GetAgent("gemini")
	require.NoError(t, EnsureContainerSettings(agentDef, sandboxDir, ""))

	settings, err := fileutil.ReadJSONMap(settingsPath)
	require.NoError(t, err)

	security := settings["security"].(map[string]any)
	// folderTrust should be disabled
	folderTrust := security["folderTrust"].(map[string]any)
	assert.Equal(t, false, folderTrust["enabled"])
	// auth should be preserved
	auth := security["auth"].(map[string]any)
	assert.Equal(t, "oauth-personal", auth["selectedType"])
}

// ensureHomeSeedConfig tests

func TestEnsureHomeSeedConfig_SetsInstallMethod(t *testing.T) {
	sandboxDir := t.TempDir()
	homeSeedDir := filepath.Join(sandboxDir, "home-seed")
	require.NoError(t, os.MkdirAll(homeSeedDir, 0750))

	// Create the .claude.json that would have been seeded
	require.NoError(t, fileutil.WriteJSONMap(filepath.Join(homeSeedDir, ".claude.json"), map[string]any{
		"installMethod": "native",
		"otherKey":      "preserved",
	}))

	agentDef := agent.GetAgent("claude")
	require.NoError(t, ensureHomeSeedConfig(agentDef, sandboxDir, "npm-global"))

	cfg, err := fileutil.ReadJSONMap(filepath.Join(homeSeedDir, ".claude.json"))
	require.NoError(t, err)
	assert.Equal(t, "npm-global", cfg["installMethod"])
	assert.Equal(t, "preserved", cfg["otherKey"])
}

func TestEnsureHomeSeedConfig_NativeMethodForTart(t *testing.T) {
	sandboxDir := t.TempDir()
	homeSeedDir := filepath.Join(sandboxDir, "home-seed")
	require.NoError(t, os.MkdirAll(homeSeedDir, 0750))

	require.NoError(t, fileutil.WriteJSONMap(filepath.Join(homeSeedDir, ".claude.json"), map[string]any{
		"installMethod": "native",
		"otherKey":      "preserved",
	}))

	agentDef := agent.GetAgent("claude")
	require.NoError(t, ensureHomeSeedConfig(agentDef, sandboxDir, "native"))

	cfg, err := fileutil.ReadJSONMap(filepath.Join(homeSeedDir, ".claude.json"))
	require.NoError(t, err)
	assert.Equal(t, "native", cfg["installMethod"])
	assert.Equal(t, "preserved", cfg["otherKey"])
}

func TestEnsureHomeSeedConfig_NoopForTestAgent(t *testing.T) {
	sandboxDir := t.TempDir()
	agentDef := agent.GetAgent("test")

	// Should not error even with no home-seed dir
	require.NoError(t, ensureHomeSeedConfig(agentDef, sandboxDir, "npm-global"))
}

// HasAnyAuthHint tests

func TestHasAnyAuthHint_NoHintVars(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	assert.False(t, HasAnyAuthHint(agentDef, nil, config.Layout{}))
}

func TestHasAnyAuthHint_HostEnvSet(t *testing.T) {
	agentDef := agent.GetAgent("aider")
	hostEnv := config.Layout{}.WithEnv(map[string]string{"OLLAMA_API_BASE": "http://localhost:11434"})
	assert.True(t, HasAnyAuthHint(agentDef, nil, hostEnv))
}

func TestHasAnyAuthHint_ConfigEnvSet(t *testing.T) {
	agentDef := agent.GetAgent("aider")
	configEnv := map[string]string{
		"OLLAMA_API_BASE": "http://localhost:11434",
	}
	assert.True(t, HasAnyAuthHint(agentDef, configEnv, config.Layout{}))
}

func TestHasAnyAuthHint_NeitherSet(t *testing.T) {
	agentDef := agent.GetAgent("aider")
	assert.False(t, HasAnyAuthHint(agentDef, nil, config.Layout{}))
}
