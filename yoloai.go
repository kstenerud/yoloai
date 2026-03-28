// Package yoloai provides a simple, high-level API for running AI coding agents
// in isolated sandboxes. For advanced use, import the sandbox and config packages
// directly.
//
// Typical usage:
//
//	client, err := yoloai.New(ctx)
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
)

// Sentinel errors returned by Client methods.
var (
	// ErrSandboxExists is returned by Run when a sandbox with the given name
	// already exists and Replace is false.
	ErrSandboxExists = sandbox.ErrSandboxExists

	// ErrUnappliedChanges is returned by Destroy when the sandbox has unapplied
	// changes and force is false.
	ErrUnappliedChanges = errors.New("sandbox has unapplied changes")

	// ErrNoChanges is returned by Apply when there is nothing to apply.
	ErrNoChanges = sandbox.ErrNoChanges
)

// Options configures a Client.
type Options struct {
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
}

// New creates a Client with default options. The backend is selected from
// config.yaml, falling back to "docker". Returns an error if the runtime
// backend cannot be connected.
func New(ctx context.Context) (*Client, error) {
	return NewWithOptions(ctx, Options{})
}

// NewWithOptions creates a Client with explicit options.
func NewWithOptions(ctx context.Context, opts Options) (*Client, error) {
	backend := opts.Backend
	if backend == "" {
		backend = resolveBackendFromConfig()
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

	rt, err := newRuntime(ctx, backend)
	if err != nil {
		return nil, fmt.Errorf("connect to %s backend: %w", backend, err)
	}

	mgr := sandbox.NewManager(rt, logger, input, output)
	return &Client{manager: mgr, rt: rt}, nil
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
func (c *Client) Run(ctx context.Context, opts RunOptions) (*sandbox.Info, error) {
	if err := c.manager.EnsureSetup(ctx); err != nil {
		return nil, fmt.Errorf("setup: %w", err)
	}

	agent := opts.Agent
	if agent == "" {
		agent = resolveAgentFromConfig()
	}
	model := opts.Model
	if model == "" {
		model = resolveModelFromConfig()
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

	progress := opts.OnProgress
	if progress != nil {
		progress(opts.Name, "agent running")
	}

	if !opts.Wait {
		return c.manager.Inspect(ctx, opts.Name)
	}

	// Poll until done
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}

		info, err := c.manager.Inspect(ctx, opts.Name)
		if err != nil {
			return nil, fmt.Errorf("inspect sandbox: %w", err)
		}
		switch info.Status {
		case sandbox.StatusDone, sandbox.StatusFailed, sandbox.StatusStopped, sandbox.StatusRemoved:
			return info, nil
		}
		if progress != nil {
			progress(opts.Name, string(info.Status))
		}
	}
}

// Diff returns the diff of agent changes for a sandbox.
// Equivalent to 'yoloai diff <name>'.
func (c *Client) Diff(_ context.Context, name string) ([]*sandbox.DiffResult, error) {
	return sandbox.GenerateMultiDiff(sandbox.DiffOptions{Name: name})
}

// Apply applies the agent's changes back to the original host directories.
// Equivalent to 'yoloai apply <name>'.
// Returns ErrNoChanges if there is nothing to apply.
func (c *Client) Apply(ctx context.Context, name string) ([]*sandbox.ApplyResult, error) {
	return sandbox.ApplyAll(ctx, c.rt, name)
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

// --- private helpers ---

func resolveBackendFromConfig() string {
	cfg, err := config.LoadDefaultsConfig()
	if err == nil && cfg.ContainerBackend != "" {
		return cfg.ContainerBackend
	}
	return "docker"
}

func resolveAgentFromConfig() string {
	cfg, err := config.LoadDefaultsConfig()
	if err == nil && cfg.Agent != "" {
		return cfg.Agent
	}
	return "claude"
}

func resolveModelFromConfig() string {
	cfg, err := config.LoadDefaultsConfig()
	if err == nil && cfg.Model != "" {
		return cfg.Model
	}
	return ""
}

func resolveProfileFromConfig() string {
	return ""
}

func newRuntime(ctx context.Context, backend string) (runtime.Runtime, error) {
	if backend == "" {
		backend = "docker"
	}
	return runtime.New(ctx, backend)
}
