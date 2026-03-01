package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/runtime"
)

// hasAnyAPIKey tests

func TestHasAnyAPIKey_Set(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-123")

	assert.True(t, hasAnyAPIKey(agentDef))
}

func TestHasAnyAPIKey_Unset(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	t.Setenv("ANTHROPIC_API_KEY", "")

	assert.False(t, hasAnyAPIKey(agentDef))
}

func TestHasAnyAPIKey_EmptyList(t *testing.T) {
	agentDef := agent.GetAgent("test")
	assert.False(t, hasAnyAPIKey(agentDef))
}

// hasAnyAuthFile tests

func TestHasAnyAuthFile_Exists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	agentDef := agent.GetAgent("claude")

	// Create the credentials file
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{}`), 0600))

	assert.True(t, hasAnyAuthFile(agentDef))
}

func TestHasAnyAuthFile_Missing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	agentDef := agent.GetAgent("claude")
	assert.False(t, hasAnyAuthFile(agentDef))
}

func TestHasAnyAuthFile_NoAuthFiles(t *testing.T) {
	agentDef := agent.GetAgent("test")
	assert.False(t, hasAnyAuthFile(agentDef))
}

// describeSeedAuthFiles tests

func TestDescribeSeedAuthFiles_Claude(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	desc := describeSeedAuthFiles(agentDef)
	assert.Contains(t, desc, ".credentials.json")
}

func TestDescribeSeedAuthFiles_NoAuthFiles(t *testing.T) {
	agentDef := agent.GetAgent("test")
	assert.Empty(t, describeSeedAuthFiles(agentDef))
}

// createSecretsDir tests

func TestCreateSecretsDir_WithKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-secret")
	agentDef := agent.GetAgent("claude")

	dir, err := createSecretsDir(agentDef, nil)
	require.NoError(t, err)
	require.NotEmpty(t, dir)
	defer os.RemoveAll(dir) //nolint:errcheck

	content, err := os.ReadFile(filepath.Join(dir, "ANTHROPIC_API_KEY")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "sk-test-secret", string(content))
}

func TestCreateSecretsDir_NoKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	agentDef := agent.GetAgent("claude")

	dir, err := createSecretsDir(agentDef, nil)
	require.NoError(t, err)
	assert.Empty(t, dir)
}

func TestCreateSecretsDir_NoEnvVars(t *testing.T) {
	agentDef := agent.GetAgent("test")

	dir, err := createSecretsDir(agentDef, nil)
	require.NoError(t, err)
	assert.Empty(t, dir)
}

func TestCreateSecretsDir_WithEnvVars(t *testing.T) {
	agentDef := agent.GetAgent("test") // no API keys
	envVars := map[string]string{
		"OLLAMA_API_BASE": "http://host.docker.internal:11434",
		"CUSTOM_VAR":      "myvalue",
	}

	dir, err := createSecretsDir(agentDef, envVars)
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
	t.Setenv("ANTHROPIC_API_KEY", "sk-real-key")
	agentDef := agent.GetAgent("claude")
	envVars := map[string]string{
		"ANTHROPIC_API_KEY": "should-be-overwritten",
	}

	dir, err := createSecretsDir(agentDef, envVars)
	require.NoError(t, err)
	require.NotEmpty(t, dir)
	defer os.RemoveAll(dir) //nolint:errcheck

	content, err := os.ReadFile(filepath.Join(dir, "ANTHROPIC_API_KEY")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "sk-real-key", string(content), "API key should override env var")
}

func TestCreateSecretsDir_EmptyBoth(t *testing.T) {
	agentDef := agent.GetAgent("test")

	dir, err := createSecretsDir(agentDef, map[string]string{})
	require.NoError(t, err)
	assert.Empty(t, dir)
}

// copySeedFiles tests

func TestCopySeedFiles_CopiesExistingFiles(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create seed files on host
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(`{"s":1}`), 0600))

	// Create sandbox dir structure
	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")
	copied, err := copySeedFiles(agentDef, sandboxDir, true)
	require.NoError(t, err)
	assert.True(t, copied)

	// settings.json should be in agent-state (not auth-only)
	assert.FileExists(t, filepath.Join(sandboxDir, "agent-state", "settings.json"))
}

func TestCopySeedFiles_SkipsAuthWhenAPIKeySet(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create auth file
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{}`), 0600))

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")
	_, err := copySeedFiles(agentDef, sandboxDir, true) // hasAPIKey=true
	require.NoError(t, err)

	// Auth-only file should NOT be copied when API key is set
	assert.NoFileExists(t, filepath.Join(sandboxDir, "agent-state", ".credentials.json"))
}

func TestCopySeedFiles_CopiesAuthWhenNoAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create auth file
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{"token":"x"}`), 0600))

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")
	copied, err := copySeedFiles(agentDef, sandboxDir, false) // hasAPIKey=false
	require.NoError(t, err)
	assert.True(t, copied)

	assert.FileExists(t, filepath.Join(sandboxDir, "agent-state", ".credentials.json"))
}

func TestCopySeedFiles_HomeDirFiles(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create home-dir seed file
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".claude.json"), []byte(`{"install":"native"}`), 0600))

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")
	_, err := copySeedFiles(agentDef, sandboxDir, true)
	require.NoError(t, err)

	// HomeDir=true file should go to home-seed/
	assert.FileExists(t, filepath.Join(sandboxDir, "home-seed", ".claude.json"))
}

func TestCopySeedFiles_SkipsMissingFiles(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")
	copied, err := copySeedFiles(agentDef, sandboxDir, true)
	require.NoError(t, err)
	assert.False(t, copied)
}

// ensureContainerSettings tests

func TestEnsureContainerSettings_SetsSkipPermissions(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750))

	agentDef := agent.GetAgent("claude")
	require.NoError(t, ensureContainerSettings(agentDef, sandboxDir))

	settings, err := readJSONMap(filepath.Join(sandboxDir, "agent-state", "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, true, settings["skipDangerousModePermissionPrompt"])
}

func TestEnsureContainerSettings_NoopForTestAgent(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750))

	agentDef := agent.GetAgent("test")
	require.NoError(t, ensureContainerSettings(agentDef, sandboxDir))

	// No settings file should be created for test agent
	assert.NoFileExists(t, filepath.Join(sandboxDir, "agent-state", "settings.json"))
}

func TestEnsureContainerSettings_PreservesExisting(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750))

	// Pre-populate settings
	settingsPath := filepath.Join(sandboxDir, "agent-state", "settings.json")
	require.NoError(t, writeJSONMap(settingsPath, map[string]any{"customKey": "customValue"}))

	agentDef := agent.GetAgent("claude")
	require.NoError(t, ensureContainerSettings(agentDef, sandboxDir))

	settings, err := readJSONMap(settingsPath)
	require.NoError(t, err)
	assert.Equal(t, "customValue", settings["customKey"])
	assert.Equal(t, true, settings["skipDangerousModePermissionPrompt"])
}

func TestEnsureContainerSettings_GeminiDisablesFolderTrust(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750))

	agentDef := agent.GetAgent("gemini")
	require.NoError(t, ensureContainerSettings(agentDef, sandboxDir))

	settings, err := readJSONMap(filepath.Join(sandboxDir, "agent-state", "settings.json"))
	require.NoError(t, err)

	security, ok := settings["security"].(map[string]interface{})
	require.True(t, ok)
	folderTrust, ok := security["folderTrust"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, false, folderTrust["enabled"])
}

func TestEnsureContainerSettings_GeminiPreservesAuthSettings(t *testing.T) {
	sandboxDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750))

	// Pre-populate settings with auth config (as would come from seed file)
	settingsPath := filepath.Join(sandboxDir, "agent-state", "settings.json")
	require.NoError(t, writeJSONMap(settingsPath, map[string]any{
		"security": map[string]any{
			"auth": map[string]any{"selectedType": "oauth-personal"},
		},
	}))

	agentDef := agent.GetAgent("gemini")
	require.NoError(t, ensureContainerSettings(agentDef, sandboxDir))

	settings, err := readJSONMap(settingsPath)
	require.NoError(t, err)

	security := settings["security"].(map[string]interface{})
	// folderTrust should be disabled
	folderTrust := security["folderTrust"].(map[string]interface{})
	assert.Equal(t, false, folderTrust["enabled"])
	// auth should be preserved
	auth := security["auth"].(map[string]interface{})
	assert.Equal(t, "oauth-personal", auth["selectedType"])
}

// ensureHomeSeedConfig tests

func TestEnsureHomeSeedConfig_SetsInstallMethod(t *testing.T) {
	sandboxDir := t.TempDir()
	homeSeedDir := filepath.Join(sandboxDir, "home-seed")
	require.NoError(t, os.MkdirAll(homeSeedDir, 0750))

	// Create the .claude.json that would have been seeded
	require.NoError(t, writeJSONMap(filepath.Join(homeSeedDir, ".claude.json"), map[string]any{
		"installMethod": "native",
		"otherKey":      "preserved",
	}))

	agentDef := agent.GetAgent("claude")
	require.NoError(t, ensureHomeSeedConfig(agentDef, sandboxDir))

	config, err := readJSONMap(filepath.Join(homeSeedDir, ".claude.json"))
	require.NoError(t, err)
	assert.Equal(t, "npm-global", config["installMethod"])
	assert.Equal(t, "preserved", config["otherKey"])
}

func TestEnsureHomeSeedConfig_NoopForTestAgent(t *testing.T) {
	sandboxDir := t.TempDir()
	agentDef := agent.GetAgent("test")

	// Should not error even with no home-seed dir
	require.NoError(t, ensureHomeSeedConfig(agentDef, sandboxDir))
}

// shellEscapeForDoubleQuotes tests

func TestShellEscapeForDoubleQuotes(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`hello`, `hello`},
		{`say "hello"`, `say \"hello\"`},
		{"use `backticks`", "use \\`backticks\\`"},
		{`$HOME`, `\$HOME`},
		{`back\slash`, `back\\slash`},
		{`all "special" $chars` + " `here`", `all \"special\" \$chars` + " \\`here\\`"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, shellEscapeForDoubleQuotes(tt.input))
	}
}

// buildMounts tests

func TestBuildMounts_CopyMode(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	state := &sandboxState{
		sandboxDir:  "/home/user/.yoloai/sandboxes/test",
		workdir:     &DirArg{Path: "/home/user/project", Mode: "copy"},
		workCopyDir: "/home/user/.yoloai/sandboxes/test/work/project",
		agent:       agentDef,
		hasPrompt:   true,
	}

	mounts := buildMounts(state, "")

	// Find workdir mount
	var workMount *runtime.MountSpec
	for i := range mounts {
		if mounts[i].Target == "/home/user/project" {
			workMount = &mounts[i]
			break
		}
	}
	require.NotNil(t, workMount)
	assert.Equal(t, state.workCopyDir, workMount.Source)
}

func TestBuildMounts_RWMode(t *testing.T) {
	agentDef := agent.GetAgent("test")
	state := &sandboxState{
		sandboxDir: "/home/user/.yoloai/sandboxes/test",
		workdir:    &DirArg{Path: "/home/user/project", Mode: "rw"},
		agent:      agentDef,
	}

	mounts := buildMounts(state, "")

	// In rw mode, source should be the host path itself
	var workMount *runtime.MountSpec
	for i := range mounts {
		if mounts[i].Target == "/home/user/project" {
			workMount = &mounts[i]
			break
		}
	}
	require.NotNil(t, workMount)
	assert.Equal(t, "/home/user/project", workMount.Source)
}

func TestBuildMounts_IncludesAgentState(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	state := &sandboxState{
		sandboxDir: "/sandbox",
		workdir:    &DirArg{Path: "/project", Mode: "copy"},
		agent:      agentDef,
	}

	mounts := buildMounts(state, "")

	var found bool
	for _, m := range mounts {
		if m.Target == agentDef.StateDir {
			found = true
			assert.Equal(t, "/sandbox/agent-state", m.Source)
		}
	}
	assert.True(t, found, "should include agent state mount")
}

func TestBuildMounts_IncludesPrompt(t *testing.T) {
	agentDef := agent.GetAgent("test")
	state := &sandboxState{
		sandboxDir: "/sandbox",
		workdir:    &DirArg{Path: "/project", Mode: "copy"},
		agent:      agentDef,
		hasPrompt:  true,
	}

	mounts := buildMounts(state, "")

	var found bool
	for _, m := range mounts {
		if m.Target == "/yoloai/prompt.txt" {
			found = true
			assert.True(t, m.ReadOnly)
		}
	}
	assert.True(t, found, "should include prompt mount when hasPrompt")
}

func TestBuildMounts_ExcludesPromptWhenNone(t *testing.T) {
	agentDef := agent.GetAgent("test")
	state := &sandboxState{
		sandboxDir: "/sandbox",
		workdir:    &DirArg{Path: "/project", Mode: "copy"},
		agent:      agentDef,
		hasPrompt:  false,
	}

	mounts := buildMounts(state, "")

	for _, m := range mounts {
		assert.NotEqual(t, "/yoloai/prompt.txt", m.Target, "should not include prompt mount")
	}
}

func TestBuildMounts_IncludesSecrets(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	state := &sandboxState{
		sandboxDir: "/sandbox",
		workdir:    &DirArg{Path: "/project", Mode: "copy"},
		agent:      agentDef,
	}

	secretsDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(secretsDir, "ANTHROPIC_API_KEY"), []byte("key"), 0600))

	mounts := buildMounts(state, secretsDir)

	var found bool
	for _, m := range mounts {
		if m.Target == "/run/secrets/ANTHROPIC_API_KEY" {
			found = true
			assert.True(t, m.ReadOnly)
		}
	}
	assert.True(t, found, "should include secrets mount")
}

// printCreationOutput tests

func TestPrintCreationOutput_Basic(t *testing.T) {
	var buf bytes.Buffer
	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), &buf)

	agentDef := agent.GetAgent("claude")
	state := &sandboxState{
		name:    "test-sandbox",
		workdir: &DirArg{Path: "/project", Mode: "copy"},
		agent:   agentDef,
	}

	mgr.printCreationOutput(state, false)

	output := buf.String()
	assert.Contains(t, output, "test-sandbox")
	assert.Contains(t, output, "claude")
	assert.Contains(t, output, "/project")
	assert.Contains(t, output, "copy")
	assert.Contains(t, output, "attach")
}

func TestPrintCreationOutput_AutoAttach(t *testing.T) {
	var buf bytes.Buffer
	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), &buf)

	state := &sandboxState{
		name:    "test",
		workdir: &DirArg{Path: "/project", Mode: "copy"},
		agent:   agent.GetAgent("test"),
	}

	mgr.printCreationOutput(state, true)

	// autoAttach=true suppresses the attach hint
	assert.NotContains(t, buf.String(), "attach")
}

func TestPrintCreationOutput_WithPrompt(t *testing.T) {
	var buf bytes.Buffer
	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), &buf)

	state := &sandboxState{
		name:      "test",
		workdir:   &DirArg{Path: "/project", Mode: "copy"},
		agent:     agent.GetAgent("test"),
		hasPrompt: true,
	}

	mgr.printCreationOutput(state, false)

	assert.Contains(t, buf.String(), "diff")
}

func TestPrintCreationOutput_NetworkNone(t *testing.T) {
	var buf bytes.Buffer
	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), &buf)

	state := &sandboxState{
		name:        "test",
		workdir:     &DirArg{Path: "/project", Mode: "copy"},
		agent:       agent.GetAgent("test"),
		networkMode: "none",
	}

	mgr.printCreationOutput(state, false)

	assert.Contains(t, buf.String(), "Network:  none")
}

func TestPrintCreationOutput_WithPorts(t *testing.T) {
	var buf bytes.Buffer
	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), &buf)

	state := &sandboxState{
		name:    "test",
		workdir: &DirArg{Path: "/project", Mode: "copy"},
		agent:   agent.GetAgent("test"),
		ports:   []string{"3000:3000", "8080:80"},
	}

	mgr.printCreationOutput(state, false)

	assert.Contains(t, buf.String(), "3000:3000")
	assert.Contains(t, buf.String(), "8080:80")
}

func TestPrintCreationOutput_NilState(t *testing.T) {
	var buf bytes.Buffer
	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), &buf)

	mgr.printCreationOutput(nil, false)

	assert.Empty(t, buf.String())
}

// prepareSandboxState validation tests

func TestPrepareSandboxState_MissingName(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), io.Discard)

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:       "",
		WorkdirArg: tmpDir,
		Agent:      "test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestPrepareSandboxState_UnknownAgent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), io.Discard)

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:       "test",
		WorkdirArg: tmpDir,
		Agent:      "nonexistent-agent",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown agent")
}

func TestPrepareSandboxState_WorkdirMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), io.Discard)

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:       "test",
		WorkdirArg: "/nonexistent/path",
		Agent:      "test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workdir does not exist")
}

func TestPrepareSandboxState_SandboxExists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create existing sandbox dir with valid meta.json
	sandboxDir := filepath.Join(tmpDir, ".yoloai", "sandboxes", "existing")
	require.NoError(t, os.MkdirAll(sandboxDir, 0750))
	require.NoError(t, SaveMeta(sandboxDir, &Meta{
		Name:  "existing",
		Agent: "test",
	}))

	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), io.Discard)

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:       "existing",
		WorkdirArg: tmpDir,
		Agent:      "test",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSandboxExists)
}

func TestPrepareSandboxState_ConflictingPromptFlags(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), io.Discard)

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:       "test",
		WorkdirArg: tmpDir,
		Agent:      "test",
		Prompt:     "hello",
		PromptFile: "/some/file",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestPrepareSandboxState_MissingAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("ANTHROPIC_API_KEY", "")

	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), io.Discard)

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:       "test",
		WorkdirArg: tmpDir,
		Agent:      "claude",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingAPIKey)
}

func TestPrepareSandboxState_DangerousDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), io.Discard)

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:       "test",
		WorkdirArg: "/",
		Agent:      "claude",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dangerous directory")
}

func TestPrepareSandboxState_DangerousDirForce(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	// HOME is classified as dangerous. Use :rw:force to avoid copying.
	var buf bytes.Buffer
	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader("y\n"), &buf)

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:       "test",
		WorkdirArg: tmpDir + ":rw:force",
		Agent:      "claude",
	})
	// Should NOT fail on "dangerous directory" — :force bypasses it.
	if err != nil {
		assert.NotContains(t, err.Error(), "dangerous directory")
	}
	assert.Contains(t, buf.String(), "WARNING: mounting dangerous directory")
}

// Keychain fallback tests

func TestHasAnyAuthFile_KeychainFallback(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// No credentials file on disk
	agentDef := agent.GetAgent("claude")

	// Override keychainReader to return credentials
	origReader := keychainReader
	keychainReader = func(service string) ([]byte, error) {
		if service == "Claude Code-credentials" {
			return []byte(`{"token":"from-keychain"}`), nil
		}
		return nil, fmt.Errorf("not found")
	}
	defer func() { keychainReader = origReader }()

	assert.True(t, hasAnyAuthFile(agentDef))
}

func TestHasAnyAuthFile_KeychainFallbackFails(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	agentDef := agent.GetAgent("claude")

	// Override keychainReader to always fail
	origReader := keychainReader
	keychainReader = func(_ string) ([]byte, error) {
		return nil, fmt.Errorf("not found")
	}
	defer func() { keychainReader = origReader }()

	assert.False(t, hasAnyAuthFile(agentDef))
}

func TestCopySeedFiles_KeychainFallback(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")

	// Override keychainReader to return credentials
	origReader := keychainReader
	keychainReader = func(service string) ([]byte, error) {
		if service == "Claude Code-credentials" {
			return []byte(`{"token":"from-keychain"}`), nil
		}
		return nil, fmt.Errorf("not found")
	}
	defer func() { keychainReader = origReader }()

	copied, err := copySeedFiles(agentDef, sandboxDir, false) // hasAPIKey=false
	require.NoError(t, err)
	assert.True(t, copied)

	// Credentials from keychain should be written to agent-state
	data, err := os.ReadFile(filepath.Join(sandboxDir, "agent-state", ".credentials.json")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, `{"token":"from-keychain"}`, string(data))
}

func TestCopySeedFiles_KeychainSkippedWhenFileExists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create the credentials file on disk
	claudeDir := filepath.Join(tmpDir, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{"token":"from-file"}`), 0600))

	sandboxDir := filepath.Join(tmpDir, "sandbox")
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "agent-state"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(sandboxDir, "home-seed"), 0750))

	agentDef := agent.GetAgent("claude")

	// Override keychainReader — should NOT be called since file exists
	origReader := keychainReader
	keychainCalled := false
	keychainReader = func(_ string) ([]byte, error) {
		keychainCalled = true
		return []byte(`{"token":"from-keychain"}`), nil
	}
	defer func() { keychainReader = origReader }()

	copied, err := copySeedFiles(agentDef, sandboxDir, false)
	require.NoError(t, err)
	assert.True(t, copied)
	assert.False(t, keychainCalled, "keychainReader should not be called when file exists")

	// Should have the file contents, not keychain
	data, err := os.ReadFile(filepath.Join(sandboxDir, "agent-state", ".credentials.json")) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, `{"token":"from-file"}`, string(data))
}

// hasAnyAuthHint tests

func TestHasAnyAuthHint_NoHintVars(t *testing.T) {
	agentDef := agent.GetAgent("claude")
	assert.False(t, hasAnyAuthHint(agentDef, nil))
}

func TestHasAnyAuthHint_HostEnvSet(t *testing.T) {
	agentDef := agent.GetAgent("aider")
	t.Setenv("OLLAMA_API_BASE", "http://localhost:11434")
	assert.True(t, hasAnyAuthHint(agentDef, nil))
}

func TestHasAnyAuthHint_ConfigEnvSet(t *testing.T) {
	agentDef := agent.GetAgent("aider")
	configEnv := map[string]string{
		"OLLAMA_API_BASE": "http://localhost:11434",
	}
	assert.True(t, hasAnyAuthHint(agentDef, configEnv))
}

func TestHasAnyAuthHint_NeitherSet(t *testing.T) {
	agentDef := agent.GetAgent("aider")
	// Clear all aider's AuthHintEnvVars
	for _, key := range agentDef.AuthHintEnvVars {
		t.Setenv(key, "")
	}
	assert.False(t, hasAnyAuthHint(agentDef, nil))
}

// Error message tests

func TestPrepareSandboxState_MissingAPIKeyErrorNoEmptyParens(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	agentDef := agent.GetAgent("aider")
	// Clear all aider API key env vars
	for _, key := range agentDef.APIKeyEnvVars {
		t.Setenv(key, "")
	}
	// Clear all aider auth hint env vars
	for _, key := range agentDef.AuthHintEnvVars {
		t.Setenv(key, "")
	}

	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), io.Discard)

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:       "test",
		WorkdirArg: tmpDir,
		Agent:      "aider",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingAPIKey)
	errMsg := err.Error()
	assert.NotContains(t, errMsg, "()", "error message should not contain empty parens")
	assert.Contains(t, errMsg, "local models", "error should mention local models")
	assert.Contains(t, errMsg, "OLLAMA_API_BASE", "error should mention OLLAMA_API_BASE")
}

func TestPrepareSandboxState_MissingAPIKeyErrorWithAuthFiles(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("ANTHROPIC_API_KEY", "")

	// Override keychainReader to fail
	origReader := keychainReader
	keychainReader = func(_ string) ([]byte, error) {
		return nil, fmt.Errorf("not found")
	}
	defer func() { keychainReader = origReader }()

	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), io.Discard)

	_, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:       "test",
		WorkdirArg: tmpDir,
		Agent:      "claude",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingAPIKey)
	errMsg := err.Error()
	assert.Contains(t, errMsg, ".credentials.json", "error should mention .credentials.json from AuthOnly seed files")
	assert.NotContains(t, errMsg, "local models", "claude has no AuthHintEnvVars, should not mention local models")
}

func TestPrintCreationOutput_NetworkIsolated(t *testing.T) {
	var buf bytes.Buffer
	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader(""), &buf)

	state := &sandboxState{
		name:         "test",
		workdir:      &DirArg{Path: "/project", Mode: "copy"},
		agent:        agent.GetAgent("test"),
		networkMode:  "isolated",
		networkAllow: []string{"api.anthropic.com", "sentry.io"},
	}

	mgr.printCreationOutput(state, false)

	assert.Contains(t, buf.String(), "Network:  isolated (2 allowed domains)")
}

func TestPrepareSandboxState_NetworkIsolatedSetsAllowlist(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	// Create a workdir subdirectory to avoid dangerous directory detection
	workDir := filepath.Join(tmpDir, "project")
	require.NoError(t, os.MkdirAll(workDir, 0750))

	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader("y\n"), io.Discard)

	state, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:            "test",
		WorkdirArg:      workDir,
		Agent:           "claude",
		NetworkIsolated: true,
		Version:         "test",
	})
	require.NoError(t, err)
	require.NotNil(t, state)

	assert.Equal(t, "isolated", state.networkMode)
	assert.Contains(t, state.networkAllow, "api.anthropic.com")
	assert.Contains(t, state.networkAllow, "statsig.anthropic.com")
	assert.Contains(t, state.networkAllow, "sentry.io")
}

func TestPrepareSandboxState_NetworkAllowAddsExtraDomains(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	// Create a workdir subdirectory to avoid dangerous directory detection
	workDir := filepath.Join(tmpDir, "project")
	require.NoError(t, os.MkdirAll(workDir, 0750))

	mgr := NewManager(&mockRuntime{}, "docker", slog.Default(), strings.NewReader("y\n"), io.Discard)

	state, err := mgr.prepareSandboxState(context.TODO(), CreateOptions{
		Name:            "test",
		WorkdirArg:      workDir,
		Agent:           "claude",
		NetworkIsolated: true,
		NetworkAllow:    []string{"api.example.com"},
		Version:         "test",
	})
	require.NoError(t, err)
	require.NotNil(t, state)

	assert.Equal(t, "isolated", state.networkMode)
	assert.Contains(t, state.networkAllow, "api.anthropic.com")
	assert.Contains(t, state.networkAllow, "api.example.com")
}
