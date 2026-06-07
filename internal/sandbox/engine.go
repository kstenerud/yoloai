// ABOUTME: Engine type and NewEngine constructor: the central orchestrator that
// ABOUTME: holds a Runtime and coordinates all sandbox CRUD and lifecycle operations.
package sandbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	tmuxres "github.com/kstenerud/yoloai/internal/resources/tmux"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// Engine is the central orchestrator for sandbox operations.
//
// The Engine holds no output writer (F8): operations that emit human-readable
// progress take an explicit io.Writer per call (e.g. CreateOptions.Output,
// EnsureSetup's out param) and discrete advisories are returned as structured
// Notices. The yoloai.Client seeds those per-call writers from its ClientConfiguration.Output.
//
// The Engine owns the lazy backend connection (D74). It is built eagerly from
// layout-only state (NewEngine), with runtime nil; the first backend-bound
// method opens the runtime via ensure. A backend-less Engine (backend == "")
// still serves host-only reads; a backend-bound op on it returns
// ErrBackendRequired. Construction with an already-open runtime
// (NewEngineWithRuntime — tests and the ephemeral overwrite path) latches
// opened so ensure is a no-op.
type Engine struct {
	backend  runtime.BackendType
	logger   *slog.Logger
	input    io.Reader
	progress func(name, msg string) // optional progress callback
	layout   config.Layout          // DataDir-rooted path resolver (Q-W.2)

	// Lazy backend connection. runtime is opened once, on the first
	// backend-bound operation, via ensure/TryEnsure — host-only reads never
	// trigger it. Guarded by mutex; opened latches true on success and runtime
	// is then stable for the Engine's lifetime.
	mutex   sync.Mutex
	opened  bool
	runtime runtime.Runtime
}

// ErrBackendRequired is returned by backend-bound Engine operations when the
// Engine was constructed without a backend (backend == ""). A backend-less
// Engine still serves host-only reads. The yoloai root package re-exports this
// sentinel as yoloai.ErrBackendRequired.
var ErrBackendRequired = yoerrors.NewUsageError("yoloai: this operation requires a backend, but the Client was constructed without ClientCreateOptions.BackendType (backend-less). Set ClientCreateOptions.BackendType (e.g. via yoloai.SelectBackend) to enable backend-bound operations. See development-principles.md §4.")

// EngineOption configures an Engine.
type EngineOption func(*Engine)

// WithProgress sets a callback that receives human-readable progress messages
// during long operations. The callback receives the sandbox name and message.
func WithProgress(fn func(name, msg string)) EngineOption {
	return func(e *Engine) { e.progress = fn }
}

// WithLayout sets the path-resolution Layout. REQUIRED at
// construction — Q-W.5 removed the implicit $HOME/.yoloai/ fallback.
// Embedders construct a Layout from their data directory of choice;
// the CLI constructs it once from --data-dir or $HOME/.yoloai/ at
// startup. See development-principles.md §12.
func WithLayout(layout config.Layout) EngineOption {
	return func(e *Engine) { e.layout = layout }
}

// NewEngine creates a lazy Engine bound to a backend, opening no connection
// (D74). The runtime is opened on the first backend-bound method via ensure;
// backend == "" yields a backend-less Engine whose backend-bound ops return
// ErrBackendRequired while its host-only reads still work. This is the primary
// constructor used by yoloai.Client.
//
// The Engine holds no output writer (F8): per-call progress writers are passed
// explicitly (CreateOptions.Output, EnsureSetup's out param) and discrete
// advisories are returned as Notices.
//
// A WithLayout option is REQUIRED — Q-W.5 removed the implicit
// $HOME/.yoloai/ fallback so library code never reads ambient HOME.
// Callers that omit WithLayout get an Engine whose Layout.DataDir is
// "", which produces relative paths at every store helper call
// site (failures surface quickly). The yoloai.Client adapter and
// the CLI command handlers always pass WithLayout; only direct
// test construction needs to remember it (use config.NewLayout
// with t.TempDir-based DataDir).
func NewEngine(backend runtime.BackendType, logger *slog.Logger, input io.Reader, opts ...EngineOption) *Engine {
	return newEngine(backend, nil, false, logger, input, opts...)
}

// NewEngineWithRuntime creates an Engine with an already-open runtime injected,
// latching opened so ensure is a no-op (the runtime never re-opens lazily). The
// backend name is read from rt.Descriptor().Type when rt is non-nil. Used by
// tests (mock runtimes) and the ephemeral cross-backend overwrite path; rt may
// be nil for a disk-only Engine whose backend-bound methods are never called.
func NewEngineWithRuntime(rt runtime.Runtime, logger *slog.Logger, input io.Reader, opts ...EngineOption) *Engine {
	var backend runtime.BackendType
	if rt != nil {
		backend = rt.Descriptor().Type
	}
	return newEngine(backend, rt, true, logger, input, opts...)
}

func newEngine(backend runtime.BackendType, rt runtime.Runtime, opened bool, logger *slog.Logger, input io.Reader, opts ...EngineOption) *Engine {
	e := &Engine{
		backend: backend,
		runtime: rt,
		opened:  opened,
		logger:  logger,
		input:   input,
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.layout.DataDir == "" {
		// Q-W.5 / §12 invariant: every Engine method that touches disk
		// derives its path from e.layout. A zero-value Layout silently
		// produces relative paths under CWD, which test runs were
		// leaking into the repo. Panic here so missing WithLayout is
		// caught at construction instead of corrupting the working
		// directory.
		//
		// F14 / config.NewLayout panics on empty input, so the only
		// way to reach this branch is a caller that never invoked
		// WithLayout (e.layout is the zero value).
		panic("sandbox.NewEngine: WithLayout is required; pass sandbox.WithLayout(config.NewLayout(...))")
	}
	return e
}

// ensure lazily opens the backend connection on first use, caching it for the
// Engine's lifetime. It is the gate for every backend-bound operation. Returns
// ErrBackendRequired for a backend-less Engine; a failed open is NOT cached (the
// next call retries). Safe for concurrent use.
func (e *Engine) ensure(ctx context.Context) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if e.opened {
		return nil
	}
	if e.backend == "" {
		return ErrBackendRequired
	}
	rt, err := runtime.New(ctx, e.backend, e.layout)
	if err != nil {
		return fmt.Errorf("connect to %s backend: %w", e.backend, err)
	}
	e.runtime = rt
	e.opened = true
	return nil
}

// TryEnsure opens the backend best-effort for operations that have a host-only
// fallback (Workdir host-git, on-disk allowlist live-patch, ContainerLogs): on
// success runtime is populated; on failure (including a backend-less Engine) it
// stays nil and the caller falls back to its disk-only path. The error is
// intentionally discarded.
func (e *Engine) TryEnsure(ctx context.Context) {
	_ = e.ensure(ctx) //nolint:errcheck // best-effort: callers fall back to a host-only path when runtime stays nil
}

// Close releases the underlying runtime connection, if one was ever opened.
// A no-op on an Engine whose backend was never used.
func (e *Engine) Close() error {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if !e.opened || e.runtime == nil {
		return nil
	}
	return e.runtime.Close()
}

// deps bundles the Engine's runtime, layout, and input into state.Deps for the
// lifecycle and create free functions. Callers reach it only after ensure has
// opened the runtime (or via TryEnsure for the host-only-fallback verbs, where a
// nil runtime is acceptable).
func (e *Engine) deps() state.Deps {
	return state.Deps{Runtime: e.runtime, Layout: e.layout, Input: e.input}
}

// Layout returns the Engine's path-resolution Layout. Read-only —
// callers that need a different layout construct a new Engine with
// WithLayout.
func (e *Engine) Layout() config.Layout { return e.layout }

// EnsureSetup performs first-run auto-setup. Idempotent — safe to call
// before every sandbox operation. Non-interactive: scaffolds the data
// dir, materializes declarative safe defaults, runs the library schema
// migration, and builds/refreshes the base image. Requires a non-nil
// runtime (the image build calls rt.Setup).
//
// The library just-works from declarative config-layer defaults; it
// keeps no setup-ceremony state. Any interactive wizard or first-run
// UX lives in the app layer (the CLI's `yoloai system setup`), which
// records its own "wizard has run" bookkeeping — none of the library's
// business.
func (e *Engine) EnsureSetup(ctx context.Context, out io.Writer) error {
	if err := e.ensure(ctx); err != nil {
		return err
	}
	if out == nil {
		out = io.Discard
	}
	if err := e.ensureLayoutScaffold(); err != nil {
		return err
	}
	baseProfileDir := e.layout.ProfileDir("base")
	return e.runtime.Setup(ctx, e.layout, baseProfileDir, out, e.logger, false)
}

// ensureDefaultsDir creates DataDir/defaults/ and materializes the
// declarative default artifacts (defaults/config.yaml, defaults/tmux.conf)
// when missing. Method on Engine so it can use e.layout's DefaultsDir() /
// DefaultsConfigPath() — Q-W requires path resolution through Layout,
// never via ambient $HOME.
func (e *Engine) ensureDefaultsDir() error {
	defaultsDir := e.layout.DefaultsDir()
	if err := fileutil.MkdirAll(defaultsDir, 0750); err != nil {
		return fmt.Errorf("create defaults dir: %w", err)
	}
	configPath := e.layout.DefaultsConfigPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		scaffold := config.GenerateScaffoldConfig(config.DefaultConfigYAML)
		if err := fileutil.WriteFile(configPath, []byte(scaffold), 0600); err != nil {
			return fmt.Errorf("write defaults/config.yaml: %w", err)
		}
	}
	// Materialize the reference tmux.conf declaratively. The in-sandbox tmux
	// mount binds this under the tmux_conf=default/default+host default; users
	// may inspect/customize it. 0644 so uid 1001 inside Kata VMs can read it.
	tmuxConfPath := filepath.Join(defaultsDir, "tmux.conf")
	if _, err := os.Stat(tmuxConfPath); os.IsNotExist(err) {
		if err := fileutil.WriteFile(tmuxConfPath, tmuxres.Embedded(), 0644); err != nil { //nolint:gosec // G306: tmux.conf contains no secrets; 0644 required for uid 1001 in Kata VMs
			return fmt.Errorf("write defaults/tmux.conf: %w", err)
		}
	}
	return nil
}

// ensureLayoutScaffold creates the DataDir directory structure and writes the
// default global config.yaml and declarative defaults/ when missing. Pure
// filesystem work — no runtime required. Used by EnsureSetup (which adds the
// image build on top).
//
// It does NOT migrate or stamp the schema version: bringing the DataDir to the
// current on-disk version is the startup gate's (fresh-create) or the explicit
// migrate command's job, never a silent side effect of setup.
func (e *Engine) ensureLayoutScaffold() error {
	for _, dir := range []string{e.layout.SandboxesDir(), e.layout.ProfilesDir(), e.layout.CacheDir()} {
		if err := fileutil.MkdirAll(dir, 0750); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	if err := e.ensureDefaultsDir(); err != nil {
		return err
	}
	globalConfigPath := e.layout.GlobalConfigPath()
	if _, err := os.Stat(globalConfigPath); os.IsNotExist(err) {
		if err := fileutil.WriteFile(globalConfigPath, []byte(config.DefaultGlobalConfigYAML), 0600); err != nil {
			return fmt.Errorf("write global config.yaml: %w", err)
		}
	}
	return nil
}

// List returns info for all sandboxes.
func (e *Engine) List(ctx context.Context) ([]*Info, error) {
	if err := e.ensure(ctx); err != nil {
		return nil, err
	}
	return ListSandboxes(ctx, e.layout, e.runtime)
}

// Inspect returns combined metadata and live state for a single sandbox.
func (e *Engine) Inspect(ctx context.Context, name string) (*Info, error) {
	if err := e.ensure(ctx); err != nil {
		return nil, err
	}
	return InspectSandbox(ctx, e.layout, e.runtime, name)
}

// Runtime returns the active runtime backend. Exposed so callers (e.g. the MCP
// proxy) can type-assert against optional interfaces like runtime.StdioExecer
// without going behind Engine's back via shell invocations.
func (e *Engine) Runtime() runtime.Runtime { return e.runtime }

// Status returns the current lifecycle status of a sandbox.
func (e *Engine) Status(ctx context.Context, name string) (Status, error) {
	if err := e.ensure(ctx); err != nil {
		return "", err
	}
	return DetectStatus(ctx, e.runtime, store.InstanceName(e.layout.Principal, name), e.layout.SandboxDir(name))
}

// SandboxFiles returns the path to the per-sandbox file exchange directory.
func (e *Engine) SandboxFiles(name string) string {
	return store.FilesDir(e.layout.SandboxDir(name))
}

// SandboxCache returns the path to the per-sandbox cache directory.
func (e *Engine) SandboxCache(name string) string {
	return store.CacheDir(e.layout.SandboxDir(name))
}

// SendInput sends text to the sandbox agent's terminal via tmux send-keys.
// If the agent is running, this interrupts it mid-task. If the agent is idle
// at its prompt, this sends a follow-up message. The caller should check
// Engine.Status before calling to know which case applies.
//
// Acquires the per-sandbox lock (Q-T): SendInput mutates sandbox state
// (injects keystrokes into the running agent's tmux session), so it
// serialises against concurrent Stop / Destroy / Reset / Apply for the
// same sandbox. Each call is brief (one exec), so the lock-hold time
// is small even under interactive use.
func (e *Engine) SendInput(ctx context.Context, name string, text string) error {
	if err := e.ensure(ctx); err != nil {
		return err
	}
	unlock, err := store.AcquireLock(e.layout, name)
	if err != nil {
		return err
	}
	defer unlock()

	containerName := store.InstanceName(e.layout.Principal, name)
	_, err = e.runtime.Exec(ctx, containerName,
		[]string{"tmux", "send-keys", "-t", "main", text, "Enter"},
		"yoloai",
	)
	if err != nil {
		return fmt.Errorf("send input to sandbox %q: %w", name, err)
	}
	return nil
}
