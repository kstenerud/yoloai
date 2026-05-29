// ABOUTME: Manager type and NewManager constructor: the central orchestrator that
// ABOUTME: holds a Runtime and coordinates all sandbox CRUD and lifecycle operations.
package sandbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// Manager is the central orchestrator for sandbox operations.
//
// The Manager holds no output writer (F8): operations that emit human-readable
// progress take an explicit io.Writer per call (e.g. CreateOptions.Output,
// EnsureSetup's out param) and discrete advisories are returned as structured
// Notices. The yoloai.Client seeds those per-call writers from its Options.Output.
type Manager struct {
	runtime  runtime.Runtime
	backend  runtime.BackendName
	logger   *slog.Logger
	input    io.Reader
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

// NewManager creates a Manager with the given runtime, logger, and input reader
// for interactive prompts. The backend name is read from rt.Descriptor().Name
// when rt is non-nil.
//
// The Manager holds no output writer (F8): per-call progress writers are passed
// explicitly (CreateOptions.Output, EnsureSetup's out param) and discrete
// advisories are returned as Notices.
//
// A WithLayout option is REQUIRED — Q-W.5 removed the implicit
// $HOME/.yoloai/ fallback so library code never reads ambient HOME.
// Callers that omit WithLayout get a Manager whose Layout.DataDir is
// "", which produces relative paths at every store helper call
// site (failures surface quickly). The yoloai.Client adapter and
// the CLI command handlers always pass WithLayout; only direct
// test construction needs to remember it (use config.NewLayout
// with t.TempDir-based DataDir).
func NewManager(rt runtime.Runtime, logger *slog.Logger, input io.Reader, opts ...ManagerOption) *Manager {
	var backend runtime.BackendName
	if rt != nil {
		backend = rt.Descriptor().Name
	}
	m := &Manager{
		runtime: rt,
		backend: backend,
		logger:  logger,
		input:   input,
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.layout.DataDir == "" {
		// Q-W.5 / §12 invariant: every Manager method that touches disk
		// derives its path from m.layout. A zero-value Layout silently
		// produces relative paths under CWD, which test runs were
		// leaking into the repo. Panic here so missing WithLayout is
		// caught at construction instead of corrupting the working
		// directory.
		//
		// F14 / config.NewLayout panics on empty input, so the only
		// way to reach this branch is a caller that never invoked
		// WithLayout (m.layout is the zero value).
		panic("sandbox.NewManager: WithLayout is required; pass sandbox.WithLayout(config.NewLayout(...))")
	}
	return m
}

// Layout returns the Manager's path-resolution Layout. Read-only —
// callers that need a different layout construct a new Manager with
// WithLayout.
func (m *Manager) Layout() config.Layout { return m.layout }

// EnsureSetup performs first-run auto-setup. Idempotent — safe to call
// before every sandbox operation. Non-interactive: writes safe defaults
// (tmux_conf=default+host, setup_complete=true) and returns. The
// interactive wizard for customizing tmux/backend/agent lives in the
// CLI layer and is invoked explicitly via `yoloai system setup` (which
// calls Manager.ApplySetup with the user's answers).
//
// Pre-Q-F, this method ran the wizard when stdin was a TTY. That
// coupled prompts to library code and contradicted §12's "no ambient
// IO" principle. Q-F moves the wizard to the CLI; EnsureSetup now
// behaves as the old non-interactive branch always behaved.
func (m *Manager) EnsureSetup(ctx context.Context, out io.Writer) error {
	if err := m.EnsureSetupNonInteractive(ctx, out); err != nil {
		return err
	}
	state, err := config.LoadState(m.layout)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	if state.SetupComplete {
		return nil
	}
	if err := m.setTmuxConf("default+host"); err != nil {
		return fmt.Errorf("set tmux_conf: %w", err)
	}
	if err := config.SaveState(m.layout, &config.State{SetupComplete: true}); err != nil {
		return fmt.Errorf("save state: %w", err)
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

// EnsureSetupNonInteractive performs the non-interactive portion of
// first-run setup: layout scaffolding, default config writing, AND
// base-image build. Requires a non-nil runtime (the image build calls
// rt.Setup). Called by EnsureSetup, which runs before every sandbox
// operation.
//
// For pure-config setup that does NOT need a runtime (e.g. the
// `yoloai system setup` wizard's ApplySetup path), use
// ensureLayoutScaffold instead.
func (m *Manager) EnsureSetupNonInteractive(ctx context.Context, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	if err := m.ensureLayoutScaffold(); err != nil {
		return err
	}
	state, err := config.LoadState(m.layout)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	// Seed resources and build/rebuild base image as needed
	baseProfileDir := m.layout.ProfileDir("base")
	if err := m.runtime.Setup(ctx, m.layout, baseProfileDir, out, m.logger, false); err != nil {
		return err
	}
	if !state.SetupComplete {
		fmt.Fprintln(out, "Tip: enable shell completions with 'yoloai system completion --help'") //nolint:errcheck // best-effort output
	}
	return nil
}

// ensureLayoutScaffold creates the DataDir directory structure and
// writes default global config.yaml / state.yaml / defaults/ if
// missing. Pure filesystem work — no runtime required. Shared between
// EnsureSetupNonInteractive (which adds image build on top) and
// ApplySetup (config-write only).
func (m *Manager) ensureLayoutScaffold() error {
	for _, dir := range []string{m.layout.SandboxesDir(), m.layout.ProfilesDir(), m.layout.CacheDir()} {
		if err := fileutil.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	state, err := config.LoadState(m.layout)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	// Upgrading user: defaults/ should exist. If it doesn't, surface
	// the migration error before doing anything else.
	if state.SetupComplete {
		if err := config.CheckDefaultsDir(m.layout); err != nil {
			return err
		}
	}
	if err := m.ensureDefaultsDir(); err != nil {
		return err
	}
	globalConfigPath := m.layout.GlobalConfigPath()
	if _, err := os.Stat(globalConfigPath); os.IsNotExist(err) {
		if err := fileutil.WriteFile(globalConfigPath, []byte(config.DefaultGlobalConfigYAML), 0600); err != nil {
			return fmt.Errorf("write global config.yaml: %w", err)
		}
	}
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
	unlock, err := store.AcquireLock(m.layout, name)
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
