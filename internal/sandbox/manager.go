package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/runtime"
	"golang.org/x/term"
)

const defaultConfigYAML = `# yoloai configuration
# See https://github.com/kstenerud/yoloai for documentation
# Run 'yoloai config set <key> <value>' to change settings.
#
# Available settings:
#   defaults.agent        Agent to use: claude, codex, gemini
#   defaults.backend      Runtime backend: docker, tart, seatbelt
#   defaults.tart_image   Custom base VM image (tart backend only)

setup_complete: false
`

// Manager is the central orchestrator for sandbox operations.
type Manager struct {
	runtime runtime.Runtime
	backend string
	logger  *slog.Logger
	input   io.Reader
	output  io.Writer
}

// NewManager creates a Manager with the given runtime, backend name, logger,
// input reader for interactive prompts, and output writer for user-facing messages.
func NewManager(rt runtime.Runtime, backend string, logger *slog.Logger, input io.Reader, output io.Writer) *Manager {
	return &Manager{
		runtime: rt,
		backend: backend,
		logger:  logger,
		input:   input,
		output:  output,
	}
}

// EnsureSetup performs first-run auto-setup. Idempotent — safe to call
// before every sandbox operation.
func (m *Manager) EnsureSetup(ctx context.Context) error {
	if err := m.EnsureSetupNonInteractive(ctx); err != nil {
		return err
	}

	// Run new-user experience if setup_complete is false
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.SetupComplete {
		if !m.isInteractive() {
			// Non-TTY: auto-configure without prompts (power-user behavior)
			if err := m.setTmuxConf("default+host"); err != nil {
				return fmt.Errorf("set tmux_conf: %w", err)
			}
			if err := UpdateConfigFields(map[string]string{"setup_complete": "true"}); err != nil {
				return fmt.Errorf("set setup_complete: %w", err)
			}
		} else {
			if err := m.runNewUserSetup(ctx); err != nil {
				if !errors.Is(err, errSetupPreview) {
					return err
				}
			}
		}
	}

	return nil
}

// EnsureSetupNonInteractive performs the non-interactive portion of first-run
// setup: directory creation, resource seeding, image building, and default
// config writing. Does not run interactive prompts.
func (m *Manager) EnsureSetupNonInteractive(ctx context.Context) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}
	yoloaiDir := filepath.Join(homeDir, ".yoloai")

	// Create directory structure
	for _, sub := range []string{"sandboxes", "profiles", "cache"} {
		dir := filepath.Join(yoloaiDir, sub)
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	// Seed resources and build/rebuild base image as needed
	if err := m.runtime.EnsureImage(ctx, yoloaiDir, m.output, m.logger, false); err != nil {
		return err
	}

	// Write default config.yaml on first run
	configPath := filepath.Join(yoloaiDir, "config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		if err := os.WriteFile(configPath, []byte(defaultConfigYAML), 0600); err != nil {
			return fmt.Errorf("write config.yaml: %w", err)
		}
		fmt.Fprintln(m.output, "Tip: enable shell completions with 'yoloai completion --help'") //nolint:errcheck // best-effort output
	}

	return nil
}

// isInteractive returns true if m.input is a TTY (terminal).
func (m *Manager) isInteractive() bool {
	if f, ok := m.input.(*os.File); ok {
		return term.IsTerminal(int(f.Fd())) //nolint:gosec // file descriptor fits in int on all supported platforms
	}
	return false
}
