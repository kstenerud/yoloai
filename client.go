// ABOUTME: Public high-level Client API (Run, List, Clone, Create, plus the
// ABOUTME: Sandbox/System sub-handles) for embedding yoloAI in Go programs.
// Package yoloai is the orchestration layer for yoloAI. Both the CLI
// (internal/cli) and external embedders use it as the entry point for
// running AI coding agents in isolated sandboxes.
//
// One Client is the entry point (A2/A3). It owns creation and cross-sandbox
// operations (Run, Create, Clone, List) plus the per-sandbox handle accessor
// Sandbox(name); per-sandbox operations (Inspect, Start, Stop, Restart, Reset,
// Destroy, Exec, and the Workdir/Network/Agent sub-handles) live on that
// *Sandbox handle, not the Client root (F2). The backend connection is opened
// lazily on the first backend-bound operation, so ClientCreateOptions.BackendType
// is optional: a backend-less Client still serves host-only reads (Sandbox.Metadata,
// Workdir diffs, the on-disk allowlist) and, via Client.System(), cross-backend
// admin — a backend-bound op on such a Client returns ErrBackendRequired.
//
//   - System — the admin/cross-backend sub-handle (DiskUsage, Prune, Build,
//     Check, …), reached only via Client.System(). Decoupled from a single
//     backend: it iterates the registered backends internally.
//
// Following the W-L8 layering refactor, the CLI is a thin shell over
// Client + System; orchestration logic lives here, not in
// internal/cli. New CLI commands should call Client/System
// methods rather than reaching into sandbox/* or runtime/* directly.
//
// Typical usage:
//
//	client, err := yoloai.NewClient(ctx, yoloai.ClientCreateOptions{
//	    DataDir:     filepath.Join(os.Getenv("HOME"), ".yoloai", "library"),
//	    HomeDir:     os.Getenv("HOME"),     // required; where ~/.claude etc. resolve
//	    BackendType: yoloai.BackendDocker,  // optional; backend-bound ops open it lazily
//	})
//	if err != nil { log.Fatal(err) }
//	defer client.Close()
//
//	info, err := client.Run(ctx, yoloai.SandboxRunOptions{
//	    Name:    "myproject",
//	    WorkDir: "/path/to/project",
//	    Prompt:  "Fix the login bug",
//	    Wait:    true,
//	})
//	if err != nil { log.Fatal(err) }
//	if info.Status == yoloai.StatusDone {
//	    sb, err := client.Sandbox(info.Environment.Name)
//	    if err != nil { log.Fatal(err) }
//	    sb.Workdir().Apply(ctx, yoloai.WorkdirApplyOptions{Mode: yoloai.ApplyModeCommits})
//	}
package yoloai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	_ "github.com/kstenerud/yoloai/internal/runtime/docker"   // register backend
	_ "github.com/kstenerud/yoloai/internal/runtime/podman"   // register backend
	_ "github.com/kstenerud/yoloai/internal/runtime/seatbelt" // register backend
	_ "github.com/kstenerud/yoloai/internal/runtime/tart"     // register backend
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/create"
	"github.com/kstenerud/yoloai/internal/sandbox/lifecycle"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// Sentinel errors returned by Client methods. Re-exported from
// internal/sandbox so embedders can `errors.Is` against them without
// reaching into internal packages.
var (
	// ErrSandboxExists is returned by Run when a sandbox with the given name
	// already exists and Replace is false.
	ErrSandboxExists = sandbox.ErrSandboxExists

	// ErrSandboxNotFound is returned by methods that operate on a named
	// sandbox when no sandbox with that name exists on disk.
	ErrSandboxNotFound = sandbox.ErrSandboxNotFound

	// ErrContainerNotRunning is returned by methods that require a live
	// container (Exec, Attach, CaptureTerminal, SendInput, …) when the
	// sandbox exists but its container is stopped or has not been
	// recreated since the host last booted.
	ErrContainerNotRunning = sandbox.ErrContainerNotRunning

	// ErrMissingAPIKey is returned by Run/Create when the selected agent
	// requires an API key (via Definition.APIKeyEnvVars) but none is set.
	ErrMissingAPIKey = sandbox.ErrMissingAPIKey
)

// Client is the simple entry point for yoloAI operations.
// A Client is safe for concurrent use by multiple goroutines.
// Construct with NewClient.
type Client struct {
	layout  config.Layout       // Q-W: DataDir-rooted path resolver propagated to Engine + apply
	backend runtime.BackendType // selected backend; "" = backend-less (host-only reads/admin), backend-bound ops return ErrBackendRequired
	logger  *slog.Logger        // for the lazily-built Engine
	version string              // yoloAI version stamped into created sandboxes' environment.json
	output  io.Writer           // ClientCreateOptions.Output (defaulted to io.Discard); seeds per-call progress writers (F8)
	input   io.Reader           // ClientCreateOptions.Input (defaulted to an empty reader, never os.Stdin — §12); threaded to create.Run via state.Deps

	// Lazy backend connection. The runtime is opened once, on the first
	// backend-bound operation, via ensure/tryEnsure — host-only reads
	// (Workdir host-git, on-disk allowlist, filesystem readers) never trigger
	// it. Guarded by mu; opened latches true on success and rt/manager are then
	// stable for the Client's lifetime.
	mu      sync.Mutex
	opened  bool
	rt      runtime.Runtime
	manager *sandbox.Engine
}

// NewClient creates a Client with explicit options.
// ClientCreateOptions.DataDir is REQUIRED (Q-W.5); empty is rejected.
func NewClient(ctx context.Context, opts ClientCreateOptions) (*Client, error) {
	if opts.DataDir == "" {
		return nil, fmt.Errorf("yoloai: ClientCreateOptions.DataDir is required (no implicit $HOME fallback; see development-principles.md §12)")
	}
	if opts.HomeDir == "" {
		return nil, yoerrors.NewUsageError("yoloai: ClientCreateOptions.HomeDir is required (no implicit filepath.Dir(DataDir) derivation; under the D60 bifurcation DataDir is $HOME/.yoloai/library, so its parent is not $HOME). Pass the host user's home explicitly; the CLI uses cliutil.Layout().HomeDir. See development-principles.md §12.")
	}
	principal, err := config.ParsePrincipalSegment(opts.Principal)
	if err != nil {
		return nil, yoerrors.NewUsageError("yoloai: invalid ClientCreateOptions.Principal: %v", err)
	}

	layout := config.NewLayoutFor(opts.DataDir, opts.HomeDir).WithPrincipal(principal)
	layout.Env = opts.Env
	layout.SecretsStagingDir = opts.SecretsStagingDir

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	output := opts.Output
	if output == nil {
		output = io.Discard
	}
	input := opts.Input
	if input == nil {
		input = bytes.NewReader(nil) // §12: empty reader, never the process's os.Stdin; embedders override, the CLI passes IOStreams
	}

	// The backend connection is NOT opened here (A2/A3).
	// ClientCreateOptions.BackendType is optional: a backend-less Client serves
	// host-only reads and admin without ever connecting; backend-bound ops open
	// the runtime lazily on first use (ensure) or return ErrBackendRequired when
	// BackendType is "".
	return &Client{
		layout:  layout,
		backend: opts.BackendType,
		logger:  logger,
		version: opts.Version,
		output:  output,
		input:   input,
	}, nil
}

// ErrBackendRequired is returned by backend-bound operations (Exec, Attach,
// Start, Stop, lifecycle, Create, List, Clone, …) when the Client was
// constructed without ClientCreateOptions.BackendType. A backend-less Client
// still serves host-only reads (Workdir host-git, on-disk allowlist, filesystem
// readers) and, via System(), cross-backend admin. Set
// ClientCreateOptions.BackendType — resolve it at the boundary with
// yoloai.SelectBackend — to enable backend-bound ops.
var ErrBackendRequired = yoerrors.NewUsageError("yoloai: this operation requires a backend, but the Client was constructed without ClientCreateOptions.BackendType (backend-less). Set ClientCreateOptions.BackendType (e.g. via yoloai.SelectBackend) to enable backend-bound operations. See development-principles.md §4.")

// ensure lazily opens the backend connection and builds the Engine on first
// use, caching both for the Client's lifetime. It is the gate for every
// backend-bound operation. Returns ErrBackendRequired for a backend-less
// Client; a failed open is NOT cached (the next call retries). Safe for
// concurrent use.
func (c *Client) ensure(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.opened {
		return nil
	}
	if c.backend == "" {
		return ErrBackendRequired
	}
	rt, err := newRuntime(ctx, c.backend, c.layout)
	if err != nil {
		return fmt.Errorf("connect to %s backend: %w", c.backend, err)
	}
	c.rt = rt
	c.manager = sandbox.NewEngine(rt, c.logger, c.input, sandbox.WithLayout(c.layout))
	c.opened = true
	return nil
}

// tryEnsure opens the backend connection best-effort for operations that have a
// host-only fallback (Workdir host-git, on-disk allowlist live-patch,
// ContainerLogs, HasActiveWork): on success rt/manager are populated; on
// failure (including a backend-less Client) they stay nil and the caller falls
// back to its disk-only path. The error is intentionally discarded.
func (c *Client) tryEnsure(ctx context.Context) {
	_ = c.ensure(ctx) //nolint:errcheck // best-effort: callers fall back to a host-only path when rt stays nil
}

// Close releases the underlying runtime connection, if one was ever opened.
// A no-op on a Client whose backend was never used.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.opened {
		return nil
	}
	return c.rt.Close()
}

// Sandbox returns a sandbox-scoped handle, validating that the sandbox
// exists. A missing name is rejected with ErrSandboxNotFound here — at
// the point the caller typed the name — rather than lazily deep inside a
// later operation (F22 / §4 parse-don't-validate; the Q-G design rejected
// the GCS-style lazy handle since validation is local, not a network
// round-trip). Existence is a sandbox-directory check; a corrupt environment.json
// surfaces from the individual operation that reads it.
func (c *Client) Sandbox(name string) (*Sandbox, error) {
	if err := store.RequireSandboxDir(c.layout.SandboxDir(name)); err != nil {
		return nil, err
	}
	return &Sandbox{c: c, name: name}, nil
}

// System returns the admin sub-handle for system-level operations.
// Always non-nil; never errors. See System for the surface.
func (c *Client) System() *System {
	return &System{layout: c.layout}
}

// deps bundles the Client's runtime, layout, and input into state.Deps for
// use with lifecycle and create free functions. Callers must ensure the
// runtime is open (via ensure) before calling deps for a backend-bound op.
func (c *Client) deps() state.Deps {
	return state.Deps{Runtime: c.rt, Layout: c.layout, Input: c.input}
}

// pollUntilDone polls the sandbox status until it reaches a terminal state.
func (c *Client) pollUntilDone(ctx context.Context, name string, progress func(string, string)) (*SandboxInfo, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}

		info, err := c.manager.Inspect(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("inspect sandbox: %w", err)
		}
		switch info.Status {
		case sandbox.StatusDone, sandbox.StatusFailed, sandbox.StatusStopped, sandbox.StatusRemoved:
			return sandboxInfoFromStatus(info), nil
		case sandbox.StatusActive, sandbox.StatusIdle:
			// still running — continue polling
		default: // StatusBroken, StatusUnavailable
			return sandboxInfoFromStatus(info), nil
		}
		if progress != nil {
			progress(name, string(info.Status))
		}
	}
}

// Run creates a sandbox with the given options and starts the agent.
// Equivalent to 'yoloai new <name> <workdir> --prompt <prompt>'.
//
// If opts.Wait is true, Run blocks until the agent finishes and returns the
// final sandbox SandboxInfo. If opts.Wait is false, Run returns immediately after
// the agent is launched; the SandboxInfo reflects the initial state.
func (c *Client) Run(ctx context.Context, opts SandboxRunOptions) (*SandboxInfo, error) {
	createOpts := opts.materialize()
	if createOpts.AgentType == "" {
		createOpts.AgentType = AgentType(resolveAgentFromConfig(c.layout))
	}
	if createOpts.Model == "" {
		createOpts.Model = resolveModelFromConfig(c.layout)
	}

	if _, err := c.Create(ctx, createOpts); err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}

	if opts.OnProgress != nil {
		opts.OnProgress(opts.Name, "agent running")
	}

	if !opts.Wait {
		si, err := c.manager.Inspect(ctx, opts.Name)
		if err != nil {
			return nil, err
		}
		return sandboxInfoFromStatus(si), nil
	}

	return c.pollUntilDone(ctx, opts.Name, opts.OnProgress)
}

// List returns info for all sandboxes.
func (c *Client) List(ctx context.Context) ([]*SandboxInfo, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	sis, err := c.manager.List(ctx)
	if err != nil {
		return nil, err
	}
	return sandboxInfosFromStatus(sis), nil
}

// SandboxCloneOptions configures Client.Clone. Hand-written rather than aliased so the
// public surface doesn't expose internal/sandbox.SandboxCloneOptions. Overwrite (not
// "Force") is the concern-specific name per the Q-J field audit — "Force" stays
// a CLI flag only.
type SandboxCloneOptions struct {
	Source    string // existing sandbox name to copy from; required
	Dest      string // new sandbox name; required
	Overwrite bool   // destroy Dest first if it already exists
}

func (o SandboxCloneOptions) toInternal() sandbox.CloneOptions {
	return sandbox.CloneOptions{Source: o.Source, Dest: o.Dest}
}

// Clone copies an existing sandbox's state into a new sandbox. Although the
// copy itself is a disk-only deep-copy of the source sandbox dir under
// DataDir/sandboxes/, Clone is backend-bound: it goes through the Engine (and,
// under opts.Overwrite, tears the destination down through the runtime), so a
// backend-less Client returns ErrBackendRequired. This matches real clone
// workflows, which almost always start the destination right after. Embedders
// wanting a pure offline copy should copy the sandbox dir themselves.
//
// With opts.Overwrite set, an existing destination is destroyed before the
// copy; without it, an existing destination is a hard error.
func (c *Client) Clone(ctx context.Context, opts SandboxCloneOptions) error {
	if err := c.ensure(ctx); err != nil {
		return err
	}
	if opts.Overwrite {
		if err := c.destroyForOverwrite(ctx, opts.Dest); err != nil {
			return err
		}
	}
	return c.manager.Clone(ctx, opts.toInternal())
}

// destroyForOverwrite tears down a pre-existing destination sandbox so a clone
// can take its place. A missing destination is a no-op. The destination may
// have been created on a different backend than this Client's, so it destroys
// through the backend recorded in the destination's environment.json (falling
// back to the Client's own runtime when that metadata is unreadable). Work is
// abandoned unconditionally — an Overwrite clone is an explicit replace.
func (c *Client) destroyForOverwrite(ctx context.Context, dest string) error {
	dstDir := c.layout.SandboxDir(dest)
	if _, err := os.Stat(dstDir); os.IsNotExist(err) {
		return nil
	}

	deps := c.deps()
	if meta, err := store.LoadEnvironment(dstDir); err == nil && meta.BackendType != "" {
		rt, rtErr := newRuntime(ctx, meta.BackendType, c.layout)
		if rtErr != nil {
			return fmt.Errorf("connect to %s backend to overwrite %q: %w", meta.BackendType, dest, rtErr)
		}
		defer rt.Close() //nolint:errcheck // best-effort close after teardown
		deps = state.Deps{Runtime: rt, Layout: c.layout, Input: c.input}
	}

	if _, err := lifecycle.Destroy(ctx, deps, dest); err != nil {
		return fmt.Errorf("overwrite existing destination %q: %w", dest, err)
	}
	return nil
}

// Create provisions a new sandbox from SandboxCreateOptions and (unless
// opts.NoStart) starts the container with the agent. Returns the sandbox
// name on success — currently always opts.Name, since name is required
// (no auto-generation). Use Run for the higher-level "create + wait for
// terminal status" convenience.
func (c *Client) Create(ctx context.Context, opts SandboxCreateOptions) (string, error) {
	if err := c.ensure(ctx); err != nil {
		return "", err
	}
	internal := opts.toInternal()
	internal.Version = c.version
	if internal.Output == nil {
		internal.Output = c.output // seed the per-call progress writer from the Client's Output (F8)
	}
	if err := c.manager.EnsureSetup(ctx, c.output); err != nil {
		return "", err
	}
	return create.Run(ctx, c.deps(), internal)
}

// IOStreams names the stdio handles for interactive Client methods.
// It's a type alias for runtime.IOStreams so embedders can use the
// yoloai.IOStreams name without importing runtime directly. See
// runtime.IOStreams for the field documentation.
type IOStreams = runtime.IOStreams

// TermSize is a terminal-geometry update an embedder pushes through
// IOStreams.Resize to resize a running interactive exec. Alias for
// runtime.TermSize so embedders need not import runtime directly.
type TermSize = runtime.TermSize

// attachStatusOK returns nil if the sandbox status permits attach,
// otherwise a typed error suitable for the CLI exit-code mapping.
func attachStatusOK(status sandbox.Status, name string) error {
	switch status {
	case sandbox.StatusActive, sandbox.StatusIdle, sandbox.StatusDone, sandbox.StatusFailed:
		return nil
	default:
		// StatusStopped, StatusRemoved, StatusBroken, StatusUnavailable
		return fmt.Errorf("sandbox %q: %w", name, sandbox.ErrContainerNotRunning)
	}
}

// EnsureSetup performs idempotent first-run setup: scaffolds the DataDir,
// materializes safe declarative defaults (defaults/config.yaml,
// defaults/tmux.conf), builds the base image if needed, and stamps the
// library schema version. Safe to call before every sandbox operation —
// each step is a no-op once its artifact exists. Choosing non-default values
// (default backend/agent, tmux mode) is a separate concern handled by writing
// config via System.Config().Set — the library has no setup-wizard verb.
func (c *Client) EnsureSetup(ctx context.Context) error {
	if err := c.ensure(ctx); err != nil {
		return err
	}
	return c.manager.EnsureSetup(ctx, c.output)
}

// --- private helpers ---

// resolveBackendFromConfig picks a default backend for System admin
// operations that aren't bound to a specific backend. Reads the user's
// container_backend preference from config and routes it through
// runtime.SelectBackend; if that backend isn't available, SelectBackend falls
// back to any other registered container backend (the warning is discarded —
// admin callers don't surface it).
func resolveBackendFromConfig(ctx context.Context, layout config.Layout) runtime.BackendType {
	var preferred runtime.BackendType
	if cfg, err := config.LoadDefaultsConfig(layout); err == nil {
		preferred = runtime.BackendType(cfg.ContainerBackend)
	}
	backend, _ := runtime.SelectBackend(ctx, preferred, "", "", layout.Env)
	return backend
}

func resolveAgentFromConfig(layout config.Layout) string {
	cfg, err := config.LoadDefaultsConfig(layout)
	if err == nil && cfg.Agent != "" {
		return cfg.Agent
	}
	return "claude"
}

func resolveModelFromConfig(layout config.Layout) string {
	cfg, err := config.LoadDefaultsConfig(layout)
	if err == nil && cfg.Model != "" {
		return cfg.Model
	}
	return ""
}

func newRuntime(ctx context.Context, backend runtime.BackendType, layout config.Layout) (runtime.Runtime, error) {
	if backend == "" {
		backend = runtime.BackendDocker
	}
	return runtime.New(ctx, backend, layout)
}
