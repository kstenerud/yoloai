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

func TestRunNewUserSetup_LargeConfig_AutoConfigures(t *testing.T) {
	mgr, _, tmpDir := setupTestManager(t, "")

	// Create a large tmux config
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
	mgr, output, _ := setupTestManager(t, "y\n")

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default", cfg.TmuxConf)
	assert.Contains(t, output.String(), "Setup complete")
}

func TestRunNewUserSetup_NoConfig_AnswerEmpty(t *testing.T) {
	mgr, _, _ := setupTestManager(t, "\n")

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default", cfg.TmuxConf)
}

func TestRunNewUserSetup_NoConfig_AnswerN(t *testing.T) {
	mgr, _, _ := setupTestManager(t, "n\n")

	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "none", cfg.TmuxConf)
}

func TestRunNewUserSetup_NoConfig_AnswerP(t *testing.T) {
	mgr, output, _ := setupTestManager(t, "p\n")

	err := mgr.runNewUserSetup(context.Background())
	assert.ErrorIs(t, err, errSetupPreview)

	// setup_complete should NOT be set
	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.False(t, cfg.SetupComplete)

	// Should print yoloai defaults
	assert.Contains(t, output.String(), "yoloai defaults")
}

func TestRunNewUserSetup_SmallConfig_AnswerY(t *testing.T) {
	mgr, _, tmpDir := setupTestManager(t, "y\n")

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
	mgr, _, tmpDir := setupTestManager(t, "n\n")

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
	mgr, output, tmpDir := setupTestManager(t, "p\n")

	tmuxConf := "set -g mouse on\n"
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, ".tmux.conf"), []byte(tmuxConf), 0600))

	err := mgr.runNewUserSetup(context.Background())
	assert.ErrorIs(t, err, errSetupPreview)

	// Should print both sections
	assert.Contains(t, output.String(), "yoloai defaults")
	assert.Contains(t, output.String(), "your config")
}

func TestRunNewUserSetup_EOF_DefaultsToY(t *testing.T) {
	// Empty reader simulates EOF (non-TTY with no input)
	mgr, _, _ := setupTestManager(t, "")

	// No tmux config → noConfig path. EOF from scanner → answer is ""
	// which maps to the default (Y) behavior.
	err := mgr.runNewUserSetup(context.Background())
	require.NoError(t, err)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	assert.True(t, cfg.SetupComplete)
	assert.Equal(t, "default", cfg.TmuxConf)
}

// mockRuntime is defined in manager_test.go — we can use it here since
// both are in the sandbox package. This test just needs a valid Manager.
var _ io.Reader = strings.NewReader("") // compile check
