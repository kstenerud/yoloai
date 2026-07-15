// ABOUTME: Sandbox env bootstrap: auth-presence detection (key/file/keychain),
// ABOUTME: secrets dir creation, seed-file copying, and container settings
// ABOUTME: patches (skip-permissions, folder-trust, stale install-method).

package envsetup

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
	"github.com/kstenerud/yoloai/store"
)

func agentSpec(agentDef *agent.Definition) EnvSpec {
	sfs := make([]SeedFile, len(agentDef.SeedFiles))
	for i, sf := range agentDef.SeedFiles {
		sfs[i] = SeedFile{
			HostPath:        sf.HostPath,
			TargetPath:      sf.TargetPath,
			Content:         sf.Content,
			AuthOnly:        sf.AuthOnly,
			HomeDir:         sf.HomeDir,
			KeychainService: sf.KeychainService,
			OwnerAPIKeys:    sf.OwnerAPIKeys,
			Executable:      sf.Executable,
		}
	}
	var patches []SettingsPatch
	if !agentDef.SeedsAllAgents && agentDef.StateDir != "" && agentDef.ApplySettings != nil {
		patches = []SettingsPatch{{
			RelDir:  store.AgentRuntimeDir,
			DirPerm: store.Perms().Dir,
			Apply:   agentDef.ApplySettings,
		}}
	}
	return EnvSpec{
		APIKeyEnvVars:          agentDef.APIKeyEnvVars,
		AuthHintEnvVars:        agentDef.AuthHintEnvVars,
		SeedFiles:              sfs,
		StateRelPath:           agentDef.StateRelPath(),
		HasStateDir:            agentDef.StateDir != "",
		AgentFilesExclude:      agentDef.AgentFilesExclude,
		SettingsPatches:        patches,
		ShortLivedOAuthWarning: agentDef.ShortLivedOAuthWarning,
	}
}

// ResolveAuthPresence is the single source of truth for the auth-presence
// policy shared by the create-time gate, `run`'s headless decision, and
// `system check`. These lock the OR truth-table so those callers can't diverge.
func TestResolveAuthPresence(t *testing.T) {
	origReader := KeychainReader
	KeychainReader = func(string) ([]byte, error) { return nil, fmt.Errorf("no keychain") }
	defer func() { KeychainReader = origReader }()

	claude := agentSpec(agent.GetAgent("claude"))

	t.Run("api key env var", func(t *testing.T) {
		layout := config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test"})
		got := ResolveAuthPresence(claude, nil, layout)
		assert.Equal(t, AuthPresence{APIKey: true}, got)
		assert.True(t, got.OK())
	})

	t.Run("auth file on disk", func(t *testing.T) {
		home := t.TempDir()
		cred := filepath.Join(home, ".claude", ".credentials.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(cred), 0o750))
		require.NoError(t, os.WriteFile(cred, []byte(`{}`), 0o600))
		got := ResolveAuthPresence(claude, nil, config.Layout{HomeDir: home})
		assert.Equal(t, AuthPresence{AuthFile: true}, got)
		assert.True(t, got.OK())
	})

	t.Run("keychain entry", func(t *testing.T) {
		KeychainReader = func(string) ([]byte, error) { return []byte(`{}`), nil }
		defer func() {
			KeychainReader = func(string) ([]byte, error) { return nil, fmt.Errorf("no keychain") }
		}()
		got := ResolveAuthPresence(claude, nil, config.Layout{HomeDir: t.TempDir()})
		assert.True(t, got.AuthFile)
		assert.True(t, got.OK())
	})

	t.Run("auth hint via configEnv", func(t *testing.T) {
		aider := agentSpec(agent.GetAgent("aider"))
		got := ResolveAuthPresence(aider, map[string]string{"OLLAMA_API_BASE": "http://host:11434"}, config.Layout{HomeDir: t.TempDir()})
		assert.True(t, got.AuthHint)
		assert.False(t, got.APIKey)
		assert.True(t, got.OK())
	})

	t.Run("no auth at all", func(t *testing.T) {
		got := ResolveAuthPresence(claude, nil, config.Layout{HomeDir: t.TempDir()})
		assert.Equal(t, AuthPresence{}, got)
		assert.False(t, got.OK())
	})
}

// HasAnyAPIKey tests

func TestHasAnyAPIKey_Set(t *testing.T) {
	spec := EnvSpec{APIKeyEnvVars: agent.GetAgent("claude").APIKeyEnvVars}
	hostEnv := config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test-123"})

	assert.True(t, HasAnyAPIKey(spec, hostEnv))
}

func TestHasAnyAPIKey_Unset(t *testing.T) {
	spec := EnvSpec{APIKeyEnvVars: agent.GetAgent("claude").APIKeyEnvVars}

	assert.False(t, HasAnyAPIKey(spec, config.Layout{}))
}

func TestHasAnyAPIKey_EmptyList(t *testing.T) {
	spec := EnvSpec{}
	assert.True(t, HasAnyAPIKey(spec, config.Layout{})) // no API key required = always true
}

// HasAnyAuthFile tests

func TestHasAnyAuthFile_Exists(t *testing.T) {
	tmpDir := t.TempDir()

	agentDef := agent.GetAgent("claude")

	// Create the credentials file
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{}`), 0600))

	assert.True(t, HasAnyAuthFile(agentSpec(agentDef), tmpDir))
}

func TestHasAnyAuthFile_Missing(t *testing.T) {
	tmpDir := t.TempDir()

	agentDef := agent.GetAgent("claude")
	assert.False(t, HasAnyAuthFile(agentSpec(agentDef), tmpDir))
}

func TestHasAnyAuthFile_NoAuthFiles(t *testing.T) {
	agentDef := agent.GetAgent("test")
	assert.False(t, HasAnyAuthFile(agentSpec(agentDef), "/home/user"))
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

	assert.True(t, HasAnyAuthFile(agentSpec(agentDef), tmpDir))
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

	assert.False(t, HasAnyAuthFile(agentSpec(agentDef), tmpDir))
}

// DescribeSeedAuthFiles tests

func TestDescribeSeedAuthFiles_Claude(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	desc := DescribeSeedAuthFiles(agentSpec(agentDef))
	assert.Contains(t, desc, ".credentials.json")
}

func TestDescribeSeedAuthFiles_NoAuthFiles(t *testing.T) {
	agentDef := agent.GetAgent("test")
	assert.Empty(t, DescribeSeedAuthFiles(agentSpec(agentDef)))
}

// ResolveSecretEnv → StageSecretEnv tests
//
// These cover the credential-staging pair the launch path actually calls
// (launch.go: resolve, let the broker rewrite the map, then stage). They used to
// run through a CreateSecretsDir wrapper that combined the two; it was deleted
// as unused, and combining them is exactly what production must not do — the
// broker step goes in between. So the convenience lives here, in the test, where
// no broker is involved.
//
// Deleting these along with the wrapper would have been a silent loss: it was
// the only test entry point into either half of a security-sensitive path.

// resolveAndStage runs the pair back-to-back, mirroring the launch path minus
// the broker interposition.
func resolveAndStage(t *testing.T, spec EnvSpec, configEnv map[string]string, hostEnv config.Layout, stagingRoot string) (string, error) {
	t.Helper()
	return StageSecretEnv(ResolveSecretEnv(spec, configEnv, hostEnv), hostEnv, stagingRoot)
}

func TestResolveAndStageSecretEnv_WithKey(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	hostEnv := config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test-secret"})

	dir, err := resolveAndStage(t, agentSpec(agentDef), nil, hostEnv, "")
	require.NoError(t, err)
	require.NotEmpty(t, dir)
	defer os.RemoveAll(dir) //nolint:errcheck

	content, err := os.ReadFile(filepath.Join(dir, "ANTHROPIC_API_KEY")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "sk-test-secret", string(content))
}

func TestResolveAndStageSecretEnv_NoKey(t *testing.T) {
	agentDef := agent.GetAgent("claude")

	dir, err := resolveAndStage(t, agentSpec(agentDef), nil, config.Layout{}, "")
	require.NoError(t, err)
	assert.Empty(t, dir)
}

func TestResolveAndStageSecretEnv_NoEnvVars(t *testing.T) {
	agentDef := agent.GetAgent("test")

	dir, err := resolveAndStage(t, agentSpec(agentDef), nil, config.Layout{}, "")
	require.NoError(t, err)
	assert.Empty(t, dir)
}

func TestResolveAndStageSecretEnv_WithEnvVars(t *testing.T) {
	agentDef := agent.GetAgent("test") // no API keys
	envVars := map[string]string{
		"OLLAMA_API_BASE": "http://host.docker.internal:11434",
		"CUSTOM_VAR":      "myvalue",
	}

	dir, err := resolveAndStage(t, agentSpec(agentDef), envVars, config.Layout{}, "")
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

func TestResolveAndStageSecretEnv_APIKeyOverridesEnv(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	envVars := map[string]string{
		"ANTHROPIC_API_KEY": "should-be-overwritten",
	}
	hostEnv := config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-real-key"})

	dir, err := resolveAndStage(t, agentSpec(agentDef), envVars, hostEnv, "")
	require.NoError(t, err)
	require.NotEmpty(t, dir)
	defer os.RemoveAll(dir) //nolint:errcheck

	content, err := os.ReadFile(filepath.Join(dir, "ANTHROPIC_API_KEY")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "sk-real-key", string(content), "API key should override env var")
}

func TestResolveAndStageSecretEnv_EmptyBoth(t *testing.T) {
	agentDef := agent.GetAgent("test")

	dir, err := resolveAndStage(t, agentSpec(agentDef), map[string]string{}, config.Layout{}, "")
	require.NoError(t, err)
	assert.Empty(t, dir)
}

func TestResolveAndStageSecretEnv_HonorsStagingRoot(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	hostEnv := config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test-secret"})
	stagingRoot := t.TempDir()

	dir, err := resolveAndStage(t, agentSpec(agentDef), nil, hostEnv, stagingRoot)
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

	spec := agentSpec(agent.GetAgent("claude"))
	copied, err := CopySeedFiles(spec, sandboxDir, true, tmpDir, config.Layout{})
	require.NoError(t, err)
	assert.False(t, copied) // copied only tracks auth-only files; settings.json is not auth-only

	// settings.json should be in agent-runtime (not auth-only)
	assert.FileExists(t, filepath.Join(sandboxDir, store.AgentRuntimeDir, "settings.json"))
}

func TestCopySeedFiles_ContentFallbackWhenHostAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	// Host file does not exist → the Content fallback is written (aider's case).
	spec := EnvSpec{SeedFiles: []SeedFile{
		{HostPath: filepath.Join(tmpDir, "absent.yml"), TargetPath: ".aider.conf.yml", Content: []byte("{}\n"), HomeDir: true},
	}}
	_, err := CopySeedFiles(spec, sandboxDir, false, tmpDir, config.Layout{})
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(sandboxDir, "home-seed", ".aider.conf.yml")) //nolint:gosec // G304: test-controlled temp path
	require.NoError(t, err)
	assert.Equal(t, "{}\n", string(got))
}

func TestCopySeedFiles_HostFileWinsOverContent(t *testing.T) {
	tmpDir := t.TempDir()
	hostConf := filepath.Join(tmpDir, "host.yml")
	require.NoError(t, os.WriteFile(hostConf, []byte("model: x\n"), 0600))
	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	// A present host file wins over the Content fallback.
	spec := EnvSpec{SeedFiles: []SeedFile{
		{HostPath: hostConf, TargetPath: ".aider.conf.yml", Content: []byte("{}\n"), HomeDir: true},
	}}
	_, err := CopySeedFiles(spec, sandboxDir, false, tmpDir, config.Layout{})
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(sandboxDir, "home-seed", ".aider.conf.yml")) //nolint:gosec // G304: test-controlled temp path
	require.NoError(t, err)
	assert.Equal(t, "model: x\n", string(got))
}

// TestCopySeedFiles_StatusLineScriptIsExecutable verifies the Executable seed
// (Claude Code's statusline.sh) is copied with the owner-exec bit set, since
// Claude Code execs it by path.
func TestCopySeedFiles_StatusLineScriptIsExecutable(t *testing.T) {
	tmpDir := t.TempDir()

	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "statusline.sh"), []byte("#!/bin/sh\necho hi\n"), 0600))

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	_, err := CopySeedFiles(agentSpec(agent.GetAgent("claude")), sandboxDir, true, tmpDir, config.Layout{})
	require.NoError(t, err)

	dst := filepath.Join(sandboxDir, store.AgentRuntimeDir, "statusline.sh")
	info, err := os.Stat(dst)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0100, "owner-exec bit must be set on the seeded statusLine script (got %o)", info.Mode().Perm())
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

	_, err := CopySeedFiles(agentSpec(agent.GetAgent("claude")), sandboxDir, true, tmpDir, config.Layout{}) // hasAPIKey=true
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

	copied, err := CopySeedFiles(agentSpec(agent.GetAgent("claude")), sandboxDir, false, tmpDir, config.Layout{}) // hasAPIKey=false
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

	_, err := CopySeedFiles(agentSpec(agent.GetAgent("claude")), sandboxDir, true, tmpDir, config.Layout{})
	require.NoError(t, err)

	// HomeDir=true file should go to home-seed/
	assert.FileExists(t, filepath.Join(sandboxDir, "home-seed", ".claude.json"))
}

func TestCopySeedFiles_SkipsMissingFiles(t *testing.T) {
	tmpDir := t.TempDir()

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	copied, err := CopySeedFiles(agentSpec(agent.GetAgent("claude")), sandboxDir, true, tmpDir, config.Layout{})
	require.NoError(t, err)
	assert.False(t, copied)
}

func TestCopySeedFiles_KeychainFallback(t *testing.T) {
	tmpDir := t.TempDir()

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	// Override KeychainReader to return credentials
	origReader := KeychainReader
	KeychainReader = func(service string) ([]byte, error) {
		if service == "Claude Code-credentials" {
			return []byte(`{"token":"from-keychain"}`), nil
		}
		return nil, fmt.Errorf("not found")
	}
	defer func() { KeychainReader = origReader }()

	copied, err := CopySeedFiles(agentSpec(agent.GetAgent("claude")), sandboxDir, false, tmpDir, config.Layout{}) // hasAPIKey=false
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

	// Override KeychainReader — should NOT be called since file exists
	origReader := KeychainReader
	keychainCalled := false
	KeychainReader = func(_ string) ([]byte, error) {
		keychainCalled = true
		return []byte(`{"token":"from-keychain"}`), nil
	}
	defer func() { KeychainReader = origReader }()

	copied, err := CopySeedFiles(agentSpec(agent.GetAgent("claude")), sandboxDir, false, tmpDir, config.Layout{})
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
	require.NoError(t, EnsureContainerSettings(sandboxDir, agentSpec(agentDef).SettingsPatches))

	settings, err := fileutil.ReadJSONMap(filepath.Join(sandboxDir, store.AgentRuntimeDir, "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, true, settings["skipDangerousModePermissionPrompt"])
}

func TestEnsureContainerSettings_NoopForTestAgent(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))

	agentDef := agent.GetAgent("test")
	require.NoError(t, EnsureContainerSettings(sandboxDir, agentSpec(agentDef).SettingsPatches))

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
	require.NoError(t, EnsureContainerSettings(sandboxDir, agentSpec(agentDef).SettingsPatches))

	settings, err := fileutil.ReadJSONMap(settingsPath)
	require.NoError(t, err)
	assert.Equal(t, "customValue", settings["customKey"])
	assert.Equal(t, true, settings["skipDangerousModePermissionPrompt"])
}

func TestEnsureContainerSettings_GeminiDisablesFolderTrust(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))

	agentDef := agent.GetAgent("gemini")
	require.NoError(t, EnsureContainerSettings(sandboxDir, agentSpec(agentDef).SettingsPatches))

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
	require.NoError(t, EnsureContainerSettings(sandboxDir, agentSpec(agentDef).SettingsPatches))

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

// TestEnsureHomeSeedConfig_StripsStaleInstallMethod verifies that a host-seeded
// .claude.json carrying its own installMethod (e.g. "native") has that key removed
// rather than overwritten, so no stale value propagates into the sandbox.
// Other keys must be left intact (the rest of the host config is still useful).
func TestEnsureHomeSeedConfig_StripsStaleInstallMethod(t *testing.T) {
	sandboxDir := t.TempDir()
	homeSeedDir := filepath.Join(sandboxDir, "home-seed")
	require.NoError(t, os.MkdirAll(homeSeedDir, 0750))

	// Seed a .claude.json as the host would produce it
	require.NoError(t, fileutil.WriteJSONMap(filepath.Join(homeSeedDir, ".claude.json"), map[string]any{
		"installMethod": "native",
		"otherKey":      "preserved",
	}))

	agentDef := agent.GetAgent("claude")
	require.NoError(t, ensureHomeSeedConfig(agentSpec(agentDef), sandboxDir, nil))

	cfg, err := fileutil.ReadJSONMap(filepath.Join(homeSeedDir, ".claude.json"))
	require.NoError(t, err)
	_, ok := cfg["installMethod"]
	assert.False(t, ok, "installMethod should be stripped from the seeded config")
	assert.Equal(t, "preserved", cfg["otherKey"])
}

func TestEnsureHomeSeedConfig_NoopForTestAgent(t *testing.T) {
	sandboxDir := t.TempDir()
	agentDef := agent.GetAgent("test")

	// Should not error even with no home-seed dir
	require.NoError(t, ensureHomeSeedConfig(agentSpec(agentDef), sandboxDir, nil))
}

// TestEnsureHomeSeedConfig_PreAcceptsFolderTrust verifies that each mount path in
// trustPaths is written into projects.<path>.hasTrustDialogAccepted, so Claude
// Code's per-directory folder-trust dialog never blocks the sandbox at launch.
func TestEnsureHomeSeedConfig_PreAcceptsFolderTrust(t *testing.T) {
	sandboxDir := t.TempDir()
	homeSeedDir := filepath.Join(sandboxDir, "home-seed")
	require.NoError(t, os.MkdirAll(homeSeedDir, 0750))
	require.NoError(t, fileutil.WriteJSONMap(filepath.Join(homeSeedDir, ".claude.json"), map[string]any{
		"hasCompletedOnboarding": true,
	}))

	agentDef := agent.GetAgent("claude")
	require.NoError(t, ensureHomeSeedConfig(agentSpec(agentDef), sandboxDir, []string{"/work/proj", "/work/aux"}))

	cfg, err := fileutil.ReadJSONMap(filepath.Join(homeSeedDir, ".claude.json"))
	require.NoError(t, err)
	projects, ok := cfg["projects"].(map[string]any)
	require.True(t, ok, "projects map should be present")
	for _, p := range []string{"/work/proj", "/work/aux"} {
		entry, ok := projects[p].(map[string]any)
		require.True(t, ok, "trust entry for %s should be present", p)
		assert.Equal(t, true, entry["hasTrustDialogAccepted"], "%s should be pre-trusted", p)
	}
	// Pre-existing keys must survive.
	assert.Equal(t, true, cfg["hasCompletedOnboarding"])
}

// TestRefreshHomeSeed_TrustSurvivesReseed guards the restart-clobber bug: a bare
// CopySeedFiles rewrites .claude.json to the controlled default (no trust), so the
// canonical RefreshHomeSeed must re-inject folder trust after every re-copy.
func TestRefreshHomeSeed_TrustSurvivesReseed(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, store.AgentRuntimeDir), 0750))

	spec := agentSpec(agent.GetAgent("claude"))
	configPath := filepath.Join(sandboxDir, "home-seed", ".claude.json")

	// First seed (create).
	_, err := RefreshHomeSeed(spec, sandboxDir, true, t.TempDir(), config.Layout{}, []string{"/work/proj"})
	require.NoError(t, err)

	// Second seed (restart) — the bare CopySeedFiles inside would otherwise clobber
	// the trust; RefreshHomeSeed must re-inject it.
	_, err = RefreshHomeSeed(spec, sandboxDir, true, t.TempDir(), config.Layout{}, []string{"/work/proj"})
	require.NoError(t, err)

	cfg, err := fileutil.ReadJSONMap(configPath)
	require.NoError(t, err)
	projects, ok := cfg["projects"].(map[string]any)
	require.True(t, ok, "projects trust must survive the second reseed")
	entry, ok := projects["/work/proj"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, entry["hasTrustDialogAccepted"])
}

// HasAnyAuthHint tests

func TestHasAnyAuthHint_NoHintVars(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	assert.False(t, HasAnyAuthHint(agentSpec(agentDef), nil, config.Layout{}))
}

func TestHasAnyAuthHint_HostEnvSet(t *testing.T) {
	agentDef := agent.GetAgent("aider")
	hostEnv := config.Layout{}.WithEnv(map[string]string{"OLLAMA_API_BASE": "http://localhost:11434"})
	assert.True(t, HasAnyAuthHint(agentSpec(agentDef), nil, hostEnv))
}

func TestHasAnyAuthHint_ConfigEnvSet(t *testing.T) {
	agentDef := agent.GetAgent("aider")
	configEnv := map[string]string{
		"OLLAMA_API_BASE": "http://localhost:11434",
	}
	assert.True(t, HasAnyAuthHint(agentSpec(agentDef), configEnv, config.Layout{}))
}

func TestHasAnyAuthHint_NeitherSet(t *testing.T) {
	agentDef := agent.GetAgent("aider")
	assert.False(t, HasAnyAuthHint(agentSpec(agentDef), nil, config.Layout{}))
}
