// ABOUTME: Manager type and NewManager constructor: the central orchestrator that
// ABOUTME: holds a Runtime and coordinates all sandbox CRUD and lifecycle operations.
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
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox/store"
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
	layout   config.Layout          // DataDir-rooted path resolver (Q-W.2)
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithProgress sets a callback that receives human-readable progress messages
// during long operations. The callback receives the sandbox name and message.
func WithProgress(fn func(name, msg string)) ManagerOption {
	return func(m *Manager) { m.progress = fn }
}

// WithLayout sets the path-resolution Layout. REQUIRED at
// construction — Q-W.5 removed the implicit $HOME/.yoloai/ fallback.
// Embedders construct a Layout from their data directory of choice;
// the CLI constructs it once from --data-dir or $HOME/.yoloai/ at
// startup. See development-principles.md §12.
func WithLayout(layout config.Layout) ManagerOption {
	return func(m *Manager) { m.layout = layout }
}

// NewManager creates a Manager with the given runtime, logger, input reader
// for interactive prompts, and output writer for user-facing messages.
// The backend name is read from rt.Descriptor().Name when rt is non-nil.
//
// A WithLayout option is REQUIRED — Q-W.5 removed the implicit
// $HOME/.yoloai/ fallback so library code never reads ambient HOME.
// Callers that omit WithLayout get a Manager whose Layout.DataDir is
// "", which produces relative paths at every store helper call
// site (failures surface quickly). The yoloai.Client adapter and
// the CLI command handlers always pass WithLayout; only direct
// test construction needs to remember it (use config.NewLayout
// with t.TempDir-based DataDir).
func NewManager(rt runtime.Runtime, logger *slog.Logger, input io.Reader, output io.Writer, opts ...ManagerOption) *Manager {
	backend := ""
	if rt != nil {
		backend = rt.Descriptor().Name
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

// Layout returns the Manager's path-resolution Layout. Read-only —
// callers that need a different layout construct a new Manager with
// WithLayout.
func (m *Manager) Layout() config.Layout { return m.layout }

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
	state, err := config.LoadState(m.layout)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if !state.SetupComplete {
		if !m.isInteractive() {
			// Non-TTY: auto-configure without prompts (power-user behavior)
			if err := m.setTmuxConf("default+host"); err != nil {
				return fmt.Errorf("set tmux_conf: %w", err)
			}
			if err := config.SaveState(m.layout, &config.State{SetupComplete: true}); err != nil {
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

// ensureDefaultsDir creates DataDir/defaults/ and writes defaults/config.yaml
// scaffold if it doesn't exist. Method on Manager so it can use m.layout's
// DefaultsDir() / DefaultsConfigPath() — Q-W requires path resolution
// through Layout, never via ambient $HOME.
func (m *Manager) ensureDefaultsDir() error {
	defaultsDir := m.layout.DefaultsDir()
	if err := fileutil.MkdirAll(defaultsDir, 0750); err != nil {
		return fmt.Errorf("create defaults dir: %w", err)
	}
	configPath := m.layout.DefaultsConfigPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		scaffold := config.GenerateScaffoldConfig(config.DefaultConfigYAML)
		if err := fileutil.WriteFile(configPath, []byte(scaffold), 0600); err != nil {
			return fmt.Errorf("write defaults/config.yaml: %w", err)
		}
	}
	return nil
}

// EnsureSetupNonInteractive performs the non-interactive portion of first-run
// setup: migration, directory creation, resource seeding, image building,
// and default config writing. Does not run interactive prompts.
func (m *Manager) EnsureSetupNonInteractive(ctx context.Context) error {
	// Create directory structure
	for _, dir := range []string{m.layout.SandboxesDir(), m.layout.ProfilesDir(), m.layout.CacheDir()} {
		if err := fileutil.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	// Upgrading user: defaults/ should exist. If it doesn't, they need to migrate.
	state, err := config.LoadState(m.layout)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if state.SetupComplete {
		if err := config.CheckDefaultsDir(m.layout); err != nil {
			return err
		}
	}

	// Fresh install (or after manual migration): create defaults/ scaffold.
	if err := m.ensureDefaultsDir(); err != nil {
		return err
	}

	// Seed resources and build/rebuild base image as needed
	baseProfileDir := m.layout.ProfileDir("base")
	if err := m.runtime.Setup(ctx, m.layout, baseProfileDir, m.output, m.logger, false); err != nil {
		return err
	}

	// Write defaults/config.yaml tip message on first run
	if !state.SetupComplete {
		fmt.Fprintln(m.output, "Tip: enable shell completions with 'yoloai system completion --help'") //nolint:errcheck // best-effort output
	}

	// Write default global config.yaml if missing
	globalConfigPath := m.layout.GlobalConfigPath()
	if _, err := os.Stat(globalConfigPath); os.IsNotExist(err) {
		if err := fileutil.WriteFile(globalConfigPath, []byte(config.DefaultGlobalConfigYAML), 0600); err != nil {
			return fmt.Errorf("write global config.yaml: %w", err)
		}
	}

	// Write default state.yaml if missing
	statePath := m.layout.StatePath()
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		if err := config.SaveState(m.layout, &config.State{}); err != nil {
			return fmt.Errorf("write state.yaml: %w", err)
		}
	}

	return nil
}

// List returns info for all sandboxes.
func (m *Manager) List(ctx context.Context) ([]*Info, error) {
	return ListSandboxes(ctx, m.layout, m.runtime)
}

// Inspect returns combined metadata and live state for a single sandbox.
func (m *Manager) Inspect(ctx context.Context, name string) (*Info, error) {
	return InspectSandbox(ctx, m.layout, m.runtime, name)
}

// Runtime returns the active runtime backend. Exposed so callers (e.g. the MCP
// proxy) can type-assert against optional interfaces like runtime.StdioExecer
// without going behind Manager's back via shell invocations.
func (m *Manager) Runtime() runtime.Runtime { return m.runtime }

// Status returns the current lifecycle status of a sandbox.
func (m *Manager) Status(ctx context.Context, name string) (Status, error) {
	return DetectStatus(ctx, m.runtime, store.InstanceName(name), m.layout.SandboxDir(name))
}

// SandboxFiles returns the path to the per-sandbox file exchange directory.
func (m *Manager) SandboxFiles(name string) string {
	return store.FilesDir(m.layout.SandboxDir(name))
}

// SandboxCache returns the path to the per-sandbox cache directory.
func (m *Manager) SandboxCache(name string) string {
	return store.CacheDir(m.layout.SandboxDir(name))
}

// SendInput sends text to the sandbox agent's terminal via tmux send-keys.
// If the agent is running, this interrupts it mid-task. If the agent is idle
// at its prompt, this sends a follow-up message. The caller should check
// Manager.Status before calling to know which case applies.
//
// Acquires the per-sandbox lock (Q-T): SendInput mutates sandbox state
// (injects keystrokes into the running agent's tmux session), so it
// serialises against concurrent Stop / Destroy / Reset / Apply for the
// same sandbox. Each call is brief (one exec), so the lock-hold time
// is small even under interactive use.
func (m *Manager) SendInput(ctx context.Context, name string, text string) error {
	unlock, err := AcquireLock(m.layout, name)
	if err != nil {
		return err
	}
	defer unlock()

	containerName := store.InstanceName(name)
	_, err = m.runtime.Exec(ctx, containerName,
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
