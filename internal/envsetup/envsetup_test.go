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

	"github.com/kstenerud/yoloai/feedback"
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
			Dir:     store.AgentRuntimePath,
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
		AgentName:              string(agentDef.Type),
		UserDefined:            agentDef.UserDefined,
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

// DescribeInjectedCredentials tests (D144 line 2: the launch-time disclosure)

func TestDescribeInjectedCredentials_NothingResolvedIsSilent(t *testing.T) {
	spec := agentSpec(agent.GetAgent("claude"))

	got := DescribeInjectedCredentials(spec, config.Layout{})

	assert.Empty(t, got, "no key resolved from the host snapshot must produce no line")
}

func TestDescribeInjectedCredentials_NamesTheShippedAgent(t *testing.T) {
	spec := agentSpec(agent.GetAgent("claude"))
	hostEnv := config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test"})

	got := DescribeInjectedCredentials(spec, hostEnv)

	assert.Equal(t, `credentials injected from the environment: ANTHROPIC_API_KEY (declared by agent "claude")`, got)
}

func TestDescribeInjectedCredentials_NamesTheUserDefinedAgentAndSortsKeys(t *testing.T) {
	spec := EnvSpec{
		AgentName:       "diamond",
		UserDefined:     true,
		APIKeyEnvVars:   []string{"GITHUB_TOKEN", "DIAMOND_KEY"},
		AuthHintEnvVars: nil,
	}
	hostEnv := config.Layout{}.WithEnv(map[string]string{
		"GITHUB_TOKEN": "gh-test",
		"DIAMOND_KEY":  "dk-test",
	})

	got := DescribeInjectedCredentials(spec, hostEnv)

	assert.Equal(t, `credentials injected from the environment: DIAMOND_KEY, GITHUB_TOKEN (declared by user-defined agent "diamond")`, got)
}

func TestDescribeInjectedCredentials_OnlyNamesKeysResolvedFromHost(t *testing.T) {
	// A key present only in configEnv (the user typed it) or merely declared but
	// unset on the host must never appear — DescribeInjectedCredentials takes no
	// configEnv argument at all, precisely so it cannot see that source.
	spec := EnvSpec{
		AgentName:     "claude",
		APIKeyEnvVars: []string{"ANTHROPIC_API_KEY", "UNSET_KEY"},
	}
	hostEnv := config.Layout{}.WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk-test"})

	got := DescribeInjectedCredentials(spec, hostEnv)

	assert.Equal(t, `credentials injected from the environment: ANTHROPIC_API_KEY (declared by agent "claude")`, got)
}

// TestArch_LibraryNeverReadsAmbientEnv pins D144 line 1 as a behavioural claim,
// not just an enforced call. `forbidigo` bans os.Getenv/os.Environ/os.LookupEnv/
// os.ExpandEnv/syscall.Getenv/syscall.Environ repo-wide (one path exemption:
// cliutil/layout.go's licensed os.Environ() read) — that proves the call is
// absent, not that a variable which is live in the process but missing from the
// threaded snapshot stays out of the sandbox. This test proves the behaviour
// directly: it sets a real process-env var a shipped agent declares, builds a
// config.Layout whose snapshot omits it, and asserts ResolveSecretEnv — the
// resolver that delivers credentials into the sandbox — never sees it.
//
// Verified red on revert: temporarily rewriting ResolveSecretEnv's host-lookup
// to fall back to os.Getenv when the snapshot misses a declared key made this
// test fail (the ambient value leaked through), confirming it is wired to the
// behaviour and not just to the call site.
func TestArch_LibraryNeverReadsAmbientEnv(t *testing.T) {
	claudeDef := agent.GetAgent("claude")
	require.NotNil(t, claudeDef)
	require.Contains(t, claudeDef.APIKeyEnvVars, "ANTHROPIC_API_KEY")

	t.Setenv("ANTHROPIC_API_KEY", "sk-ambient-leak")
	_, present := os.LookupEnv("ANTHROPIC_API_KEY")
	require.True(t, present, "sanity: the var must actually be live in the process env")

	spec := agentSpec(claudeDef)
	hostEnv := config.Layout{} // zero-value snapshot: does not carry ANTHROPIC_API_KEY

	got := ResolveSecretEnv(spec, nil, hostEnv)

	assert.NotContains(t, got, "ANTHROPIC_API_KEY")
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
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))

	spec := agentSpec(agent.GetAgent("claude"))
	copied, err := CopySeedFiles(spec, sandboxDir, true, tmpDir, config.Layout{})
	require.NoError(t, err)
	assert.False(t, copied) // copied only tracks auth-only files; settings.json is not auth-only

	// settings.json should be in agent-runtime (not auth-only)
	assert.FileExists(t, filepath.Join(store.AgentRuntimePath(sandboxDir), "settings.json"))
}

func TestCopySeedFiles_ContentFallbackWhenHostAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))

	// Host file does not exist → the Content fallback is written (aider's case).
	spec := EnvSpec{SeedFiles: []SeedFile{
		{HostPath: filepath.Join(tmpDir, "absent.yml"), TargetPath: ".aider.conf.yml", Content: []byte("{}\n"), HomeDir: true},
	}}
	_, err := CopySeedFiles(spec, sandboxDir, false, tmpDir, config.Layout{})
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(store.HomeSeedPath(sandboxDir), ".aider.conf.yml"))
	require.NoError(t, err)
	assert.Equal(t, "{}\n", string(got))
}

func TestCopySeedFiles_HostFileWinsOverContent(t *testing.T) {
	tmpDir := t.TempDir()
	hostConf := filepath.Join(tmpDir, "host.yml")
	require.NoError(t, os.WriteFile(hostConf, []byte("model: x\n"), 0600))
	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))

	// A present host file wins over the Content fallback.
	spec := EnvSpec{SeedFiles: []SeedFile{
		{HostPath: hostConf, TargetPath: ".aider.conf.yml", Content: []byte("{}\n"), HomeDir: true},
	}}
	_, err := CopySeedFiles(spec, sandboxDir, false, tmpDir, config.Layout{})
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(store.HomeSeedPath(sandboxDir), ".aider.conf.yml"))
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
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))

	_, err := CopySeedFiles(agentSpec(agent.GetAgent("claude")), sandboxDir, true, tmpDir, config.Layout{})
	require.NoError(t, err)

	dst := filepath.Join(store.AgentRuntimePath(sandboxDir), "statusline.sh")
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
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))

	_, err := CopySeedFiles(agentSpec(agent.GetAgent("claude")), sandboxDir, true, tmpDir, config.Layout{}) // hasAPIKey=true
	require.NoError(t, err)

	// Auth-only file should NOT be copied when API key is set
	assert.NoFileExists(t, filepath.Join(store.AgentRuntimePath(sandboxDir), ".credentials.json"))
}

func TestCopySeedFiles_CopiesAuthWhenNoAPIKey(t *testing.T) {
	tmpDir := t.TempDir()

	// Create auth file
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{"token":"x"}`), 0600))

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))

	copied, err := CopySeedFiles(agentSpec(agent.GetAgent("claude")), sandboxDir, false, tmpDir, config.Layout{}) // hasAPIKey=false
	require.NoError(t, err)
	assert.True(t, copied)

	assert.FileExists(t, filepath.Join(store.AgentRuntimePath(sandboxDir), ".credentials.json"))
}

func TestCopySeedFiles_HomeDirFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create home-dir seed file
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".claude.json"), []byte(`{"install":"native"}`), 0600))

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))

	_, err := CopySeedFiles(agentSpec(agent.GetAgent("claude")), sandboxDir, true, tmpDir, config.Layout{})
	require.NoError(t, err)

	// HomeDir=true file should go to home-seed/
	assert.FileExists(t, filepath.Join(store.HomeSeedPath(sandboxDir), ".claude.json"))
}

func TestCopySeedFiles_SkipsMissingFiles(t *testing.T) {
	tmpDir := t.TempDir()

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))

	copied, err := CopySeedFiles(agentSpec(agent.GetAgent("claude")), sandboxDir, true, tmpDir, config.Layout{})
	require.NoError(t, err)
	assert.False(t, copied)
}

func TestCopySeedFiles_KeychainFallback(t *testing.T) {
	tmpDir := t.TempDir()

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))

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
	data, err := os.ReadFile(filepath.Join(store.AgentRuntimePath(sandboxDir), ".credentials.json"))
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
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))

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
	data, err := os.ReadFile(filepath.Join(store.AgentRuntimePath(sandboxDir), ".credentials.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"token":"from-file"}`, string(data))
}

// EnsureContainerSettings tests

func TestEnsureContainerSettings_SetsSkipPermissions(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))

	agentDef := agent.GetAgent("claude")
	require.NoError(t, EnsureContainerSettings(sandboxDir, agentSpec(agentDef).SettingsPatches))

	settings, err := fileutil.ReadJSONMap(filepath.Join(store.AgentRuntimePath(sandboxDir), "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, true, settings["skipDangerousModePermissionPrompt"])
}

func TestEnsureContainerSettings_NoopForTestAgent(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))

	agentDef := agent.GetAgent("test")
	require.NoError(t, EnsureContainerSettings(sandboxDir, agentSpec(agentDef).SettingsPatches))

	// No settings file should be created for test agent
	assert.NoFileExists(t, filepath.Join(store.AgentRuntimePath(sandboxDir), "settings.json"))
}

func TestEnsureContainerSettings_PreservesExisting(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))

	// Pre-populate settings
	settingsPath := filepath.Join(store.AgentRuntimePath(sandboxDir), "settings.json")
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
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))

	agentDef := agent.GetAgent("gemini")
	require.NoError(t, EnsureContainerSettings(sandboxDir, agentSpec(agentDef).SettingsPatches))

	settings, err := fileutil.ReadJSONMap(filepath.Join(store.AgentRuntimePath(sandboxDir), "settings.json"))
	require.NoError(t, err)

	security, ok := settings["security"].(map[string]any)
	require.True(t, ok)
	folderTrust, ok := security["folderTrust"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, folderTrust["enabled"])
}

func TestEnsureContainerSettings_GeminiPreservesAuthSettings(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))

	// Pre-populate settings with auth config (as would come from seed file)
	settingsPath := filepath.Join(store.AgentRuntimePath(sandboxDir), "settings.json")
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
	homeSeedDir := store.HomeSeedPath(sandboxDir)
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
	homeSeedDir := store.HomeSeedPath(sandboxDir)
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
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))

	spec := agentSpec(agent.GetAgent("claude"))
	configPath := filepath.Join(store.HomeSeedPath(sandboxDir), ".claude.json")

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

// TestSeedSandbox_ShortLivedOAuthIsAWarningRecord covers the one advisory
// SeedSandbox emits.
//
// It matters because of what it says: the credentials the sandbox was seeded
// with expire in about thirty minutes, so a long agent run will fail partway
// through with an auth error that looks like anything but a token expiry. As
// three Fprintlns it could only ever be read; as a record a caller can route
// it, or act on it before starting a long session.
func TestSeedSandbox_ShortLivedOAuthIsAWarningRecord(t *testing.T) {
	tmpDir := t.TempDir()

	// A host OAuth credential and no API key is what makes RefreshHomeSeed copy
	// it — the condition the warning is about.
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{"token":"x"}`), 0600))

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))

	spec := agentSpec(agent.GetAgent("claude"))
	require.True(t, spec.ShortLivedOAuthWarning, "the claude agent is the one that declares this")

	var got feedback.Collector
	_, err := SeedSandbox(spec, sandboxDir, nil, tmpDir, config.Layout{}, nil, &got)
	require.NoError(t, err)

	notices := got.Notices()
	require.Len(t, notices, 1, "seeding a short-lived OAuth credential must say so")
	assert.Equal(t, "credentials.short_lived_oauth", notices[0].Event)
	assert.Equal(t, feedback.LevelWarn, notices[0].Level,
		"at info level this is suppressed under --json, which is where a long unattended run is started")
	assert.Contains(t, notices[0].Message, "30 minutes")
}

// TestSeedSandbox_NoOAuthWarningWithoutTheCredential pins the silence. A notice
// emitted unconditionally would train the user to ignore it, which costs more
// than not having it.
func TestSeedSandbox_NoOAuthWarningWithoutTheCredential(t *testing.T) {
	tmpDir := t.TempDir()
	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(store.AgentRuntimePath(sandboxDir), 0750))
	require.NoError(t, os.MkdirAll(store.HomeSeedPath(sandboxDir), 0750))

	var got feedback.Collector
	_, err := SeedSandbox(agentSpec(agent.GetAgent("claude")), sandboxDir, nil, tmpDir, config.Layout{}, nil, &got)
	require.NoError(t, err)

	assert.Empty(t, got.Notices(), "nothing was seeded, so there is nothing to warn about")
}
