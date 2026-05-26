// ABOUTME: Public high-level Client API (Run, Apply, Diff, Destroy) for embedding
// ABOUTME: yoloAI in Go programs without interacting with the CLI or sandbox package.
// Package yoloai provides a simple, high-level API for running AI coding agents
// in isolated sandboxes. For advanced use, import the sandbox and config packages
// directly.
//
// Typical usage:
//
//	client, err := yoloai.NewWithOptions(ctx, yoloai.Options{
//	    DataDir: filepath.Join(os.Getenv("HOME"), ".yoloai"),
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
//	if info.Status == sandbox.StatusDone {
//	    client.Apply(ctx, info.Meta.Name)
//	}
package yoloai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
	_ "github.com/kstenerud/yoloai/runtime/docker"   // register backend
	_ "github.com/kstenerud/yoloai/runtime/podman"   // register backend
	_ "github.com/kstenerud/yoloai/runtime/seatbelt" // register backend
	_ "github.com/kstenerud/yoloai/runtime/tart"     // register backend
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/sandbox/patch"
)

// Sentinel errors returned by Client methods.
var (
	// ErrSandboxExists is returned by Run when a sandbox with the given name
	// already exists and Replace is false.
	ErrSandboxExists = sandbox.ErrSandboxExists

	// ErrUnappliedChanges is returned by Destroy when the sandbox has unapplied
	// changes and force is false.
	ErrUnappliedChanges = errors.New("sandbox has unapplied changes")
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

	// Backend selects the runtime backend: "docker", "tart", or "seatbelt".
	// Default: read from config.yaml, then "docker".
	Backend string

	// Logger receives structured log output. Default: slog.Default().
	Logger *slog.Logger

	// Output receives human-readable progress messages. Default: io.Discard.
	Output io.Writer

	// Input provides interactive input. Default: os.Stdin.
	Input io.Reader
}

// Client is the simple entry point for yoloAI operations.
// A Client is safe for concurrent use by multiple goroutines.
// Construct with New or NewWithOptions.
type Client struct {
	manager *sandbox.Manager
	rt      runtime.Runtime
	layout  config.Layout // Q-W: DataDir-rooted path resolver propagated to Manager + apply
}

// NewWithOptions creates a Client with explicit options.
// Options.DataDir is REQUIRED (Q-W.5); empty is rejected.
func NewWithOptions(ctx context.Context, opts Options) (*Client, error) {
	if opts.DataDir == "" {
		return nil, fmt.Errorf("yoloai: Options.DataDir is required (no implicit $HOME fallback; see development-principles.md §12)")
	}

	layout := config.NewLayout(opts.DataDir)

	backend := opts.Backend
	if backend == "" {
		backend = resolveBackendFromConfig(ctx, layout)
	}
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
		input = os.Stdin
	}

	rt, err := newRuntime(ctx, backend, layout)
	if err != nil {
		return nil, fmt.Errorf("connect to %s backend: %w", backend, err)
	}

	mgr := sandbox.NewManager(rt, logger, input, output, sandbox.WithLayout(layout))
	return &Client{manager: mgr, rt: rt, layout: layout}, nil
}

// Close releases the underlying runtime connection.
func (c *Client) Close() error {
	return c.rt.Close()
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

	// Agent selects the AI agent (e.g., "claude", "gemini", "codex").
	// Default: read from config.yaml, then "claude".
	Agent string

	// Model selects the model. Default: read from config.yaml, then agent default.
	Model string

	// Profile applies a named profile for environment, image, and settings.
	// Default: read from config.yaml, then no profile.
	Profile string

	// Replace destroys any existing sandbox with the same name before creating
	// a new one. The existing sandbox must have no unapplied changes.
	Replace bool

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
func (c *Client) pollUntilDone(ctx context.Context, name string, progress func(string, string)) (*sandbox.Info, error) {
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
			return info, nil
		case sandbox.StatusActive, sandbox.StatusIdle:
			// still running — continue polling
		default: // StatusBroken, StatusUnavailable
			return info, nil
		}
		if progress != nil {
			progress(name, string(info.Status))
		}
	}
}

func (c *Client) Run(ctx context.Context, opts RunOptions) (*sandbox.Info, error) {
	if err := c.manager.EnsureSetup(ctx); err != nil {
		return nil, fmt.Errorf("setup: %w", err)
	}

	agent := opts.Agent
	if agent == "" {
		agent = resolveAgentFromConfig(c.layout)
	}
	model := opts.Model
	if model == "" {
		model = resolveModelFromConfig(c.layout)
	}
	profile := opts.Profile
	if profile == "" {
		profile = resolveProfileFromConfig()
	}

	createOpts := sandbox.CreateOptions{
		Name: opts.Name,
		Workdir: sandbox.DirSpec{
			Path: opts.WorkDir,
			Mode: sandbox.DirModeCopy,
		},
		Agent:   agent,
		Model:   model,
		Profile: profile,
		Prompt:  opts.Prompt,
		Replace: opts.Replace,
		Yes:     true, // non-interactive: don't prompt for confirmation
	}

	if _, err := c.manager.Create(ctx, createOpts); err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}

	if opts.OnProgress != nil {
		opts.OnProgress(opts.Name, "agent running")
	}

	if !opts.Wait {
		return c.manager.Inspect(ctx, opts.Name)
	}

	return c.pollUntilDone(ctx, opts.Name, opts.OnProgress)
}

// Diff returns the diff of agent changes for a sandbox.
// Equivalent to 'yoloai diff <name>'.
func (c *Client) Diff(_ context.Context, name string) ([]*patch.DiffResult, error) {
	return patch.GenerateMultiDiff(patch.DiffOptions{Name: name, Layout: c.layout})
}

// Apply applies the agent's committed changes back to the original host
// directories. Equivalent to 'yoloai apply <name>'.
// Uncommitted (work-in-progress) edits the agent left behind are NOT applied;
// use ApplyWithOptions to opt in.
// Returns (nil, nil) when there is nothing to apply — branch on
// len(results) == 0 rather than on a sentinel error (Q-P).
func (c *Client) Apply(ctx context.Context, name string) ([]*patch.ApplyResult, error) {
	return patch.ApplyAll(ctx, c.layout, c.rt, name, false)
}

// ApplyOptions controls Client.ApplyWithOptions.
type ApplyOptions struct {
	// IncludeWIP, when true, additionally applies the agent's uncommitted
	// changes as unstaged modifications on the host. Mirrors the CLI's
	// `yoloai apply --include-wip`.
	IncludeWIP bool
}

// ApplyWithOptions is Apply with explicit options. Returns (nil, nil)
// when there is nothing to apply — branch on len(results) == 0 rather
// than on a sentinel error (Q-P).
func (c *Client) ApplyWithOptions(ctx context.Context, name string, opts ApplyOptions) ([]*patch.ApplyResult, error) {
	return patch.ApplyAll(ctx, c.layout, c.rt, name, opts.IncludeWIP)
}

// List returns info for all sandboxes.
func (c *Client) List(ctx context.Context) ([]*sandbox.Info, error) {
	return c.manager.List(ctx)
}

// Inspect returns combined metadata and live state for a single sandbox.
func (c *Client) Inspect(ctx context.Context, name string) (*sandbox.Info, error) {
	return c.manager.Inspect(ctx, name)
}

// Stop stops the running container for a sandbox without destroying it.
func (c *Client) Stop(ctx context.Context, name string) error {
	return c.manager.Stop(ctx, name)
}

// Destroy removes the sandbox and its container.
// If the sandbox has unapplied changes and force is false, returns ErrUnappliedChanges.
func (c *Client) Destroy(ctx context.Context, name string, force bool) error {
	if !force {
		needs, _ := c.manager.NeedsConfirmation(ctx, name)
		if needs {
			return ErrUnappliedChanges
		}
	}
	return c.manager.Destroy(ctx, name)
}

// NeedsConfirmation reports whether destroying the named sandbox should prompt
// the user — sandbox is running, workdir is dirty, or there are unapplied
// commits. The reason string is suitable for human display. Embedders use
// this to render their own confirmation UX before calling Destroy(force=true).
func (c *Client) NeedsConfirmation(ctx context.Context, name string) (bool, string) {
	return c.manager.NeedsConfirmation(ctx, name)
}

// Clone copies an existing sandbox's state into a new sandbox. The runtime
// is not consulted — clone is a disk-only operation that copies the source
// sandbox dir under DataDir/sandboxes/. Embedders still construct the Client
// with a backend because most clone workflows start the destination right
// after; the wasted connection for pure --no-start clones is acceptable.
func (c *Client) Clone(ctx context.Context, opts sandbox.CloneOptions) error {
	return c.manager.Clone(ctx, opts)
}

// Start launches (or relaunches) the container for an existing sandbox.
// The sandbox must exist on disk; use Run to create a new sandbox.
func (c *Client) Start(ctx context.Context, name string, opts sandbox.StartOptions) error {
	return c.manager.Start(ctx, name, opts)
}

// --- private helpers ---

// resolveBackendFromConfig picks the container backend for a Client created
// without an explicit Backend in Options. Reads the user's container_backend
// preference from config and lets runtime.SelectContainerBackend probe it —
// if the preferred backend isn't available, the helper falls back to any
// other registered container backend with a stderr-side warning. The Client
// emits no warning of its own (embedders may want to suppress it); we
// silently take the fallback verdict.
func resolveBackendFromConfig(ctx context.Context, layout config.Layout) string {
	var preferred string
	if cfg, err := config.LoadDefaultsConfig(layout); err == nil {
		preferred = cfg.ContainerBackend
	}
	backend, _ := runtime.SelectContainerBackend(ctx, preferred)
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

func newRuntime(ctx context.Context, backend string, layout config.Layout) (runtime.Runtime, error) {
	if backend == "" {
		backend = "docker"
	}
	return runtime.New(ctx, backend, layout)
}
