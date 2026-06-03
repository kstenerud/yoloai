// ABOUTME: Public high-level Client API (Run, Apply, Diff, Destroy) for embedding
// ABOUTME: yoloAI in Go programs without interacting with the CLI or sandbox package.
// Package yoloai is the orchestration layer for yoloAI. Both the CLI
// (internal/cli) and external embedders use it as the entry point for
// running AI coding agents in isolated sandboxes.
//
// One Client is the entry point (A2/A3). It owns creation and cross-sandbox
// operations (Run, Create, Clone, List) plus the per-sandbox handle accessor
// Sandbox(name); per-sandbox operations (Inspect, Start, Stop, Restart, Reset,
// Destroy, Exec, and the Workdir/Network/Agent sub-handles) live on that
// *Sandbox handle, not the Client root (F2). The backend connection is opened
// lazily on the first backend-bound operation, so Options.Backend is optional:
// a backend-less Client still serves host-only reads (Sandbox.Metadata,
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
//	client, err := yoloai.NewWithOptions(ctx, yoloai.Options{
//	    DataDir: filepath.Join(os.Getenv("HOME"), ".yoloai", "library"),
//	    HomeDir: os.Getenv("HOME"), // required; where ~/.claude etc. resolve
//	    Backend: yoloai.BackendDocker, // optional; backend-bound ops open it lazily
//	})
//	if err != nil { log.Fatal(err) }
//	defer client.Close()
//
//	info, err := client.Run(ctx, yoloai.RunOptions{
//	    Name:    "myproject",
//	    WorkDir: "/path/to/project",
//	    Prompt:  "Fix the login bug",
//	    Wait:    true,
//	})
//	if err != nil { log.Fatal(err) }
//	if info.Status == yoloai.StatusDone {
//	    sb, err := client.Sandbox(info.Environment.Name)
//	    if err != nil { log.Fatal(err) }
//	    sb.Workdir().Apply(ctx, yoloai.ApplyOptions{Mode: yoloai.ApplyModeCommits})
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

// Options configures a Client.
type Options struct {
	// DataDir is the root yoloai data directory; all per-Client state
	// lives below it (sandboxes/, profiles/, config.yaml, state.yaml,
	// credentials/). REQUIRED — empty is rejected at construction.
	//
	// No implicit default. yoloai library code never reads $HOME or any
	// other ambient process state. The CLI fills this from $HOME/.yoloai/
	// at startup (its single licensed os.UserHomeDir() call). HTTP
	// servers, daemons, multi-tenant processes, and tests pass an
	// explicit path. See development-principles.md §12.
	DataDir string

	// HomeDir is the host user's home directory. REQUIRED — empty is
	// rejected at construction with a *UsageError. It is where ~-expansion
	// in user-supplied paths, seed-file lookups (~/.claude, ~/.codex), and
	// auth-file discovery resolve.
	//
	// There is no implicit filepath.Dir(DataDir) derivation: under the D60
	// data-dir bifurcation DataDir is $HOME/.yoloai/library, so its parent
	// is $HOME/.yoloai — not $HOME. Silently deriving it there sent every
	// seed/credential lookup to the wrong home and launched agents
	// unconfigured, so the boundary now demands an explicit value. The CLI
	// passes cliutil.Layout().HomeDir (its single licensed os.UserHomeDir()
	// site); embedders pass the host user's home. F13 (2026-05-27).
	HomeDir string

	// Backend selects the runtime backend (yoloai.BackendDocker,
	// yoloai.BackendTart, etc.). OPTIONAL — empty constructs a backend-less
	// Client (A2/A3) that serves host-only reads and, via System(),
	// cross-backend admin without ever opening a connection. A backend-bound
	// operation (Exec, Attach, Start, lifecycle, Create, List, Clone, …) on a
	// backend-less Client returns ErrBackendRequired.
	//
	// No implicit default. Backend selection is inherently ambient (it
	// probes which container daemons are installed), so it belongs at the
	// outermost boundary, not silently inside Client construction (§4 /
	// §12). The CLI resolves it from its --backend / --isolation / --os
	// flags via runtime.SelectBackend and passes the concrete result here.
	// Embedders that want that same auto-detection call the public
	// yoloai.SelectBackend helper and pass its result. When set, the backend
	// is opened lazily on the first backend-bound op, not at construction.
	Backend BackendType

	// Logger receives structured log output. Default: slog.Default().
	Logger *slog.Logger

	// Output receives human-readable progress messages. Default: io.Discard.
	Output io.Writer

	// Input provides interactive input. Default: an empty reader (immediate
	// EOF) — the library never reads the embedding process's os.Stdin (§12: no
	// ambient configuration). Embedders that want interactive input pass it
	// explicitly; the CLI passes cmd.InOrStdin() at its boundary.
	Input io.Reader

	// Version is the yoloAI version string stamped into each created
	// sandbox's environment.json. The CLI fills it from build info; embedders may
	// leave it empty. Not a per-create input — it lives here so Create
	// callers don't repeat it.
	Version string

	// Env is the authorized host-environment snapshot for this Client. It is
	// the ONLY source from which the library resolves user-declared ${VAR}
	// references in config/profile values AND the agent's API-key / auth-hint
	// credential values injected into the sandbox — the library never reads
	// the live process environment for them (§12). Optional; nil/empty means
	// no ${VAR} resolution and no env-sourced credentials.
	//
	// The CLI fills this from its single licensed os.Environ() snapshot (plus
	// sudo-stripped-credential recovery). A multi-principal embedder MUST pass
	// each principal's own environment here — never the daemon's process env —
	// so credentials stay principal-scoped (D58/D59).
	//
	// Env is also where the selected backend reads its daemon-connection
	// settings (the library never reads them from the process env, §12). Include
	// whichever apply to your Backend:
	//   - docker:  DOCKER_HOST, DOCKER_CERT_PATH, DOCKER_TLS_VERIFY,
	//              DOCKER_API_VERSION. All optional — absent/blank means the
	//              default local socket with no TLS (same as the docker CLI).
	//   - podman:  CONTAINER_HOST, DOCKER_HOST, XDG_RUNTIME_DIR for socket
	//              discovery. Absent falls back to the well-known socket paths.
	//   - seatbelt: locale/terminal vars (PATH, HOME, TERM, LANG, LC_*) are
	//              forwarded to the on-host agent from this snapshot.
	Env map[string]string

	// Principal namespaces this Client's sandboxes under an owning principal
	// (tenant/user), so two principals can each own a sandbox of the same name
	// without colliding on the runtime backend. Client-scoped, not per-call —
	// the Client is the principal-scoped handle (D58/D59).
	//
	// Empty ("") is the default no-principal sentinel: instance names elide the
	// segment (yoloai-<name>) and behavior is identical to today. Non-empty
	// must be ≤8 alphanumeric chars (parsed at construction; invalid is
	// rejected with a *UsageError). See D62.
	Principal string

	// SecretsStagingDir is the host directory under which the library stages a
	// per-sandbox temp dir of plaintext agent credentials before bind-mounting
	// it in. Optional; empty ("") means the OS default temp dir (os.TempDir()),
	// which is what the single-principal CLI uses.
	//
	// The library decides WHAT to stage and WHEN to delete it; the embedder
	// supplies WHERE (D59 refinement). A multi-principal daemon points each
	// principal's Client at that principal's own tmpfs so plaintext
	// credentials never share a staging root across principals.
	SecretsStagingDir string
}

// Client is the simple entry point for yoloAI operations.
// A Client is safe for concurrent use by multiple goroutines.
// Construct with NewWithOptions.
type Client struct {
	layout  config.Layout       // Q-W: DataDir-rooted path resolver propagated to Engine + apply
	backend runtime.BackendType // selected backend; "" = backend-less (host-only reads/admin), backend-bound ops return ErrBackendRequired
	logger  *slog.Logger        // for the lazily-built Engine
	version string              // yoloAI version stamped into created sandboxes' environment.json
	output  io.Writer           // Options.Output (defaulted to io.Discard); seeds per-call progress writers (F8)
	input   io.Reader           // Options.Input (defaulted to an empty reader, never os.Stdin — §12); threaded to create.Run via state.Deps

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

// NewWithOptions creates a Client with explicit options.
// Options.DataDir is REQUIRED (Q-W.5); empty is rejected.
func NewWithOptions(ctx context.Context, opts Options) (*Client, error) {
	if opts.DataDir == "" {
		return nil, fmt.Errorf("yoloai: Options.DataDir is required (no implicit $HOME fallback; see development-principles.md §12)")
	}
	if opts.HomeDir == "" {
		return nil, yoerrors.NewUsageError("yoloai: Options.HomeDir is required (no implicit filepath.Dir(DataDir) derivation; under the D60 bifurcation DataDir is $HOME/.yoloai/library, so its parent is not $HOME). Pass the host user's home explicitly; the CLI uses cliutil.Layout().HomeDir. See development-principles.md §12.")
	}
	principal, err := config.ParsePrincipalSegment(opts.Principal)
	if err != nil {
		return nil, yoerrors.NewUsageError("yoloai: invalid Options.Principal: %v", err)
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

	// The backend connection is NOT opened here (A2/A3). Options.Backend is
	// optional: a backend-less Client serves host-only reads and admin without
	// ever connecting; backend-bound ops open the runtime lazily on first use
	// (ensure) or return ErrBackendRequired when Backend is "".
	return &Client{
		layout:  layout,
		backend: opts.Backend,
		logger:  logger,
		version: opts.Version,
		output:  output,
		input:   input,
	}, nil
}

// ErrBackendRequired is returned by backend-bound operations (Exec, Attach,
// Start, Stop, lifecycle, Create, List, Clone, …) when the Client was
// constructed without Options.Backend. A backend-less Client still serves
// host-only reads (Workdir host-git, on-disk allowlist, filesystem readers)
// and, via System(), cross-backend admin. Set Options.Backend — resolve it at
// the boundary with yoloai.SelectBackend — to enable backend-bound ops.
var ErrBackendRequired = yoerrors.NewUsageError("yoloai: this operation requires a backend, but the Client was constructed without Options.Backend (backend-less). Set Options.Backend (e.g. via yoloai.SelectBackend) to enable backend-bound operations. See development-principles.md §4.")

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

// deps bundles the Client's runtime, layout, and input into state.Deps for
// use with lifecycle and create free functions. Callers must ensure the
// runtime is open (via ensure) before calling deps for a backend-bound op.
func (c *Client) deps() state.Deps {
	return state.Deps{Runtime: c.rt, Layout: c.layout, Input: c.input}
}

// RunOptions configures a sandbox run.
type RunOptions struct {
	// Name is the sandbox identifier. Required.
	Name string

	// WorkDir is the host directory to work in. Required.
	// Mounted as :copy by default — the original is protected.
	WorkDir string

	// Prompt is the task description sent to the agent.
	// If empty, the sandbox starts without a prompt (interactive mode).
	Prompt string

	// Agent selects the AI agent (yoloai.AgentClaude, yoloai.AgentGemini,
	// yoloai.AgentCodex, …). Default: read from config.yaml, then
	// yoloai.AgentClaude.
	Agent AgentType

	// Model selects the model. Default: read from config.yaml, then agent default.
	Model string

	// Profile applies a named profile for environment, image, and settings.
	// Default: read from config.yaml, then no profile.
	Profile string

	// Replace destroys any existing sandbox with the same name before creating
	// a new one. The existing sandbox must have no unapplied changes.
	Replace bool

	// AllowDirtyWorkdir proceeds even when WorkDir has uncommitted git changes.
	// Default false: Run refuses with *DirtyWorkdirError rather than letting the
	// agent see — and possibly clobber — uncommitted work. Set true to
	// consciously proceed (the non-interactive equivalent of answering the CLI's
	// dirty-repo prompt).
	AllowDirtyWorkdir bool

	// Wait blocks until the agent reaches StatusDone, StatusFailed, or
	// StatusStopped, polling every 5 seconds. Default: false.
	Wait bool

	// OnProgress receives status updates during the run. The first argument
	// is the sandbox name; the second is a human-readable message.
	// Safe to call concurrently from multiple goroutines (e.g., batch runs).
	OnProgress func(name, msg string)
}

// pollUntilDone polls the sandbox status until it reaches a terminal state.
func (c *Client) pollUntilDone(ctx context.Context, name string, progress func(string, string)) (*Info, error) {
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
			return infoFromStatus(info), nil
		case sandbox.StatusActive, sandbox.StatusIdle:
			// still running — continue polling
		default: // StatusBroken, StatusUnavailable
			return infoFromStatus(info), nil
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
// final sandbox Info. If opts.Wait is false, Run returns immediately after
// the agent is launched; the Info reflects the initial state.
func (c *Client) Run(ctx context.Context, opts RunOptions) (*Info, error) {
	createOpts := opts.materialize()
	if createOpts.Agent == "" {
		createOpts.Agent = AgentType(resolveAgentFromConfig(c.layout))
	}
	if createOpts.Model == "" {
		createOpts.Model = resolveModelFromConfig(c.layout)
	}
	if createOpts.Profile == "" {
		createOpts.Profile = resolveProfileFromConfig()
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
		return infoFromStatus(si), nil
	}

	return c.pollUntilDone(ctx, opts.Name, opts.OnProgress)
}

// List returns info for all sandboxes.
func (c *Client) List(ctx context.Context) ([]*Info, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	sis, err := c.manager.List(ctx)
	if err != nil {
		return nil, err
	}
	return infosFromStatus(sis), nil
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
func (c *Client) Clone(ctx context.Context, opts CloneOptions) error {
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
	if meta, err := store.LoadEnvironment(dstDir); err == nil && meta.Backend != "" {
		rt, rtErr := newRuntime(ctx, meta.Backend, c.layout)
		if rtErr != nil {
			return fmt.Errorf("connect to %s backend to overwrite %q: %w", meta.Backend, dest, rtErr)
		}
		defer rt.Close() //nolint:errcheck // best-effort close after teardown
		deps = state.Deps{Runtime: rt, Layout: c.layout, Input: c.input}
	}

	if _, err := lifecycle.Destroy(ctx, deps, dest); err != nil {
		return fmt.Errorf("overwrite existing destination %q: %w", dest, err)
	}
	return nil
}

// Create provisions a new sandbox from CreateOptions and (unless
// opts.NoStart) starts the container with the agent. Returns the sandbox
// name on success — currently always opts.Name, since name is required
// (no auto-generation). Use Run for the higher-level "create + wait for
// terminal status" convenience.
func (c *Client) Create(ctx context.Context, opts CreateOptions) (string, error) {
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

// SelectBackend resolves a concrete backend from a preferred backend plus
// isolation / OS routing preferences, mirroring what the CLI does for its
// --backend / --isolation / --os flags. It probes which container daemons are
// installed and falls back accordingly, returning the chosen backend and a
// human-readable warning ("" when none).
//
// Backend selection is inherently ambient (it probes which container daemons
// are installed), so it belongs at the outermost boundary, not hidden inside
// Client construction (§4 / §12). Embedders that want the CLI's auto-detection
// call this at their boundary and pass the result as Options.Backend; those
// that leave Backend empty get a backend-less Client (host-only reads + admin).
//
// env is the caller's host-env snapshot (the same map passed as Options.Env):
// container-slot probes read DOCKER_HOST / CONTAINER_HOST / XDG_RUNTIME_DIR
// from it rather than the process environment, so selection stays
// principal-scoped (§12). May be nil to probe default socket paths only.
func SelectBackend(ctx context.Context, preferred BackendType, isolation IsolationMode, targetOS string, env map[string]string) (BackendType, string) {
	return runtime.SelectBackend(ctx, preferred, isolation, targetOS, env)
}

// SelectContainerBackend resolves a concrete container backend from a preferred
// backend, probing which container daemons are installed and falling back
// accordingly. It is the container-only counterpart to SelectBackend (no
// isolation/OS routing), mirroring what lifecycle commands do when resolving a
// backend for an existing sandbox. Returns the chosen backend and a
// human-readable warning ("" when none).
//
// env is the caller's host-env snapshot (the same map passed as Options.Env);
// see SelectBackend. May be nil to probe default socket paths only.
func SelectContainerBackend(ctx context.Context, preferred BackendType, env map[string]string) (BackendType, string) {
	return runtime.SelectContainerBackend(ctx, preferred, env)
}

// IsolationAvailability reports whether the given isolation mode is usable for a
// target OS on the given host OS, returning a human-readable reason and a
// remediation hint when it is not. Embedders validate a requested isolation
// mode at their boundary before constructing a Client (the CLI does this for
// its --isolation / --os flags).
func IsolationAvailability(isolation IsolationMode, targetOS, hostOS string) (available bool, reason, help string) {
	return runtime.IsolationAvailability(isolation, targetOS, hostOS)
}

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

func resolveProfileFromConfig() string {
	return ""
}

func newRuntime(ctx context.Context, backend runtime.BackendType, layout config.Layout) (runtime.Runtime, error) {
	if backend == "" {
		backend = runtime.BackendDocker
	}
	return runtime.New(ctx, backend, layout)
}
