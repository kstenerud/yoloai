package sandbox

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/config"
)

func TestCountSignificantLines(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{"empty", "", 0},
		{"blank lines only", "\n\n\n", 0},
		{"comments only", "# comment\n# another\n", 0},
		{"one setting", "set -g mouse on\n", 1},
		{"mixed", "# comment\nset -g mouse on\n\n# another\nset -g base-index 1\n", 2},
		{"threshold boundary", strings.Repeat("set -g option value\n", 10), 10},
		{"above threshold", strings.Repeat("set -g option value\n", 11), 11},
		{"whitespace prefix", "  # indented comment\n  set -g mouse on\n", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countSignificantLines(tt.content)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestClassifyTmuxConfig_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	class, content := classifyTmuxConfig()
	assert.Equal(t, tmuxConfigNone, class)
	assert.Empty(t, content)
}

func TestClassifyTmuxConfig_Small(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	tmuxConf := "set -g mouse on\nset -g base-index 1\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	class, content := classifyTmuxConfig()
	assert.Equal(t, tmuxConfigSmall, class)
	assert.Equal(t, tmuxConf, content)
}

func TestClassifyTmuxConfig_Large(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	tmuxConf := strings.Repeat("set -g option value\n", 15)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	class, _ := classifyTmuxConfig()
	assert.Equal(t, tmuxConfigLarge, class)
}

// setupTestManager creates a Manager with the given input and a temp HOME
// with profiles/base/config.yaml, global config.yaml, and state.yaml. Returns the Manager, output buffer, and HOME dir.
func setupTestManager(t *testing.T, input string) (*Manager, *bytes.Buffer, string) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	baseDir := filepath.Join(yoloaiDir, "profiles", "base")
	require.NoError(t, os.MkdirAll(baseDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(baseDir, "config.yaml"), []byte(config.DefaultConfigYAML), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(config.DefaultGlobalConfigYAML), 0600))
	require.NoError(t, config.SaveState(&config.State{}))

	var output bytes.Buffer
	mock := &mockRuntime{}
	mgr := NewManager(mock, slog.Default(), strings.NewReader(input), &output)
	return mgr, &output, tmpDir
}

// setLinuxPlatform overrides platform detection to simulate Linux.
func setLinuxPlatform(t *testing.T) {
	t.Helper()
	origOS := detectedOS
	origArch := detectedArch
	detectedOS = func() string { return "linux" }
	detectedArch = func() string { return "amd64" }
	t.Cleanup(func() {
		detectedOS = origOS
		detectedArch = origArch
	})
}

// setMacOSARMPlatform overrides platform detection to simulate macOS Apple Silicon.
func setMacOSARMPlatform(t *testing.T) {
	t.Helper()
	origOS := detectedOS
	origArch := detectedArch
	detectedOS = func() string { return "darwin" }
	detectedArch = func() string { return "arm64" }
	t.Cleanup(func() {
		detectedOS = origOS
		detectedArch = origArch
	})
}

// setMacOSIntelPlatform overrides platform detection to simulate macOS Intel.
func setMacOSIntelPlatform(t *testing.T) {
	t.Helper()
	origOS := detectedOS
	origArch := detectedArch
	detectedOS = func() string { return "darwin" }
	detectedArch = func() string { return "amd64" }
	t.Cleanup(func() {
		detectedOS = origOS
		detectedArch = origArch
	})
}

// --- Tmux-only tests (Linux: no backend/agent prompts needed) ---

func TestRunNewUserSetup_LargeConfig_AutoConfigures(t *testing.T) {
	setLinuxPlatform(t)
	// Large tmux config → auto-configure, no tmux prompt.
	// Linux → single backend, single-agent prompts also skipped if only 1.
	// Provide empty line for agent prompt (claude & gemini = 2 agents).
	mgr, _, tmpDir := setupTestManager(t, "\n")

	tmuxConf := strings.Repeat("set -g option value\n", 15)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{})
	require.NoError(t, err)

	state, err := config.LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)

	gcfg, err := config.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "default+host", gcfg.TmuxConf)
}

func TestRunNewUserSetup_NoConfig_AnswerY(t *testing.T) {
	setLinuxPlatform(t)
	// tmux=y, agent=default(enter)
	mgr, output, _ := setupTestManager(t, "y\n\n")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{})
	require.NoError(t, err)

	state, err := config.LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)

	gcfg, err := config.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "default", gcfg.TmuxConf)
	assert.Contains(t, output.String(), "Setup complete")
}

func TestRunNewUserSetup_NoConfig_AnswerEmpty(t *testing.T) {
	setLinuxPlatform(t)
	// tmux=default(enter), agent=default(enter)
	mgr, _, _ := setupTestManager(t, "\n\n")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{})
	require.NoError(t, err)

	state, err := config.LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)

	gcfg, err := config.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "default", gcfg.TmuxConf)
}

func TestRunNewUserSetup_NoConfig_AnswerN(t *testing.T) {
	setLinuxPlatform(t)
	// tmux=n, agent=default(enter)
	mgr, _, _ := setupTestManager(t, "n\n\n")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{})
	require.NoError(t, err)

	state, err := config.LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)

	gcfg, err := config.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "none", gcfg.TmuxConf)
}

func TestRunNewUserSetup_NoConfig_AnswerP(t *testing.T) {
	setLinuxPlatform(t)
	mgr, output, _ := setupTestManager(t, "p\n")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{})
	assert.ErrorIs(t, err, errSetupPreview)

	// setup_complete should NOT be set (preview exits early)
	state, err := config.LoadState()
	require.NoError(t, err)
	assert.False(t, state.SetupComplete)

	assert.Contains(t, output.String(), "yoloai defaults")
}

func TestRunNewUserSetup_SmallConfig_AnswerY(t *testing.T) {
	setLinuxPlatform(t)
	// tmux=y, agent=default(enter)
	mgr, _, tmpDir := setupTestManager(t, "y\n\n")

	tmuxConf := "set -g mouse on\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{})
	require.NoError(t, err)

	state, err := config.LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)

	gcfg, err := config.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "default+host", gcfg.TmuxConf)
}

func TestRunNewUserSetup_SmallConfig_AnswerN(t *testing.T) {
	setLinuxPlatform(t)
	// tmux=n, agent=default(enter)
	mgr, _, tmpDir := setupTestManager(t, "n\n\n")

	tmuxConf := "set -g mouse on\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{})
	require.NoError(t, err)

	state, err := config.LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)

	gcfg, err := config.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "host", gcfg.TmuxConf)
}

func TestRunNewUserSetup_SmallConfig_AnswerP(t *testing.T) {
	setLinuxPlatform(t)
	mgr, output, tmpDir := setupTestManager(t, "p\n")

	tmuxConf := "set -g mouse on\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{})
	assert.ErrorIs(t, err, errSetupPreview)

	assert.Contains(t, output.String(), "yoloai defaults")
	assert.Contains(t, output.String(), "your config")
}

func TestRunNewUserSetup_EOF_DefaultsToY(t *testing.T) {
	setLinuxPlatform(t)
	// Empty reader simulates EOF — all prompts get default
	mgr, _, _ := setupTestManager(t, "")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{})
	require.NoError(t, err)

	state, err := config.LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)

	gcfg, err := config.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "default", gcfg.TmuxConf)
}

// --- Backend prompt tests ---

func TestAvailableBackends_Linux(t *testing.T) {
	setLinuxPlatform(t)
	backends := availableBackends()
	assert.Len(t, backends, 2)
	assert.Equal(t, "docker", backends[0].name)
	assert.Equal(t, "podman", backends[1].name)
}

func TestAvailableBackends_MacOSARM(t *testing.T) {
	setMacOSARMPlatform(t)
	backends := availableBackends()
	assert.Len(t, backends, 4)
	assert.Equal(t, "docker", backends[0].name)
	assert.Equal(t, "podman", backends[1].name)
	assert.Equal(t, "seatbelt", backends[2].name)
	assert.Equal(t, "tart", backends[3].name)
}

func TestAvailableBackends_MacOSIntel(t *testing.T) {
	setMacOSIntelPlatform(t)
	backends := availableBackends()
	assert.Len(t, backends, 3)
	assert.Equal(t, "docker", backends[0].name)
	assert.Equal(t, "podman", backends[1].name)
	assert.Equal(t, "seatbelt", backends[2].name)
}

func TestPromptBackendSetup_ShownOnLinux(t *testing.T) {
	setLinuxPlatform(t)
	mgr, output, _ := setupTestManager(t, "\n")

	err := mgr.promptBackendSetup(context.Background())
	require.NoError(t, err)
	assert.Contains(t, output.String(), "Default runtime backend")
	assert.Contains(t, output.String(), "docker")
	assert.Contains(t, output.String(), "podman")

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "docker", cfg.Backend)
}

func TestPromptBackendSetup_ShownOnMacOS(t *testing.T) {
	setMacOSARMPlatform(t)
	// Select default (enter)
	mgr, output, _ := setupTestManager(t, "\n")

	err := mgr.promptBackendSetup(context.Background())
	require.NoError(t, err)

	assert.Contains(t, output.String(), "Default runtime backend")
	assert.Contains(t, output.String(), "docker")
	assert.Contains(t, output.String(), "seatbelt")
	assert.Contains(t, output.String(), "tart")

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "docker", cfg.Backend)
}

func TestPromptBackendSetup_SelectSeatbelt(t *testing.T) {
	setMacOSARMPlatform(t)
	mgr, _, _ := setupTestManager(t, "3\n")

	err := mgr.promptBackendSetup(context.Background())
	require.NoError(t, err)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "seatbelt", cfg.Backend)
}

func TestPromptBackendSetup_SelectTart(t *testing.T) {
	setMacOSARMPlatform(t)
	mgr, _, _ := setupTestManager(t, "4\n")

	err := mgr.promptBackendSetup(context.Background())
	require.NoError(t, err)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "tart", cfg.Backend)
}

func TestPromptBackendSetup_InvalidInputDefaultsToFirst(t *testing.T) {
	setMacOSARMPlatform(t)
	mgr, _, _ := setupTestManager(t, "xyz\n")

	err := mgr.promptBackendSetup(context.Background())
	require.NoError(t, err)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "docker", cfg.Backend)
}

// --- Agent prompt tests ---

func TestAvailableAgents_ExcludesTest(t *testing.T) {
	agents := availableAgents()
	for _, a := range agents {
		assert.NotEqual(t, "test", a.name)
		assert.NotEqual(t, "shell", a.name)
	}
	assert.GreaterOrEqual(t, len(agents), 2, "should have at least claude and gemini")
}

func TestPromptAgentSetup_DefaultSelectsFirst(t *testing.T) {
	mgr, output, _ := setupTestManager(t, "\n")

	err := mgr.promptAgentSetup(context.Background())
	require.NoError(t, err)

	assert.Contains(t, output.String(), "Default agent")

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "aider", cfg.Agent)
}

func TestPromptAgentSetup_SelectSecond(t *testing.T) {
	mgr, _, _ := setupTestManager(t, "2\n")

	err := mgr.promptAgentSetup(context.Background())
	require.NoError(t, err)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "claude", cfg.Agent)
}

// --- Full multi-step flow tests ---

func TestRunNewUserSetup_FullFlow_MacOS(t *testing.T) {
	setMacOSARMPlatform(t)
	// tmux=y, backend=3(seatbelt), agent=3(codex)
	mgr, output, _ := setupTestManager(t, "y\n3\n3\n")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{})
	require.NoError(t, err)

	state, err := config.LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)

	gcfg, err := config.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "default", gcfg.TmuxConf)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "seatbelt", cfg.Backend)
	assert.Equal(t, "codex", cfg.Agent)
	assert.Contains(t, output.String(), "Setup complete")
}

func TestRunNewUserSetup_FullFlow_Linux_ShowsBackend(t *testing.T) {
	setLinuxPlatform(t)
	// tmux=y, backend=1(docker), agent=1(aider)
	mgr, output, _ := setupTestManager(t, "y\n1\n1\n")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{})
	require.NoError(t, err)

	state, err := config.LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)

	gcfg, err := config.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "default", gcfg.TmuxConf)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "docker", cfg.Backend)
	assert.Equal(t, "aider", cfg.Agent)
	assert.Contains(t, output.String(), "Default runtime backend")
	assert.Contains(t, output.String(), "Default agent")
}

func TestRunNewUserSetup_LargeConfig_StillAsksBackendAndAgent(t *testing.T) {
	setMacOSARMPlatform(t)
	// Large tmux → auto-configure, but backend=4(tart), agent=3(codex)
	mgr, output, tmpDir := setupTestManager(t, "4\n3\n")

	tmuxConf := strings.Repeat("set -g option value\n", 15)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{})
	require.NoError(t, err)

	state, err := config.LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)

	gcfg, err := config.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "default+host", gcfg.TmuxConf)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "tart", cfg.Backend)
	assert.Equal(t, "codex", cfg.Agent)
	assert.Contains(t, output.String(), "Default runtime backend")
	assert.Contains(t, output.String(), "Default agent")
}

// --- Flag-based setup tests ---

func TestRunNewUserSetup_WithAgentFlag(t *testing.T) {
	setLinuxPlatform(t)
	// tmux prompt needs input, backend skipped on Linux, agent flag skips agent prompt
	mgr, output, _ := setupTestManager(t, "y\n")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{Agent: "claude"})
	require.NoError(t, err)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "claude", cfg.Agent)
	assert.NotContains(t, output.String(), "Default agent")
}

func TestRunNewUserSetup_WithBackendFlag(t *testing.T) {
	setMacOSARMPlatform(t)
	// tmux prompt needs input, backend flag skips backend prompt, agent prompt needs input
	mgr, output, _ := setupTestManager(t, "y\n\n")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{Backend: "seatbelt"})
	require.NoError(t, err)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "seatbelt", cfg.Backend)
	assert.NotContains(t, output.String(), "Default runtime backend")
	assert.Contains(t, output.String(), "Default agent")
}

func TestRunNewUserSetup_WithTmuxConfFlag(t *testing.T) {
	setLinuxPlatform(t)
	// tmux flag skips tmux prompt, backend skipped on Linux, agent prompt needs input
	mgr, output, _ := setupTestManager(t, "\n")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{TmuxConf: "host"})
	require.NoError(t, err)

	gcfg, err := config.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "host", gcfg.TmuxConf)
	assert.NotContains(t, output.String(), "tmux")
}

func TestRunNewUserSetup_AllFlags_NonInteractive(t *testing.T) {
	setMacOSARMPlatform(t)
	// All flags provided — no prompts, empty input
	mgr, output, _ := setupTestManager(t, "")

	opts := SetupOptions{
		Agent:    "gemini",
		Backend:  "docker",
		TmuxConf: "default",
	}
	err := mgr.runNewUserSetup(context.Background(), opts)
	require.NoError(t, err)

	state, err := config.LoadState()
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)

	gcfg, err := config.LoadGlobalConfig()
	require.NoError(t, err)
	assert.Equal(t, "default", gcfg.TmuxConf)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "docker", cfg.Backend)
	assert.Equal(t, "gemini", cfg.Agent)
	assert.NotContains(t, output.String(), "Choice")
}

func TestRunNewUserSetup_InvalidAgent_Error(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, _ := setupTestManager(t, "")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{
		TmuxConf: "default",
		Agent:    "nonexistent",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --agent value")
}

func TestRunNewUserSetup_InvalidBackend_Error(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, _ := setupTestManager(t, "")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{
		TmuxConf: "default",
		Backend:  "tart",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --backend value")
}

func TestRunNewUserSetup_InvalidTmuxConf_Error(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, _ := setupTestManager(t, "")

	err := mgr.runNewUserSetup(context.Background(), SetupOptions{TmuxConf: "badvalue"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --tmux-conf value")
}

func TestRunNewUserSetup_BackendDockerOnLinux_OK(t *testing.T) {
	setLinuxPlatform(t)
	// docker is available on Linux — should succeed
	mgr, _, _ := setupTestManager(t, "")

	opts := SetupOptions{
		TmuxConf: "default",
		Backend:  "docker",
		Agent:    "claude",
	}
	err := mgr.runNewUserSetup(context.Background(), opts)
	require.NoError(t, err)

	cfg, err := config.LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "docker", cfg.Backend)
}

// mockRuntime is defined above — we can use it here since
// both are in the sandbox package. This test just needs a valid Manager.
var _ io.Reader = strings.NewReader("") // compile check
