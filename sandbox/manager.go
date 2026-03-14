package sandbox

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
	"golang.org/x/term"
)

// Manager is the central orchestrator for sandbox operations.
type Manager struct {
	runtime  runtime.Runtime
	backend  string
	logger   *slog.Logger
	input    io.Reader
	scanner  *bufio.Scanner // shared scanner for multi-step interactive prompts
	output   io.Writer
	progress func(name, msg string) // optional progress callback
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithProgress sets a callback that receives human-readable progress messages
// during long operations. The callback receives the sandbox name and message.
func WithProgress(fn func(name, msg string)) ManagerOption {
	return func(m *Manager) { m.progress = fn }
}

// NewManager creates a Manager with the given runtime, logger, input reader
// for interactive prompts, and output writer for user-facing messages.
// The backend name is read from rt.Name() when rt is non-nil.
func NewManager(rt runtime.Runtime, logger *slog.Logger, input io.Reader, output io.Writer, opts ...ManagerOption) *Manager {
	backend := ""
	if rt != nil {
		backend = rt.Name()
	}
	m := &Manager{
		runtime: rt,
		backend: backend,
		logger:  logger,
		input:   input,
		scanner: bufio.NewScanner(input),
		output:  output,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// readLine reads a single line from the shared scanner, returning early if ctx
// is cancelled. On EOF, returns ("", nil) so callers can treat it as a default.
//
// This method uses the Manager's shared bufio.Scanner so that sequential reads
// in multi-step interactive prompts (e.g., setup wizard) consume successive
// lines correctly. For one-shot confirmations that create a fresh scanner on
// each call, see the standalone readLine() in confirm.go.
func (m *Manager) readLine(ctx context.Context) (string, error) {
	ch := make(chan string, 1)
	go func() {
		if m.scanner.Scan() {
			ch <- m.scanner.Text()
		} else {
			ch <- ""
		}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case line := <-ch:
		return line, nil
	}
}

// EnsureSetup performs first-run auto-setup. Idempotent — safe to call
// before every sandbox operation.
func (m *Manager) EnsureSetup(ctx context.Context) error {
	if err := m.EnsureSetupNonInteractive(ctx); err != nil {
		return err
	}

	// Run new-user experience if setup_complete is false
	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if !state.SetupComplete {
		if !m.isInteractive() {
			// Non-TTY: auto-configure without prompts (power-user behavior)
			if err := m.setTmuxConf("default+host"); err != nil {
				return fmt.Errorf("set tmux_conf: %w", err)
			}
			if err := config.SaveState(&config.State{SetupComplete: true}); err != nil {
				return fmt.Errorf("save state: %w", err)
			}
		} else {
			if err := m.runNewUserSetup(ctx, SetupOptions{}); err != nil {
				if !errors.Is(err, errSetupPreview) {
					return err
				}
			}
		}
	}

	return nil
}

// EnsureSetupNonInteractive performs the non-interactive portion of first-run
// setup: migration, directory creation, resource seeding, image building,
// and default config writing. Does not run interactive prompts.
func (m *Manager) EnsureSetupNonInteractive(ctx context.Context) error {
	// Create directory structure
	for _, dir := range []string{config.SandboxesDir(), config.ProfilesDir(), config.CacheDir()} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	baseProfileDir := config.ProfileDirPath("base")
	if err := os.MkdirAll(baseProfileDir, 0750); err != nil {
		return fmt.Errorf("create %s: %w", baseProfileDir, err)
	}

	// Seed resources and build/rebuild base image as needed
	if err := m.runtime.EnsureImage(ctx, baseProfileDir, m.output, m.logger, false); err != nil {
		return err
	}

	// Write default config.yaml on first run
	configPath := config.ConfigPath()
	if _, err := os.Stat(configPath); err != nil {
		if err := os.WriteFile(configPath, []byte(config.DefaultConfigYAML), 0600); err != nil {
			return fmt.Errorf("write config.yaml: %w", err)
		}
		fmt.Fprintln(m.output, "Tip: enable shell completions with 'yoloai system completion --help'") //nolint:errcheck // best-effort output
	}

	// Write default global config.yaml if missing
	globalConfigPath := config.GlobalConfigPath()
	if _, err := os.Stat(globalConfigPath); os.IsNotExist(err) {
		if err := os.WriteFile(globalConfigPath, []byte(config.DefaultGlobalConfigYAML), 0600); err != nil {
			return fmt.Errorf("write global config.yaml: %w", err)
		}
	}

	// Write default state.yaml if missing
	statePath := config.StatePath()
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		if err := config.SaveState(&config.State{}); err != nil {
			return fmt.Errorf("write state.yaml: %w", err)
		}
	}

	return nil
}

// List returns info for all sandboxes.
func (m *Manager) List(ctx context.Context) ([]*Info, error) {
	return ListSandboxes(ctx, m.runtime)
}

// Inspect returns combined metadata and live state for a single sandbox.
func (m *Manager) Inspect(ctx context.Context, name string) (*Info, error) {
	return InspectSandbox(ctx, m.runtime, name)
}

// Status returns the current lifecycle status of a sandbox.
func (m *Manager) Status(ctx context.Context, name string) (Status, error) {
	return DetectStatus(ctx, m.runtime, InstanceName(name), Dir(name))
}

// SandboxFiles returns the path to the per-sandbox file exchange directory.
func (m *Manager) SandboxFiles(name string) string {
	return FilesDir(name)
}

// SandboxCache returns the path to the per-sandbox cache directory.
func (m *Manager) SandboxCache(name string) string {
	return CacheDir(name)
}

// SendInput sends text to the sandbox agent's terminal via tmux send-keys.
// If the agent is running, this interrupts it mid-task. If the agent is idle
// at its prompt, this sends a follow-up message. The caller should check
// Manager.Status before calling to know which case applies.
func (m *Manager) SendInput(ctx context.Context, name string, text string) error {
	containerName := InstanceName(name)
	_, err := m.runtime.Exec(ctx, containerName,
		[]string{"tmux", "send-keys", "-t", "main", text, "Enter"},
		"yoloai",
	)
	if err != nil {
		return fmt.Errorf("send input to sandbox %q: %w", name, err)
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
