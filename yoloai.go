// ABOUTME: Public high-level Client API (Run, Apply, Diff, Destroy) for embedding
// ABOUTME: yoloAI in Go programs without interacting with the CLI or sandbox package.
// Package yoloai is the orchestration layer for yoloAI. Both the CLI
// (internal/cli) and external embedders use it as the entry point for
// running AI coding agents in isolated sandboxes.
//
// Two clients live here:
//
//   - Client — creation and cross-sandbox operations: Run, Create, Clone,
//     List, plus the per-sandbox handle accessor Sandbox(name). Per-sandbox
//     operations (Inspect, Start, Stop, Restart, Reset, Destroy, Attach,
//     Exec, …) live on that *Sandbox handle, not the Client root (F2).
//     Constructed via NewWithOptions; holds a single backend connection.
//     Use one Client per backend.
//
//   - SystemClient — admin/cross-backend operations: DiskUsage, Prune,
//     Build, Check. Reached via Client.System() or constructed directly
//     via NewSystemClient (when no backend Client is needed). Decoupled
//     from a single backend — iterates registered backends internally.
//
// Following the W-L8 layering refactor, the CLI is a thin shell over
// Client + SystemClient; orchestration logic lives here, not in
// internal/cli. New CLI commands should call Client/SystemClient
// methods rather than reaching into sandbox/* or runtime/* directly.
//
// Typical usage:
//
//	client, err := yoloai.NewWithOptions(ctx, yoloai.Options{
//	    DataDir: filepath.Join(os.Getenv("HOME"), ".yoloai", "library"),
//	    HomeDir: os.Getenv("HOME"), // required; where ~/.claude etc. resolve
//	    Backend: yoloai.BackendDocker, // required; or yoloai.SelectBackend(...)
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
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	_ "github.com/kstenerud/yoloai/internal/runtime/docker"   // register backend
	_ "github.com/kstenerud/yoloai/internal/runtime/podman"   // register backend
	_ "github.com/kstenerud/yoloai/internal/runtime/seatbelt" // register backend
	_ "github.com/kstenerud/yoloai/internal/runtime/tart"     // register backend
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/create"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
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
	// yoloai.BackendTart, etc.). REQUIRED — empty is rejected at
	// construction with a *UsageError (F4).
	//
	// No implicit default. Backend selection is inherently ambient (it
	// probes which container daemons are installed), so it belongs at the
	// outermost boundary, not silently inside Client construction (§4 /
	// §12). The CLI resolves it from its --backend / --isolation / --os
	// flags via runtime.SelectBackend and passes the concrete result here.
	// Embedders that want that same auto-detection call the public
	// yoloai.SelectBackend helper and pass its result.
	Backend BackendName

	// Logger receives structured log output. Default: slog.Default().
	Logger *slog.Logger

	// Output receives human-readable progress messages. Default: io.Discard.
	Output io.Writer

	// Input provides interactive input. Default: os.Stdin.
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
}

// Client is the simple entry point for yoloAI operations.
// A Client is safe for concurrent use by multiple goroutines.
// Construct with New or NewWithOptions.
type Client struct {
	manager *sandbox.Engine
	rt      runtime.Runtime
	layout  config.Layout // Q-W: DataDir-rooted path resolver propagated to Engine + apply
	version string        // yoloAI version stamped into created sandboxes' environment.json
	output  io.Writer     // Options.Output (defaulted to io.Discard); seeds per-call progress writers (F8)
	input   io.Reader     // Options.Input (defaulted to os.Stdin); threaded to create.Run via state.Deps
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
	if opts.Backend == "" {
		return nil, yoerrors.NewUsageError("yoloai: Options.Backend is required — empty is not a valid backend (F4). Resolve it at the boundary before constructing the Client, e.g. yoloai.SelectBackend(ctx, preferred, isolation, os). See development-principles.md §4.")
	}

	principal, err := config.ParsePrincipalSegment(opts.Principal)
	if err != nil {
		return nil, yoerrors.NewUsageError("yoloai: invalid Options.Principal: %v", err)
	}

	layout := config.NewLayoutFor(opts.DataDir, opts.HomeDir).WithPrincipal(principal)
	layout.Env = opts.Env

	backend := opts.Backend
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
		input = os.Stdin //nolint:forbidigo // §12: documented Options.Input default at the library entry; embedders override, the CLI passes IOStreams
	}

	rt, err := newRuntime(ctx, backend, layout)
	if err != nil {
		return nil, fmt.Errorf("connect to %s backend: %w", backend, err)
	}

	mgr := sandbox.NewEngine(rt, logger, input, sandbox.WithLayout(layout))
	return &Client{manager: mgr, rt: rt, layout: layout, version: opts.Version, output: output, input: input}, nil
}

// Close releases the underlying runtime connection.
func (c *Client) Close() error {
	return c.rt.Close()
}

// deps bundles the Client's runtime, layout, and input into state.Deps for
// use with lifecycle and create free functions.
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
	Agent AgentName

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

// Run creates a sandbox with the given options and starts the agent.
// Equivalent to 'yoloai new <name> <workdir> --prompt <prompt>'.
//
// If opts.Wait is true, Run blocks until the agent finishes and returns the
// final sandbox Info. If opts.Wait is false, Run returns immediately after
// the agent is launched; the Info reflects the initial state.
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

func (c *Client) Run(ctx context.Context, opts RunOptions) (*Info, error) {
	createOpts := opts.materialize()
	if createOpts.Agent == "" {
		createOpts.Agent = AgentName(resolveAgentFromConfig(c.layout))
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
	sis, err := c.manager.List(ctx)
	if err != nil {
		return nil, err
	}
	return infosFromStatus(sis), nil
}

// Clone copies an existing sandbox's state into a new sandbox. The runtime
// is not consulted — clone is a disk-only operation that copies the source
// sandbox dir under DataDir/sandboxes/. Embedders still construct the Client
// with a backend because most clone workflows start the destination right
// after; the wasted connection for pure --no-start clones is acceptable.
func (c *Client) Clone(ctx context.Context, opts CloneOptions) error {
	return c.manager.Clone(ctx, opts.toInternal())
}

// Create provisions a new sandbox from CreateOptions and (unless
// opts.NoStart) starts the container with the agent. Returns the sandbox
// name on success — currently always opts.Name, since name is required
// (no auto-generation). Use Run for the higher-level "create + wait for
// terminal status" convenience.
func (c *Client) Create(ctx context.Context, opts CreateOptions) (string, error) {
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
// each step is a no-op once its artifact exists. The interactive setup
// wizard is a separate flow — see SystemClient.SetupStatus / SystemClient.Setup.
func (c *Client) EnsureSetup(ctx context.Context) error {
	return c.manager.EnsureSetup(ctx, c.output)
}

// --- private helpers ---

// SelectBackend resolves a concrete backend from a preferred backend plus
// isolation / OS routing preferences, mirroring what the CLI does for its
// --backend / --isolation / --os flags. It probes which container daemons are
// installed and falls back accordingly, returning the chosen backend and a
// human-readable warning ("" when none).
//
// Because Options.Backend is required (F4), embedders that want the CLI's
// auto-detection call this at their boundary and pass the result into
// NewWithOptions — keeping the ambient probe explicit rather than hidden in
// Client construction (§4 / §12).
func SelectBackend(ctx context.Context, preferred BackendName, isolation IsolationMode, targetOS string) (BackendName, string) {
	return runtime.SelectBackend(ctx, preferred, isolation, targetOS)
}

// SelectContainerBackend resolves a concrete container backend from a preferred
// backend, probing which container daemons are installed and falling back
// accordingly. It is the container-only counterpart to SelectBackend (no
// isolation/OS routing), mirroring what lifecycle commands do when resolving a
// backend for an existing sandbox. Returns the chosen backend and a
// human-readable warning ("" when none).
func SelectContainerBackend(ctx context.Context, preferred BackendName) (BackendName, string) {
	return runtime.SelectContainerBackend(ctx, preferred)
}

// IsolationAvailability reports whether the given isolation mode is usable for a
// target OS on the given host OS, returning a human-readable reason and a
// remediation hint when it is not. Embedders validate a requested isolation
// mode at their boundary before constructing a Client (the CLI does this for
// its --isolation / --os flags).
func IsolationAvailability(isolation IsolationMode, targetOS, hostOS string) (available bool, reason, help string) {
	return runtime.IsolationAvailability(isolation, targetOS, hostOS)
}

// resolveBackendFromConfig picks a default backend for SystemClient admin
// operations that aren't bound to a specific backend. Reads the user's
// container_backend preference from config and routes it through
// runtime.SelectBackend; if that backend isn't available, SelectBackend falls
// back to any other registered container backend (the warning is discarded —
// admin callers don't surface it).
func resolveBackendFromConfig(ctx context.Context, layout config.Layout) runtime.BackendName {
	var preferred runtime.BackendName
	if cfg, err := config.LoadDefaultsConfig(layout); err == nil {
		preferred = runtime.BackendName(cfg.ContainerBackend)
	}
	backend, _ := runtime.SelectBackend(ctx, preferred, "", "")
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

func newRuntime(ctx context.Context, backend runtime.BackendName, layout config.Layout) (runtime.Runtime, error) {
	if backend == "" {
		backend = runtime.BackendDocker
	}
	return runtime.New(ctx, backend, layout)
}
