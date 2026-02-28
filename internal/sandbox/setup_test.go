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
// with a default config.yaml. Returns the Manager, output buffer, and HOME dir.
func setupTestManager(t *testing.T, input string) (*Manager, *bytes.Buffer, string) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	require.NoError(t, os.MkdirAll(yoloaiDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(defaultConfigYAML), 0600))

	var output bytes.Buffer
	mock := &mockRuntime{}
	mgr := NewManager(mock, "docker", slog.Default(), strings.NewReader(input), &output)
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

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default+host", cfg.TmuxConf)
}

func TestRunNewUserSetup_NoConfig_AnswerY(t *testing.T) {
	setLinuxPlatform(t)
	// tmux=y, agent=default(enter)
	mgr, output, _ := setupTestManager(t, "y\n\n")

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default", cfg.TmuxConf)
	assert.Contains(t, output.String(), "Setup complete")
}

func TestRunNewUserSetup_NoConfig_AnswerEmpty(t *testing.T) {
	setLinuxPlatform(t)
	// tmux=default(enter), agent=default(enter)
	mgr, _, _ := setupTestManager(t, "\n\n")

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default", cfg.TmuxConf)
}

func TestRunNewUserSetup_NoConfig_AnswerN(t *testing.T) {
	setLinuxPlatform(t)
	// tmux=n, agent=default(enter)
	mgr, _, _ := setupTestManager(t, "n\n\n")

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "none", cfg.TmuxConf)
}

func TestRunNewUserSetup_NoConfig_AnswerP(t *testing.T) {
	setLinuxPlatform(t)
	mgr, output, _ := setupTestManager(t, "p\n")

	err := mgr.runNewUserSetup(context.Background())
	assert.ErrorIs(t, err, errSetupPreview)

	// setup_complete should NOT be set (preview exits early)
	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.False(t, cfg.SetupComplete)

	assert.Contains(t, output.String(), "yoloai defaults")
}

func TestRunNewUserSetup_SmallConfig_AnswerY(t *testing.T) {
	setLinuxPlatform(t)
	// tmux=y, agent=default(enter)
	mgr, _, tmpDir := setupTestManager(t, "y\n\n")

	tmuxConf := "set -g mouse on\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default+host", cfg.TmuxConf)
}

func TestRunNewUserSetup_SmallConfig_AnswerN(t *testing.T) {
	setLinuxPlatform(t)
	// tmux=n, agent=default(enter)
	mgr, _, tmpDir := setupTestManager(t, "n\n\n")

	tmuxConf := "set -g mouse on\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "host", cfg.TmuxConf)
}

func TestRunNewUserSetup_SmallConfig_AnswerP(t *testing.T) {
	setLinuxPlatform(t)
	mgr, output, tmpDir := setupTestManager(t, "p\n")

	tmuxConf := "set -g mouse on\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	err := mgr.runNewUserSetup(context.Background())
	assert.ErrorIs(t, err, errSetupPreview)

	assert.Contains(t, output.String(), "yoloai defaults")
	assert.Contains(t, output.String(), "your config")
}

func TestRunNewUserSetup_EOF_DefaultsToY(t *testing.T) {
	setLinuxPlatform(t)
	// Empty reader simulates EOF — all prompts get default
	mgr, _, _ := setupTestManager(t, "")

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default", cfg.TmuxConf)
}

// --- Backend prompt tests ---

func TestAvailableBackends_Linux(t *testing.T) {
	setLinuxPlatform(t)
	backends := availableBackends()
	assert.Len(t, backends, 1)
	assert.Equal(t, "docker", backends[0].name)
}

func TestAvailableBackends_MacOSARM(t *testing.T) {
	setMacOSARMPlatform(t)
	backends := availableBackends()
	assert.Len(t, backends, 3)
	assert.Equal(t, "docker", backends[0].name)
	assert.Equal(t, "seatbelt", backends[1].name)
	assert.Equal(t, "tart", backends[2].name)
}

func TestAvailableBackends_MacOSIntel(t *testing.T) {
	setMacOSIntelPlatform(t)
	backends := availableBackends()
	assert.Len(t, backends, 2)
	assert.Equal(t, "docker", backends[0].name)
	assert.Equal(t, "seatbelt", backends[1].name)
}

func TestPromptBackendSetup_SkippedOnLinux(t *testing.T) {
	setLinuxPlatform(t)
	mgr, output, _ := setupTestManager(t, "")

	err := mgr.promptBackendSetup(context.Background())
	require.NoError(t, err)
	assert.Empty(t, output.String())
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

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "docker", cfg.Backend)
}

func TestPromptBackendSetup_SelectSeatbelt(t *testing.T) {
	setMacOSARMPlatform(t)
	mgr, _, _ := setupTestManager(t, "2\n")

	err := mgr.promptBackendSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "seatbelt", cfg.Backend)
}

func TestPromptBackendSetup_SelectTart(t *testing.T) {
	setMacOSARMPlatform(t)
	mgr, _, _ := setupTestManager(t, "3\n")

	err := mgr.promptBackendSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "tart", cfg.Backend)
}

func TestPromptBackendSetup_InvalidInputDefaultsToFirst(t *testing.T) {
	setMacOSARMPlatform(t)
	mgr, _, _ := setupTestManager(t, "xyz\n")

	err := mgr.promptBackendSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
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

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "claude", cfg.Agent)
}

func TestPromptAgentSetup_SelectSecond(t *testing.T) {
	mgr, _, _ := setupTestManager(t, "2\n")

	err := mgr.promptAgentSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.Equal(t, "codex", cfg.Agent)
}

// --- Full multi-step flow tests ---

func TestRunNewUserSetup_FullFlow_MacOS(t *testing.T) {
	setMacOSARMPlatform(t)
	// tmux=y, backend=2(seatbelt), agent=3(gemini)
	mgr, output, _ := setupTestManager(t, "y\n2\n3\n")

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default", cfg.TmuxConf)
	assert.Equal(t, "seatbelt", cfg.Backend)
	assert.Equal(t, "gemini", cfg.Agent)
	assert.Contains(t, output.String(), "Setup complete")
}

func TestRunNewUserSetup_FullFlow_Linux_SkipsBackend(t *testing.T) {
	setLinuxPlatform(t)
	// tmux=y, (no backend prompt), agent=1(claude)
	mgr, output, _ := setupTestManager(t, "y\n1\n")

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default", cfg.TmuxConf)
	assert.Equal(t, "claude", cfg.Agent)
	assert.NotContains(t, output.String(), "Default runtime backend")
	assert.Contains(t, output.String(), "Default agent")
}

func TestRunNewUserSetup_LargeConfig_StillAsksBackendAndAgent(t *testing.T) {
	setMacOSARMPlatform(t)
	// Large tmux → auto-configure, but backend=3(tart), agent=3(gemini)
	mgr, output, tmpDir := setupTestManager(t, "3\n3\n")

	tmuxConf := strings.Repeat("set -g option value\n", 15)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default+host", cfg.TmuxConf)
	assert.Equal(t, "tart", cfg.Backend)
	assert.Equal(t, "gemini", cfg.Agent)
	assert.Contains(t, output.String(), "Default runtime backend")
	assert.Contains(t, output.String(), "Default agent")
}

// mockRuntime is defined in manager_test.go — we can use it here since
// both are in the sandbox package. This test just needs a valid Manager.
var _ io.Reader = strings.NewReader("") // compile check
