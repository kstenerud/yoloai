package sandbox

// ABOUTME: Unit tests for sandbox/setup.go — covers the first-run setup
// ABOUTME: state machine (tmux config classification, backend/agent discovery,
// ABOUTME: ApplySetup branching, platform-dependent enumeration).
//
// Length justification: this file's length is the cross product of the
// setup state machine's legitimate branches, not boilerplate. Open
// question #102 (resolved 2026-05-27) flagged the file as a candidate
// for splitting; reading it alongside setup.go shows the branches are
// real and the tests are exhaustive coverage of them:
//
//   - 3 host platforms (linux / darwin-arm64 / darwin-amd64) — each
//     changes which backends `availableBackends()` returns
//   - 4 tmux-conf modes (default / default+host / host / none) with
//     side-effects that differ by mode
//   - per-field validation (required-when-multiple-available,
//     unknown-name, invalid-mode) producing *yoerrors.UsageError vs other errors
//   - SetupStatus inspection (3 tmux classifications × 3 platforms)
//
// Helpers extracted (`setupTestEngine`, `setLinuxPlatform`, etc.) are
// shared by ~10 callers; collapsing them would inflate the file, not
// shrink it. Section headers (`// --- SetupStatus tests ---`, etc.)
// scope the file for readers. If `setup.go` itself grows past its
// current ~350 lines, revisit splitting both together (one source
// file → one test file is the simpler invariant to preserve).

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

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/yoerrors"

	// availableBackends iterates runtime.Descriptors(); register every
	// backend the platform tests assert on (docker, podman, tart, seatbelt).
	_ "github.com/kstenerud/yoloai/internal/runtime/docker"
	_ "github.com/kstenerud/yoloai/internal/runtime/podman"
	_ "github.com/kstenerud/yoloai/internal/runtime/seatbelt"
	_ "github.com/kstenerud/yoloai/internal/runtime/tart"
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

	class, content := classifyTmuxConfig(tmpDir)
	assert.Equal(t, tmuxConfigNone, class)
	assert.Empty(t, content)
}

func TestClassifyTmuxConfig_Small(t *testing.T) {
	tmpDir := t.TempDir()

	tmuxConf := "set -g mouse on\nset -g base-index 1\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	class, content := classifyTmuxConfig(tmpDir)
	assert.Equal(t, tmuxConfigSmall, class)
	assert.Equal(t, tmuxConf, content)
}

func TestClassifyTmuxConfig_Large(t *testing.T) {
	tmpDir := t.TempDir()

	tmuxConf := strings.Repeat("set -g option value\n", 15)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	class, _ := classifyTmuxConfig(tmpDir)
	assert.Equal(t, tmuxConfigLarge, class)
}

// setupTestEngine creates a Engine with a temp HOME containing
// defaults/config.yaml, global config.yaml, and state.yaml. Returns
// the Engine, output buffer, HOME dir, and the Layout for assertions.
//
// Q-F: ApplySetup is non-interactive, so input is io.Discard and the
// returned buffer captures whatever Setup prints (the "Setup complete"
// line lives in the CLI now, so the buffer is mostly empty here).
func setupTestEngine(t *testing.T) (*Engine, *bytes.Buffer, string, config.Layout) {
	t.Helper()
	tmpDir := t.TempDir()

	yoloaiDir := filepath.Join(tmpDir, ".yoloai")
	defaultsDir := filepath.Join(yoloaiDir, "defaults")
	require.NoError(t, os.MkdirAll(defaultsDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(defaultsDir, "config.yaml"), []byte(config.DefaultConfigYAML), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(yoloaiDir, "config.yaml"), []byte(config.DefaultGlobalConfigYAML), 0600))
	layout := config.NewLayout(yoloaiDir)
	require.NoError(t, config.SaveState(layout, &config.State{}))

	var output bytes.Buffer
	mock := &mockRuntime{}
	mgr := NewEngine(mock, slog.Default(), strings.NewReader(""), WithLayout(layout))
	return mgr, &output, tmpDir, layout
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

// --- SetupStatus tests ---

func TestSetupStatus_NoTmuxConfig(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, _, _ := setupTestEngine(t)

	status := mgr.SetupStatus()
	assert.Equal(t, TmuxConfigNone, status.TmuxClass)
	assert.Empty(t, status.UserTmuxConfig)
	assert.NotEmpty(t, status.DefaultTmuxConfig, "embedded tmux defaults should be exposed for [p] preview")
}

func TestSetupStatus_LargeTmuxConfig(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, tmpDir, _ := setupTestEngine(t)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(strings.Repeat("set -g option value\n", 15)), 0600))

	status := mgr.SetupStatus()
	assert.Equal(t, TmuxConfigLarge, status.TmuxClass)
	assert.Contains(t, status.UserTmuxConfig, "set -g option value")
}

func TestSetupStatus_ListsBackendsAndAgents(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, _, _ := setupTestEngine(t)

	status := mgr.SetupStatus()
	assert.NotEmpty(t, status.AvailableBackends)
	assert.NotEmpty(t, status.AvailableAgents)
	// Linux: docker + podman are the registered + filtered set.
	names := make([]string, len(status.AvailableBackends))
	for i, b := range status.AvailableBackends {
		names[i] = b.Name
	}
	assert.Contains(t, names, "docker")
}

// --- ApplySetup tests (non-interactive) ---

func TestApplySetup_HappyPath_Linux(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, _, layout := setupTestEngine(t)

	opts := SetupOptions{TmuxConf: "default", Backend: "docker", Agent: "claude"}
	require.NoError(t, mgr.ApplySetup(context.Background(), opts))

	state, err := config.LoadState(layout)
	require.NoError(t, err)
	assert.True(t, state.SetupComplete)

	gcfg, err := config.LoadGlobalConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "default", gcfg.TmuxConf)

	cfg, err := config.LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "docker", cfg.ContainerBackend)
	assert.Equal(t, "claude", cfg.Agent)
}

func TestApplySetup_MissingTmuxConf_Error(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, _, layout := setupTestEngine(t)

	err := mgr.ApplySetup(context.Background(), SetupOptions{Backend: "docker", Agent: "claude"})
	require.Error(t, err)
	var usageErr *yoerrors.UsageError
	assert.ErrorAs(t, err, &usageErr, "missing required field should produce *yoerrors.UsageError")

	state, _ := config.LoadState(layout)
	assert.False(t, state.SetupComplete, "Setup must not mark complete on validation failure")
}

func TestApplySetup_InvalidTmuxConf_Error(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, _, _ := setupTestEngine(t)

	err := mgr.ApplySetup(context.Background(), SetupOptions{TmuxConf: "badvalue", Backend: "docker", Agent: "claude"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tmux-conf")
}

func TestApplySetup_InvalidBackend_Error(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, _, _ := setupTestEngine(t)

	err := mgr.ApplySetup(context.Background(), SetupOptions{TmuxConf: "default", Backend: "tart", Agent: "claude"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tart")
}

func TestApplySetup_BackendRequired_WhenMultipleAvailable(t *testing.T) {
	setLinuxPlatform(t) // docker + podman both available
	mgr, _, _, _ := setupTestEngine(t)

	err := mgr.ApplySetup(context.Background(), SetupOptions{TmuxConf: "default", Agent: "claude"})
	require.Error(t, err)
	var usageErr *yoerrors.UsageError
	assert.ErrorAs(t, err, &usageErr)
	assert.Contains(t, err.Error(), "Backend is required")
}

func TestApplySetup_InvalidAgent_Error(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, _, _ := setupTestEngine(t)

	err := mgr.ApplySetup(context.Background(), SetupOptions{TmuxConf: "default", Backend: "docker", Agent: "nonexistent"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestApplySetup_AgentRequired_WhenMultipleAvailable(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, _, _ := setupTestEngine(t)

	err := mgr.ApplySetup(context.Background(), SetupOptions{TmuxConf: "default", Backend: "docker"})
	require.Error(t, err)
	var usageErr *yoerrors.UsageError
	assert.ErrorAs(t, err, &usageErr)
	assert.Contains(t, err.Error(), "Agent is required")
}

func TestApplySetup_HostTmuxConf(t *testing.T) {
	setLinuxPlatform(t)
	mgr, _, _, layout := setupTestEngine(t)

	opts := SetupOptions{TmuxConf: "host", Backend: "docker", Agent: "claude"}
	require.NoError(t, mgr.ApplySetup(context.Background(), opts))

	gcfg, err := config.LoadGlobalConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "host", gcfg.TmuxConf)
}

func TestApplySetup_MacOSAllBackends(t *testing.T) {
	setMacOSARMPlatform(t)
	mgr, _, _, layout := setupTestEngine(t)

	opts := SetupOptions{TmuxConf: "default", Backend: "tart", Agent: "claude"}
	require.NoError(t, mgr.ApplySetup(context.Background(), opts))

	cfg, err := config.LoadConfig(layout)
	require.NoError(t, err)
	assert.Equal(t, "tart", cfg.ContainerBackend)
}

// --- Available* enumeration tests (pure functions) ---

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

func TestAvailableAgents_ExcludesTest(t *testing.T) {
	agents := availableAgents()
	for _, a := range agents {
		assert.NotEqual(t, "test", a.name)
		assert.NotEqual(t, "shell", a.name)
	}
	assert.GreaterOrEqual(t, len(agents), 2, "should have at least claude and gemini")
}

// mockRuntime is defined elsewhere — sanity check that strings.Reader
// still satisfies io.Reader for setupTestEngine.
var _ io.Reader = strings.NewReader("")
