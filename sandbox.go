// ABOUTME: Sandbox is the per-sandbox handle returned by Client.Sandbox(name).
// ABOUTME: Provides scoped sub-handles (Workdir, Network, Agent, Files).

package yoloai

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/lifecycle"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// ErrSandboxDestroyed is returned by error-returning methods on a *Sandbox
// handle after Destroy has successfully run on that same handle. The handle is
// a name-binding, not a lease, so it is not nil'd on destroy; this sentinel
// turns a reuse-after-destroy into a precise, matchable refusal rather than the
// generic ErrSandboxNotFound the underlying read would otherwise produce.
var ErrSandboxDestroyed = yoerrors.NewUsageError("yoloai: this Sandbox handle was destroyed; obtain a fresh handle via Client.Sandbox after re-creating the sandbox")

// Sandbox is a name-scoped handle for a single sandbox. The handle is
// validated at construction (see Client.Sandbox), so methods on it can
// assume the sandbox exists.
//
// Q-G resolution (Shape B): name-bound handles group per-sandbox
// operations behind one accessor so the Client root stays uncluttered.
// Sub-handles (Workdir, Network, Agent, Files) are pure namespace expansion off
// a validated *Sandbox — no IO, no error.
type Sandbox struct {
	client *Client
	name   string
	// destroyed is set by a successful Destroy on this handle. Once set, every
	// error-returning method short-circuits with ErrSandboxDestroyed instead of
	// operating on a name whose backing state is gone. Pure path getters and the
	// sub-handle accessors (no error to return) are unaffected.
	destroyed bool
}

// checkNotDestroyed is the guard every error-returning method runs first: it
// refuses a handle whose sandbox this handle already destroyed.
func (s *Sandbox) checkNotDestroyed() error {
	if s.destroyed {
		return ErrSandboxDestroyed
	}
	return nil
}

// Name returns the sandbox name this handle is bound to. Useful for
// embedders threading the handle through multiple call sites.
func (s *Sandbox) Name() string { return s.name }

// Workdir returns the workdir sub-handle for diff/apply operations.
func (s *Sandbox) Workdir() *Workdir {
	return &Workdir{client: s.client, name: s.name}
}

// Agent returns the agent-interaction sub-handle for this sandbox.
func (s *Sandbox) Agent() *Agent { return &Agent{client: s.client, name: s.name} }

// Files returns a file-exchange handle for the sandbox.
func (s *Sandbox) Files() *Files {
	return &Files{client: s.client, name: s.name}
}

// Network returns the sandbox's network-management sub-handle.
func (s *Sandbox) Network() *Network {
	return &Network{client: s.client, name: s.name}
}

// Metadata reads the sandbox's creation-time environment straight from disk —
// no runtime connection, no live status query. This is the runtime-free,
// backend-agnostic read consumers need when only the captured configuration is
// wanted, not live state. For combined metadata + live status on a connected
// client, use Inspect instead.
func (s *Sandbox) Metadata() (*Environment, error) {
	if err := s.checkNotDestroyed(); err != nil {
		return nil, err
	}
	meta, err := store.LoadEnvironment(s.client.layout.SandboxDir(s.name))
	if err != nil {
		return nil, err
	}
	return environmentFromStore(meta), nil
}

// Inspect returns combined metadata and live state for the sandbox.
func (s *Sandbox) Inspect(ctx context.Context) (*SandboxInfo, error) {
	if err := s.checkNotDestroyed(); err != nil {
		return nil, err
	}
	if err := s.client.ensure(ctx); err != nil {
		return nil, err
	}
	si, err := s.client.engine.Inspect(ctx, s.name)
	if err != nil {
		return nil, err
	}
	return sandboxInfoFromStatus(si), nil
}

// Dir returns the on-host directory holding the sandbox's persisted state
// (environment.json, work copies, files/, cache/, logs, prompt). Computed from the
// Client's DataDir; returns a path even for unknown names (caller checks
// existence). Embedders that read/write sandbox files resolve paths under it.
func (s *Sandbox) Dir() string {
	return s.client.layout.SandboxDir(s.name)
}

// Stop stops the running container without destroying the sandbox.
func (s *Sandbox) Stop(ctx context.Context) error {
	if err := s.checkNotDestroyed(); err != nil {
		return err
	}
	if err := s.client.ensure(ctx); err != nil {
		return err
	}
	return s.client.engine.Stop(ctx, s.name)
}

// Clone copies this sandbox's state into a new sandbox named dest. Although the
// copy itself is a disk-only deep-copy of the source sandbox dir under
// DataDir/sandboxes/, Clone is backend-bound: it goes through the Engine (and,
// under opts.Overwrite, tears the destination down through the runtime), so a
// backend-less Client returns ErrBackendRequired. This matches real clone
// workflows, which almost always start the destination right after. Embedders
// wanting a pure offline copy should copy the sandbox dir themselves.
//
// With opts.Overwrite set, an existing destination is destroyed before the
// copy; without it, an existing destination is a hard error.
//
// The returned *Sandbox is dormant — the container is NOT started. Call
// Sandbox.Start to launch the agent on the clone.
func (s *Sandbox) Clone(ctx context.Context, dest string, opts SandboxCloneOptions) (*Sandbox, error) {
	if err := s.checkNotDestroyed(); err != nil {
		return nil, err
	}
	if err := s.client.ensure(ctx); err != nil {
		return nil, err
	}
	if opts.Overwrite {
		if err := s.client.engine.DestroyForOverwrite(ctx, dest); err != nil {
			return nil, err
		}
	}
	if err := s.client.engine.Clone(ctx, sandbox.CloneOptions{Source: s.name, Dest: dest}); err != nil {
		return nil, err
	}
	return &Sandbox{client: s.client, name: dest}, nil
}

// Start launches (or relaunches) the container for the existing sandbox.
// The sandbox must exist on disk; use Client.CreateSandbox for a new one.
func (s *Sandbox) Start(ctx context.Context, opts SandboxStartOptions) (*StartResult, error) {
	if err := s.checkNotDestroyed(); err != nil {
		return nil, err
	}
	if err := s.client.ensure(ctx); err != nil {
		return nil, err
	}
	return s.client.engine.Start(ctx, s.name, opts)
}

// Restart stops then starts the sandbox, applying opts on the way back up
// (e.g. StartOptions.Isolation to bring it up under a different isolation
// mode, StartOptions.Resume to re-feed the prompt).
func (s *Sandbox) Restart(ctx context.Context, opts SandboxStartOptions) (*StartResult, error) {
	if err := s.checkNotDestroyed(); err != nil {
		return nil, err
	}
	if err := s.client.ensure(ctx); err != nil {
		return nil, err
	}
	return s.client.engine.Restart(ctx, s.name, opts)
}

// waitPollInterval is how often Wait re-inspects the sandbox. A single
// Inspect is one backend status query plus a host-side status-file read (no
// container exec on the fast path), so a 1s cadence is cheap for a
// human-scale wait.
const waitPollInterval = 1 * time.Second

// ErrWaitTimeout is returned by Sandbox.Wait when SandboxWaitOptions.Timeout
// elapses before the requested condition is met. It wraps
// context.DeadlineExceeded, so errors.Is(err, context.DeadlineExceeded) also
// holds. Wait returns it alongside the last-observed SandboxInfo so callers can
// see where the sandbox stalled.
var ErrWaitTimeout = fmt.Errorf("sandbox wait timed out: %w", context.DeadlineExceeded)

// WaitCondition selects what state ends a Wait. Dead/terminal states (Done,
// Failed, Stopped, Removed, Suspended, Broken, Unavailable) always end the
// wait regardless of the condition — you never keep polling a sandbox that
// isn't running.
type WaitCondition int

const (
	// WaitForExit returns only when the agent session is over (Done / Failed /
	// Stopped, plus the always-terminal states). Idle does NOT satisfy it —
	// an agent awaiting input keeps the wait blocked. This is the zero value.
	WaitForExit WaitCondition = iota
	// WaitForIdle returns as soon as the agent stops actively working: Idle,
	// or any later terminal state. Use it for one-shot "fire a prompt, get the
	// response" flows that shouldn't block once the agent is awaiting input.
	WaitForIdle
)

// SandboxWaitOptions configures Sandbox.Wait.
type SandboxWaitOptions struct {
	// For is the condition that ends the wait. The zero value is WaitForExit.
	For WaitCondition
	// Timeout bounds the wait. Zero means no internal bound — the wait runs
	// until the condition is met or the passed ctx is cancelled. When set, a
	// timeout returns the last-observed SandboxInfo with ErrWaitTimeout.
	Timeout time.Duration
}

// Wait blocks until the sandbox reaches the condition in opts, polling its
// status once a second. It returns the SandboxInfo at the moment the condition
// is met. If opts.Timeout elapses first it returns the last-observed info with
// ErrWaitTimeout; if the passed ctx is cancelled it returns ctx.Err().
func (s *Sandbox) Wait(ctx context.Context, opts SandboxWaitOptions) (*SandboxInfo, error) {
	if err := s.checkNotDestroyed(); err != nil {
		return nil, err
	}
	if err := s.client.ensure(ctx); err != nil {
		return nil, err
	}
	return pollUntil(ctx, waitPollInterval, opts.Timeout, opts.For, func(ctx context.Context) (*SandboxInfo, error) {
		si, err := s.client.engine.Inspect(ctx, s.name)
		if err != nil {
			return nil, fmt.Errorf("inspect sandbox: %w", err)
		}
		return sandboxInfoFromStatus(si), nil
	})
}

// pollUntil drives the Wait loop independently of the backend: it calls poll
// every interval until poll's status satisfies cond. Factored out (with poll
// and interval injected) so the timing/timeout/cancel logic is unit-testable
// without a live engine. Returns (info, nil) when cond is met; (lastInfo,
// ErrWaitTimeout) when timeout > 0 elapses first; (nil, ctx.Err()) on cancel.
func pollUntil(ctx context.Context, interval, timeout time.Duration, cond WaitCondition, poll func(context.Context) (*SandboxInfo, error)) (*SandboxInfo, error) {
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timeoutCh = t.C
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		info, err := poll(ctx)
		if err != nil {
			return nil, err
		}
		if waitConditionMet(info.Status, cond) {
			return info, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeoutCh:
			return info, ErrWaitTimeout
		case <-ticker.C:
		}
	}
}

// waitConditionMet reports whether a status satisfies the wait condition.
// Active never satisfies it (agent still working); Idle satisfies only
// WaitForIdle; every other status is terminal and always satisfies it (so an
// unexpected/future status ends the wait rather than hanging forever).
func waitConditionMet(st Status, cond WaitCondition) bool {
	switch st {
	case StatusActive:
		return false
	case StatusIdle:
		return cond == WaitForIdle
	default:
		return true
	}
}

// Reset re-copies the workdir into the sandbox, resets the diff baseline, and
// (per opts) optionally restarts the container and wipes agent state. Use for
// "start over" workflows that abandon the agent's current changes.
func (s *Sandbox) Reset(ctx context.Context, opts SandboxResetOptions) (*ResetResult, error) {
	if err := s.checkNotDestroyed(); err != nil {
		return nil, err
	}
	if err := s.client.ensure(ctx); err != nil {
		return nil, err
	}
	return s.client.engine.Reset(ctx, opts.toInternal(s.name))
}

// HasActiveWork reports whether destroying the sandbox would lose work — a
// running agent, a dirty workdir, or unapplied commits — and a human-readable
// reason (empty when there's none). It's a pure query with no side effects;
// use it to pre-flight a batch of sandboxes before prompting once. For the
// single-sandbox case prefer Destroy's atomic typed refusal.
func (s *Sandbox) HasActiveWork(ctx context.Context) (bool, string) {
	s.client.tryEnsure(ctx) // best-effort: with no backend we still detect on-disk unapplied work
	return lifecycle.NeedsConfirmation(ctx, s.client.deps(), s.name)
}

// Destroy removes the sandbox and its container. With opts.AbandonUnappliedWork
// false it refuses a sandbox that HasActiveWork, returning a typed
// *ActiveWorkError carrying the reason — the caller prompts and retries with
// AbandonUnappliedWork true. Atomic: no check-then-act gap.
func (s *Sandbox) Destroy(ctx context.Context, opts SandboxDestroyOptions) (*DestroyResult, error) {
	if err := s.checkNotDestroyed(); err != nil {
		return nil, err
	}
	if err := s.client.ensure(ctx); err != nil {
		return nil, err
	}
	if !opts.AbandonUnappliedWork {
		if active, reason := s.HasActiveWork(ctx); active {
			return nil, yoerrors.NewActiveWorkError("%s", reason)
		}
	}
	res, err := s.client.engine.Destroy(ctx, s.name)
	if err != nil {
		return nil, err
	}
	s.destroyed = true
	return res, nil
}

// Exec runs opts.Command inside the sandbox's container. With opts.PTY true it
// allocates an interactive terminal (the sandbox must be Active or Idle).
// With opts.PTY false it pipes raw stdio via io.In/Out/Err (no PTY) — the right
// shape for line-oriented protocols like the MCP proxy's JSON-RPC bridge;
// returns *UsageError when the backend doesn't implement stdio exec
// (Tart/Seatbelt don't). A non-zero inner exit surfaces uniformly across
// backends as *ExecExitError carrying the inner command's status code.
func (s *Sandbox) Exec(ctx context.Context, opts SandboxExecOptions, io IOStreams) error {
	if err := s.checkNotDestroyed(); err != nil {
		return err
	}
	if err := s.client.ensure(ctx); err != nil {
		return err
	}
	if !opts.PTY {
		return execExitError(s.client.engine.StdioExec(ctx, s.name, opts.Command, io.In, io.Out, io.Err))
	}
	return execExitError(s.client.engine.InteractiveExec(ctx, s.name, opts.Command, io))
}

// execExitError translates the runtime's internal *runtime.ExecError (a
// non-zero inner exit) into the public *ExecExitError, so embedders match one
// public type regardless of backend. Any other error passes through unchanged.
func execExitError(err error) error {
	var ee *runtime.ExecError
	if errors.As(err, &ee) {
		return &yoerrors.ExecExitError{Code: ee.ExitCode}
	}
	return err
}

// ChangeState is the tri-state answer to "does this sandbox's workdir hold
// changes beyond its baseline" — carried on SandboxInfo.Changes. It is a string
// (not a bool) precisely because the question has three answers, not two: a
// sandbox whose state can't be read yet reports ChangesUnknown rather than a
// misleading "no".
type ChangeState string

const (
	ChangesPresent ChangeState = "yes" // workdir has changes beyond baseline
	ChangesAbsent  ChangeState = "no"  // workdir is unchanged from baseline
	ChangesUnknown ChangeState = "-"   // not yet determined / not applicable (e.g. a broken sandbox)
)

// SandboxInfo is the combined metadata + live state returned by Sandbox.Inspect /
// Client.ListSandboxes. Hand-written (not a type alias) so its Environment field is the public
// Environment read-model rather than the internal store.Environment — embedders can
// hold the full result without naming any internal type. Built from the
// internal status.Info at the library boundary via sandboxInfoFromStatus.
type SandboxInfo struct {
	Environment    *Environment `json:"environment"`
	Status         Status       `json:"status"`
	AgentStatus    AgentStatus  `json:"agent_status,omitempty"`
	Changes        ChangeState  `json:"has_changes"`
	DiskUsageBytes int64        `json:"disk_usage_bytes"`
}

// Status is a sandbox's lifecycle state. Re-exported (type alias) from
// internal/sandbox; the constants below are the closed set of values.
type Status = sandbox.Status

const (
	StatusActive      Status = sandbox.StatusActive      // container running, agent working
	StatusIdle        Status = sandbox.StatusIdle        // container running, agent awaiting input
	StatusDone        Status = sandbox.StatusDone        // agent exited cleanly (exit 0)
	StatusFailed      Status = sandbox.StatusFailed      // agent exited non-zero
	StatusStopped     Status = sandbox.StatusStopped     // container stopped
	StatusSuspended   Status = sandbox.StatusSuspended   // VM suspended (Tart only)
	StatusRemoved     Status = sandbox.StatusRemoved     // container removed, sandbox dir remains
	StatusBroken      Status = sandbox.StatusBroken      // sandbox dir exists but environment.json missing/invalid
	StatusUnavailable Status = sandbox.StatusUnavailable // backend not running
)

// AgentStatus is the agent's activity state inside a running sandbox, carried
// on SandboxInfo.AgentStatus. Re-exported (type alias) from internal/sandbox; the
// constants below are the closed set of values. Distinct from Status, which is
// the sandbox/container lifecycle state.
type AgentStatus = sandbox.AgentStatus

const (
	AgentStatusUnknown AgentStatus = sandbox.AgentStatusUnknown // not yet determined
	AgentStatusActive  AgentStatus = sandbox.AgentStatusActive  // actively working
	AgentStatusIdle    AgentStatus = sandbox.AgentStatusIdle    // awaiting input
	AgentStatusDone    AgentStatus = sandbox.AgentStatusDone    // completed its task
	AgentStatusFailed  AgentStatus = sandbox.AgentStatusFailed  // exited with an error
)

// SandboxStartOptions configures Sandbox.Start (and Restart). Re-exported (type alias)
// from internal/sandbox — its fields (Resume, Prompt, PromptFile, Isolation,
// VscodeTunnel) are all legitimate start-time knobs, so no field cleanup is
// needed.
type SandboxStartOptions = sandbox.StartOptions

// SandboxResetOptions configures Sandbox.Reset. Hand-written rather than aliased: the
// internal struct carries a Name field that the handle now supplies, so it's
// dropped here.
type SandboxResetOptions struct {
	RestartContainer bool // also stop+start the container after resetting (in-place by default)
	ClearState       bool // wipe the agent-runtime directory
	KeepCache        bool // preserve the cache directory
	KeepFiles        bool // preserve the files directory
	NoPrompt         bool // skip re-sending the prompt after reset
	// Prompt, when set, overwrites the sandbox's prompt.txt before resetting so
	// the new text is re-sent on restart. Empty leaves the existing prompt.
	Prompt string
	Debug  bool // enable entrypoint debug logging
}

func (o SandboxResetOptions) toInternal(name string) sandbox.ResetOptions {
	return sandbox.ResetOptions{
		Name:       name,
		Restart:    o.RestartContainer,
		ClearState: o.ClearState,
		KeepCache:  o.KeepCache,
		KeepFiles:  o.KeepFiles,
		NoPrompt:   o.NoPrompt,
		Prompt:     o.Prompt,
		Debug:      o.Debug,
	}
}

// SandboxDestroyOptions configures Sandbox.Destroy.
type SandboxDestroyOptions struct {
	// AbandonUnappliedWork proceeds even when the sandbox holds work that was
	// never applied to the host — a running agent, a dirty workdir, or unapplied
	// commits. With it false, Destroy refuses such a sandbox with a typed
	// *ActiveWorkError carrying the reason, so the caller can prompt and retry.
	// (The CLI's --force flag maps onto this field at the boundary.)
	AbandonUnappliedWork bool
}

// SandboxExecOptions configures Sandbox.Exec. PTY selects between an interactive
// terminal session (PTY true — allocates a remote pty) and raw stdio piping
// (PTY false — line-oriented, the shape the MCP proxy bridges JSON-RPC over).
type SandboxExecOptions struct {
	Command []string // command + args to run inside the container; required
	PTY     bool     // allocate a terminal (true) vs pipe raw stdio (false)
}

// CacheDir returns the host path of the sandbox's cache directory
// (<state>/cache). Like FilesDir, it is pure path computation with no backend
// contact.
func (s *Sandbox) CacheDir() string {
	return store.CacheDir(s.client.layout.SandboxDir(s.name))
}

// RuntimeConfigPath returns the host path of the sandbox's runtime-config.json
// (<state>/runtime-config.json), the entrypoint/infrastructure config the
// backend reads at launch. Pure path computation: no backend contact.
func (s *Sandbox) RuntimeConfigPath() string {
	return store.RuntimeConfigFilePath(s.client.layout.SandboxDir(s.name))
}

// EnvironmentPath returns the host path of the sandbox's environment.json
// (<state>/environment.json), the captured creation-time metadata. Pure path
// computation; the file need not exist.
func (s *Sandbox) EnvironmentPath() string {
	return filepath.Join(s.client.layout.SandboxDir(s.name), store.EnvironmentFile)
}

// LogPaths holds the host paths of a sandbox's diagnostic JSONL streams and the
// agent-status snapshot — the files the CLI tails and the bug-report bundle
// collects. Pure path computation; the files need not exist.
type LogPaths struct {
	CLI         string // <state>/logs/cli.jsonl
	Sandbox     string // <state>/logs/sandbox.jsonl
	Monitor     string // <state>/logs/monitor.jsonl
	Hooks       string // <state>/logs/agent-hooks.jsonl
	AgentStatus string // <state>/agent-status.json
}

// LogPaths returns the diagnostic file paths for the sandbox. No backend
// is contacted.
func (s *Sandbox) LogPaths() LogPaths {
	dir := s.client.layout.SandboxDir(s.name)
	return LogPaths{
		CLI:         store.CLIJSONLPath(dir),
		Sandbox:     store.SandboxJSONLPath(dir),
		Monitor:     store.MonitorJSONLPath(dir),
		Hooks:       store.HooksJSONLPath(dir),
		AgentStatus: store.AgentStatusFilePath(dir),
	}
}

// Unlock force-clears a stale lock file for the sandbox. It returns whether a
// lock was actually cleared (false means there was no lock file present) and
// surfaces a *UsageError when the recorded holder process is still alive. This
// is a host-filesystem operation and does not require a running backend.
func (s *Sandbox) Unlock() (cleared bool, err error) {
	if err := s.checkNotDestroyed(); err != nil {
		return false, err
	}
	return store.ForceUnlock(s.client.layout, s.name)
}

// VscodeAttach describes how to open a sandbox in VS Code via its
// attach-to-running-container support. Supported reports whether the sandbox's
// backend exposes a docker-compatible container surface; when false, the
// container fields and FolderURI are empty and the caller should fall back to a
// VS Code Remote Tunnel.
type VscodeAttach struct {
	BackendType   BackendType
	Supported     bool
	ContainerName string
	WorkdirPath   string
	FolderURI     string // vscode-remote://attached-container+<hex>... ; empty when unsupported
}

// VscodeAttach resolves the VS Code attach details for a sandbox. It reads the
// sandbox metadata and the backend's declared capabilities — no running backend
// is required.
func (s *Sandbox) VscodeAttach() (*VscodeAttach, error) {
	if err := s.checkNotDestroyed(); err != nil {
		return nil, err
	}
	sandboxDir := s.client.layout.SandboxDir(s.name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return nil, sandbox.ErrSandboxNotFound
	}
	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return nil, fmt.Errorf("load sandbox metadata: %w", err)
	}

	res := &VscodeAttach{BackendType: meta.BackendType}

	desc, ok := runtime.Descriptor(meta.BackendType)
	if !ok || !desc.Capabilities.ContainerAttach {
		return res, nil
	}

	res.Supported = true
	res.ContainerName = store.InstanceName(meta.Principal, meta.Name)
	res.WorkdirPath = meta.Workdir.MountPath

	payload, err := json.Marshal(map[string]string{"containerName": res.ContainerName})
	if err != nil {
		return nil, fmt.Errorf("marshal container payload: %w", err)
	}
	res.FolderURI = fmt.Sprintf("vscode-remote://attached-container+%s%s",
		hex.EncodeToString(payload), res.WorkdirPath)
	return res, nil
}
