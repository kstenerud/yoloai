// ABOUTME: Public high-level Client API (Run, Apply, Diff, Destroy) for embedding
// ABOUTME: yoloAI in Go programs without interacting with the CLI or sandbox package.
// Package yoloai is the orchestration layer for yoloAI. Both the CLI
// (internal/cli) and external embedders use it as the entry point for
// running AI coding agents in isolated sandboxes.
//
// Two clients live here:
//
//   - Client — sandbox-scoped operations: Run, Diff, Apply, Stop, Destroy,
//     List, Inspect, Attach, Exec. Constructed via NewWithOptions; holds
//     a single backend connection. Use one Client per backend.
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

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	_ "github.com/kstenerud/yoloai/internal/runtime/docker"   // register backend
	_ "github.com/kstenerud/yoloai/internal/runtime/podman"   // register backend
	_ "github.com/kstenerud/yoloai/internal/runtime/seatbelt" // register backend
	_ "github.com/kstenerud/yoloai/internal/runtime/tart"     // register backend
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/patch"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
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

	// HomeDir is the host user's home directory. Optional — if empty,
	// the Client derives it as filepath.Dir(DataDir) for the common
	// case where DataDir lives directly inside $HOME (e.g.
	// $HOME/.yoloai). Embedders whose DataDir lives elsewhere
	// (/var/lib/yoloai, multi-tenant per-user roots) must pass this
	// explicitly — otherwise ~-expansion in user-supplied paths,
	// seed-file lookups (~/.claude, ~/.codex), and auth-file discovery
	// resolve to filepath.Dir(DataDir) instead of the user's actual
	// $HOME. F13 (2026-05-27).
	HomeDir string

	// Backend selects the runtime backend (yoloai.BackendDocker,
	// yoloai.BackendTart, etc.). Default: read from config.yaml, then
	// yoloai.BackendDocker. Empty BackendName ("") is treated as "use
	// the default" by every consumer of Options.Backend.
	//
	// When Backend is empty, the Client routes Isolation + OS through
	// runtime.SelectBackend — the same routing the CLI applies for its
	// --isolation / --os flags (F21). An explicit Backend always wins
	// over Isolation/OS routing.
	Backend BackendName

	// Isolation and OS are backend-routing preferences honored only when
	// Backend is empty. They mirror the CLI's --isolation / --os flags:
	// OS=="mac" routes to seatbelt (or tart for Isolation vm); Isolation
	// vm / vm-enhanced route to containerd. Both empty (the default)
	// means plain container-slot selection. Embedders that want the same
	// backend routing the CLI performs set these instead of
	// re-implementing it. F21.
	Isolation IsolationMode
	OS        string

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

	var layout config.Layout
	if opts.HomeDir != "" {
		layout = config.NewLayoutFor(opts.DataDir, opts.HomeDir)
	} else {
		layout = config.NewLayout(opts.DataDir)
	}

	backend := opts.Backend
	if backend == "" {
		backend = resolveBackendFromConfig(ctx, layout, opts.Isolation, opts.OS)
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
		agent = AgentName(resolveAgentFromConfig(c.layout))
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
		Agent:   string(agent),
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

// Diff returns the workdir diff of agent changes for a sandbox.
// Equivalent to 'yoloai diff <name>'. Returns the diff text — an
// empty string means no changes (Q-U).
//
// For :overlay-mode workdirs, callers must use DiffOverlay; this
// helper short-circuits to "" + patch.ErrOverlayRequiresRuntime
// because the overlay diff path needs container exec.
func (c *Client) Diff(ctx context.Context, name string) (string, error) {
	return patch.GenerateDiff(ctx, patch.DiffOptions{Name: name, Layout: c.layout, Runtime: c.rt})
}

// Apply applies the agent's committed changes back to the original host
// workdir. Equivalent to 'yoloai apply <name>'.
// Uncommitted (work-in-progress) edits the agent left behind are NOT applied;
// use ApplyWithOptions to opt in.
// Returns (nil, nil) when there is nothing to apply — branch on
// result == nil rather than on a sentinel error (Q-P).
//
// Q-U: aux :copy / :overlay are no longer supported, so the result
// describes the single workdir patch (or nil for no-op).
func (c *Client) Apply(ctx context.Context, name string) (*patch.ApplyResult, error) {
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
// when there is nothing to apply — branch on result == nil rather
// than on a sentinel error (Q-P).
func (c *Client) ApplyWithOptions(ctx context.Context, name string, opts ApplyOptions) (*patch.ApplyResult, error) {
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

// Create provisions a new sandbox from CreateOptions and (unless
// opts.NoStart) starts the container with the agent. Returns the sandbox
// name on success — currently always opts.Name, since name is required
// (no auto-generation). Use Run for the higher-level "create + wait for
// terminal status" convenience.
func (c *Client) Create(ctx context.Context, opts sandbox.CreateOptions) (string, error) {
	return c.manager.Create(ctx, opts)
}

// ContainerLogs returns the tail of the named sandbox's container log,
// limited to roughly tailLines lines. Returns "" when the container is
// gone or the runtime can't fetch logs. Used by bug-report generation;
// also useful for embedders that want to surface backend errors without
// reaching for raw runtime access.
func (c *Client) ContainerLogs(ctx context.Context, name string, tailLines int) string {
	return c.rt.Logs(ctx, store.InstanceName(name), tailLines)
}

// DiffWithOptions generates the workdir diff with explicit filter
// options. paths narrows the diff to specific files; stat / nameOnly
// correspond to `git diff --stat` and `--name-only`. Returns the diff
// text — empty string means no changes (Q-U).
//
// :overlay workdirs short-circuit to "" + patch.ErrOverlayRequiresRuntime;
// use DiffOverlay for those.
func (c *Client) DiffWithOptions(ctx context.Context, name string, paths []string, stat, nameOnly bool) (string, error) {
	return patch.GenerateDiff(ctx, patch.DiffOptions{
		Name:     name,
		Layout:   c.layout,
		Paths:    paths,
		NameOnly: nameOnly,
		Stat:     stat,
		Runtime:  c.rt,
	})
}

// DiffOverlay generates the workdir diff for an :overlay-mode
// sandbox by running git inside the container. Returns the diff text
// (empty string for no changes). The container must be running.
func (c *Client) DiffOverlay(ctx context.Context, name string, stat, nameOnly bool) (string, error) {
	return patch.GenerateOverlayDiff(ctx, c.rt, patch.DiffOptions{
		Name:     name,
		Layout:   c.layout,
		Stat:     stat,
		NameOnly: nameOnly,
	})
}

// DiffRef generates the diff for a specific commit (or commit range)
// inside the sandbox's history. Disk-only. Returns the diff text —
// empty string means no changes.
func (c *Client) DiffRef(_ context.Context, name, ref string, stat bool) (string, error) {
	return patch.GenerateCommitDiff(patch.CommitDiffOptions{
		Name:   name,
		Layout: c.layout,
		Ref:    ref,
		Stat:   stat,
	})
}

// ListCommits returns the sandbox's commit history beyond baseline (one
// entry per commit since the work was started). Used by `yoloai diff --log`.
func (c *Client) ListCommits(ctx context.Context, name string) ([]patch.CommitInfo, error) {
	return patch.ListCommitsBeyondBaseline(ctx, c.layout, c.rt, name)
}

// ResolveCommitRefs resolves ref arguments (SHAs, "HEAD", ranges) to the
// concrete CommitInfo entries inside the sandbox's beyond-baseline
// history. Used by `yoloai apply <refs...>` for selective application.
func (c *Client) ResolveCommitRefs(ctx context.Context, name string, refs []string) ([]patch.CommitInfo, error) {
	return patch.ResolveRefs(ctx, c.layout, c.rt, name, refs)
}

// ListCommitsOverlay is the overlay-mode variant of ListCommits — runs
// git log inside the running container because the overlay'd workdir
// only exists there.
func (c *Client) ListCommitsOverlay(ctx context.Context, name string) ([]patch.CommitInfo, error) {
	return patch.ListCommitsBeyondBaselineOverlay(ctx, c.layout, c.rt, name)
}

// ListCommitsWithStats returns the same history as ListCommits but with
// per-commit `git diff --stat` summaries attached. Used by `yoloai diff
// --log --stat`.
func (c *Client) ListCommitsWithStats(ctx context.Context, name string) ([]patch.CommitInfoWithStat, error) {
	return patch.ListCommitsWithStats(ctx, c.layout, c.rt, name)
}

// HasUncommittedChanges reports whether the sandbox's workdir has any
// uncommitted (work-in-progress) edits beyond its last commit. Used by
// `yoloai diff --log` to surface a "*" marker.
func (c *Client) HasUncommittedChanges(ctx context.Context, name string) (bool, error) {
	return patch.HasUncommittedChanges(ctx, c.layout, c.rt, name)
}

// OverlayPatch generates patch sets for an :overlay sandbox's
// modified directories. Each PatchSet is one overlay'd directory's
// upper-layer diff, captured by running git diff inside the container.
// Used by `yoloai apply` for overlay sandboxes.
func (c *Client) OverlayPatch(ctx context.Context, name string, paths []string) ([]patch.PatchSet, error) {
	return patch.GenerateOverlayPatch(ctx, c.layout, c.rt, name, paths)
}

// UpdateOverlayBaseline advances an :overlay sandbox's baseline marker
// to HEAD for the named host path. Called after a successful apply so
// the next diff starts from a fresh baseline.
func (c *Client) UpdateOverlayBaseline(ctx context.Context, name, hostPath string) error {
	return patch.UpdateOverlayBaselineToHEAD(ctx, c.layout, c.rt, name, hostPath)
}

// AdvanceBaseline advances the sandbox's diff baseline past all
// applied commits. Called after a successful `yoloai apply` so the
// next diff starts fresh. No-op for path-filtered applies (callers
// should skip when paths are specified).
func (c *Client) AdvanceBaseline(ctx context.Context, name string) error {
	return patch.AdvanceBaseline(ctx, c.layout, c.rt, name)
}

// GeneratePatch produces a single squashed patch covering all
// committed (and optionally uncommitted) changes in the workdir.
// Returns (patchBytes, statSummary, err). Used by `yoloai apply --squash`.
func (c *Client) GeneratePatch(ctx context.Context, name string, paths []string, includeWIP bool) ([]byte, string, error) {
	return patch.GeneratePatch(ctx, c.layout, c.rt, name, paths, includeWIP)
}

// GenerateFormatPatch runs `git format-patch` in the sandbox over the
// beyond-baseline range and returns (patchDir, files, err). Caller is
// responsible for `os.RemoveAll(patchDir)` after consuming the files.
// Used by `yoloai apply` and `yoloai apply --patches`.
func (c *Client) GenerateFormatPatch(ctx context.Context, name string, paths []string) (patchDir string, files []string, err error) {
	return patch.GenerateFormatPatch(ctx, c.layout, c.rt, name, paths)
}

// GenerateFormatPatchForRefs is like GenerateFormatPatch but restricts
// output to the specified commit SHAs (selective apply). Caller must
// `os.RemoveAll(patchDir)` after use.
func (c *Client) GenerateFormatPatchForRefs(ctx context.Context, name string, shas, paths []string) (patchDir string, files []string, err error) {
	return patch.GenerateFormatPatchForRefs(ctx, c.layout, c.rt, name, shas, paths)
}

// GenerateWIPDiff produces the uncommitted-changes diff (work in
// progress) from the sandbox's workdir. Returns (patchBytes,
// statSummary, err). Used by `yoloai apply --include-wip` and
// `yoloai apply --patches --include-wip`.
func (c *Client) GenerateWIPDiff(ctx context.Context, name string, paths []string) ([]byte, string, error) {
	return patch.GenerateWIPDiff(ctx, c.layout, c.rt, name, paths)
}

// Start launches (or relaunches) the container for an existing sandbox.
// The sandbox must exist on disk; use Run to create a new sandbox.
func (c *Client) Start(ctx context.Context, name string, opts sandbox.StartOptions) error {
	return c.manager.Start(ctx, name, opts)
}

// Reset re-copies the workdir into the sandbox, resets the diff baseline, and
// (per opts) optionally restarts the container and wipes agent state. Use
// for "start over" workflows where the user wants to abandon the agent's
// current changes and resume from the original workdir.
func (c *Client) Reset(ctx context.Context, opts sandbox.ResetOptions) error {
	return c.manager.Reset(ctx, opts)
}

// IOStreams names the stdio handles for interactive Client methods.
// It's a type alias for runtime.IOStreams so embedders can use the
// yoloai.IOStreams name without importing runtime directly. See
// runtime.IOStreams for the field documentation.
type IOStreams = runtime.IOStreams

// Attach connects the supplied IOStreams to the sandbox's tmux session.
// Blocks until the user detaches (Ctrl-B d) or the agent exits.
//
// The sandbox must be running (StatusActive/Idle/Done/Failed). For
// stopped sandboxes call Start first. Use --resume on Start to relaunch
// the agent with the resume preamble before attaching.
//
// io.TTY=true is required; non-TTY attach returns a *UsageError. nil
// fields in io default to the calling process's os.Stdin/Stdout/Stderr;
// see IOStreams godoc for the current plumbing limitation.
func (c *Client) Attach(ctx context.Context, name string, io IOStreams) error {
	if !io.TTY {
		return sandbox.NewUsageError("attach requires TTY=true")
	}

	info, err := c.manager.Inspect(ctx, name)
	if err != nil {
		return err
	}
	if err := attachStatusOK(info.Status, name); err != nil {
		return err
	}

	containerName := store.InstanceName(name)
	user := sandbox.ContainerUser(info.Meta, c.layout.HostUID)

	if err := sandbox.WaitForAttachReady(ctx, c.rt, c.layout, name, user, 300*time.Second); err != nil {
		return fmt.Errorf("waiting for tmux session: %w", err)
	}

	sock := sandbox.ReadTmuxSocket(c.layout, name)
	cmd := c.rt.AttachCommand(sock, io.Rows, io.Cols, info.Meta.Isolation)
	return c.rt.InteractiveExec(ctx, containerName, cmd, user, "", io)
}

// Exec runs `cmd` inside the named sandbox's container interactively
// and connects the supplied IOStreams. The sandbox must be running
// (Active or Idle); other statuses return ErrContainerNotRunning. The
// user, working directory, and container name are derived from the
// sandbox's persisted metadata. Non-zero exit from the inner command
// surfaces as *exec.ExitError so callers can propagate the exit code.
func (c *Client) Exec(ctx context.Context, name string, cmd []string, io IOStreams) error {
	info, err := c.manager.Inspect(ctx, name)
	if err != nil {
		return err
	}
	if info.Status != sandbox.StatusActive && info.Status != sandbox.StatusIdle {
		return fmt.Errorf("sandbox %q: %w", name, sandbox.ErrContainerNotRunning)
	}
	containerName := store.InstanceName(name)
	user := sandbox.ContainerUser(info.Meta, c.layout.HostUID)
	return c.rt.InteractiveExec(ctx, containerName, cmd, user, info.Meta.Workdir.MountPath, io)
}

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

// EnsureSetup performs idempotent first-run setup: writes safe
// defaults (tmux_conf=default+host, setup_complete=true) plus the
// non-interactive layout scaffolding and base image build.
// Safe to call before any sandbox operation; a no-op once
// setup_complete is true. The interactive setup wizard is a separate
// flow — see SystemClient.SetupStatus / SystemClient.Setup.
func (c *Client) EnsureSetup(ctx context.Context) error {
	return c.manager.EnsureSetup(ctx)
}

// SendInput appends text to the running sandbox's tmux session as if
// the user had typed it. Used by the MCP server's sandbox_input tool
// to forward outer-agent messages into a running inner agent.
// Returns ErrContainerNotRunning when the sandbox is stopped.
func (c *Client) SendInput(ctx context.Context, name, text string) error {
	return c.manager.SendInput(ctx, name, text)
}

// StdioExec runs cmd inside the sandbox's container with raw stdio
// piped to the supplied stdin/stdout/stderr. Used by the MCP proxy
// to bridge an outer client's stdio to a server running in the
// sandbox. Returns *UsageError when the active backend doesn't
// implement runtime.StdioExecer (currently Tart and Seatbelt don't —
// only Docker, Podman, and containerd do).
//
// Unlike Exec/Attach, StdioExec does not allocate a PTY; it's the
// right shape for piping JSON-RPC or other line-oriented protocols.
func (c *Client) StdioExec(ctx context.Context, name string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	execer, ok := c.rt.(runtime.StdioExecer)
	if !ok {
		return sandbox.NewUsageError("backend %s does not support stdio exec", c.rt.Descriptor().Name)
	}
	containerName := store.InstanceName(name)
	return execer.StdioExec(ctx, containerName, cmd, stdin, stdout, stderr)
}

// SandboxDir returns the on-host directory that holds a sandbox's
// persisted state (meta.json, work copies, files/, cache/, agent log,
// prompt, etc.). Used by embedders that need to read or write files
// in that directory directly — e.g., the MCP server's
// sandbox_files_* tools resolve file paths under SandboxDir(name).
//
// The path is computed from c.layout's DataDir; it exists as soon as
// the sandbox has been created. Returns the path even for unknown
// names (callers must do their own existence check).
func (c *Client) SandboxDir(name string) string {
	return c.layout.SandboxDir(name)
}

// --- private helpers ---

// resolveBackendFromConfig picks the backend for a Client created without an
// explicit Backend in Options. Reads the user's container_backend preference
// from config and routes it through runtime.SelectBackend along with the
// caller's isolation/OS preferences — the same routing the CLI applies (F21).
// If the preferred container backend isn't available, SelectBackend falls back
// to any other registered container backend; the Client emits no warning of
// its own (embedders may want to suppress it), so we silently take the
// fallback verdict.
func resolveBackendFromConfig(ctx context.Context, layout config.Layout, isolation runtime.IsolationMode, targetOS string) runtime.BackendName {
	var preferred runtime.BackendName
	if cfg, err := config.LoadDefaultsConfig(layout); err == nil {
		preferred = runtime.BackendName(cfg.ContainerBackend)
	}
	backend, _ := runtime.SelectBackend(ctx, preferred, isolation, targetOS)
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
