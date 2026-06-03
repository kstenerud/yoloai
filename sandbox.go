// ABOUTME: Sandbox is the per-sandbox handle returned by Client.Sandbox(name).
// ABOUTME: Provides scoped sub-handles (Workdir, Network, Agent).

package yoloai

import (
	"context"
	"fmt"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/lifecycle"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// Sandbox is a name-scoped handle for a single sandbox. The handle is
// validated at construction (see Client.Sandbox), so methods on it can
// assume the sandbox exists.
//
// Q-G resolution (Shape B): name-bound handles group per-sandbox
// operations behind one accessor so the Client root stays uncluttered.
// Sub-handles (Workdir, Network, Agent) are pure namespace expansion off
// a validated *Sandbox — no IO, no error.
type Sandbox struct {
	c    *Client
	name string
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

// Name returns the sandbox name this handle is bound to. Useful for
// embedders threading the handle through multiple call sites.
func (s *Sandbox) Name() string { return s.name }

// Metadata reads the sandbox's creation-time environment straight from disk —
// no runtime connection, no live status query. This is the runtime-free,
// backend-agnostic read consumers need when only the captured configuration is
// wanted, not live state. For combined metadata + live status on a connected
// client, use Inspect instead.
func (s *Sandbox) Metadata() (*Environment, error) {
	meta, err := store.LoadEnvironment(s.c.layout.SandboxDir(s.name))
	if err != nil {
		return nil, err
	}
	return environmentFromStore(meta), nil
}

// Inspect returns combined metadata and live state for the sandbox.
func (s *Sandbox) Inspect(ctx context.Context) (*Info, error) {
	if err := s.c.ensure(ctx); err != nil {
		return nil, err
	}
	si, err := s.c.manager.Inspect(ctx, s.name)
	if err != nil {
		return nil, err
	}
	return infoFromStatus(si), nil
}

// Dir returns the on-host directory holding the sandbox's persisted state
// (environment.json, work copies, files/, cache/, logs, prompt). Computed from the
// Client's DataDir; returns a path even for unknown names (caller checks
// existence). Embedders that read/write sandbox files resolve paths under it.
func (s *Sandbox) Dir() string {
	return s.c.layout.SandboxDir(s.name)
}

// Stop stops the running container without destroying the sandbox.
func (s *Sandbox) Stop(ctx context.Context) error {
	if err := s.c.ensure(ctx); err != nil {
		return err
	}
	return lifecycle.Stop(ctx, s.c.deps(), s.name)
}

// Start launches (or relaunches) the container for the existing sandbox.
// The sandbox must exist on disk; use Client.Run/Create for a new one.
func (s *Sandbox) Start(ctx context.Context, opts StartOptions) (*StartResult, error) {
	if err := s.c.ensure(ctx); err != nil {
		return nil, err
	}
	return lifecycle.Start(ctx, s.c.deps(), s.name, opts)
}

// Restart stops then starts the sandbox, applying opts on the way back up
// (e.g. StartOptions.Isolation to bring it up under a different isolation
// mode, StartOptions.Resume to re-feed the prompt).
func (s *Sandbox) Restart(ctx context.Context, opts StartOptions) (*StartResult, error) {
	if err := s.c.ensure(ctx); err != nil {
		return nil, err
	}
	if err := lifecycle.Stop(ctx, s.c.deps(), s.name); err != nil {
		return nil, err
	}
	return lifecycle.Start(ctx, s.c.deps(), s.name, opts)
}

// Reset re-copies the workdir into the sandbox, resets the diff baseline, and
// (per opts) optionally restarts the container and wipes agent state. Use for
// "start over" workflows that abandon the agent's current changes.
func (s *Sandbox) Reset(ctx context.Context, opts ResetOptions) (*ResetResult, error) {
	if err := s.c.ensure(ctx); err != nil {
		return nil, err
	}
	return lifecycle.Reset(ctx, s.c.deps(), opts.toInternal(s.name))
}

// HasActiveWork reports whether destroying the sandbox would lose work — a
// running agent, a dirty workdir, or unapplied commits — and a human-readable
// reason (empty when there's none). It's a pure query with no side effects;
// use it to pre-flight a batch of sandboxes before prompting once. For the
// single-sandbox case prefer Destroy's atomic typed refusal.
func (s *Sandbox) HasActiveWork(ctx context.Context) (bool, string) {
	s.c.tryEnsure(ctx) // best-effort: with no backend we still detect on-disk unapplied work
	return lifecycle.NeedsConfirmation(ctx, s.c.deps(), s.name)
}

// Destroy removes the sandbox and its container. With opts.AbandonUnappliedWork
// false it refuses a sandbox that HasActiveWork, returning a typed
// *ActiveWorkError carrying the reason — the caller prompts and retries with
// AbandonUnappliedWork true. Atomic: no check-then-act gap.
func (s *Sandbox) Destroy(ctx context.Context, opts DestroyOptions) (*DestroyResult, error) {
	if err := s.c.ensure(ctx); err != nil {
		return nil, err
	}
	if !opts.AbandonUnappliedWork {
		if active, reason := s.HasActiveWork(ctx); active {
			return nil, yoerrors.NewActiveWorkError("%s", reason)
		}
	}
	return lifecycle.Destroy(ctx, s.c.deps(), s.name)
}

// Exec runs opts.Command inside the sandbox's container. With opts.PTY true it
// allocates an interactive terminal (the sandbox must be Active or Idle);
// non-zero inner exit surfaces as *exec.ExitError. With opts.PTY false it pipes
// raw stdio via io.In/Out/Err (no PTY) — the right shape for line-oriented
// protocols like the MCP proxy's JSON-RPC bridge; returns *UsageError when the
// backend doesn't implement stdio exec (Tart/Seatbelt don't).
func (s *Sandbox) Exec(ctx context.Context, opts ExecOptions, io IOStreams) error {
	if err := s.c.ensure(ctx); err != nil {
		return err
	}
	if !opts.PTY {
		execer, ok := s.c.rt.(runtime.StdioExecer)
		if !ok {
			return yoerrors.NewUsageError("backend %s does not support stdio exec", s.c.rt.Descriptor().Name)
		}
		return execer.StdioExec(ctx, store.InstanceName(s.c.layout.Principal, s.name), opts.Command, io.In, io.Out, io.Err)
	}
	info, err := s.c.manager.Inspect(ctx, s.name)
	if err != nil {
		return err
	}
	if info.Status != sandbox.StatusActive && info.Status != sandbox.StatusIdle {
		return fmt.Errorf("sandbox %q: %w", s.name, sandbox.ErrContainerNotRunning)
	}
	user := sandbox.ContainerUser(info.Environment, s.c.layout.HostUID)
	return s.c.rt.InteractiveExec(ctx, store.InstanceName(s.c.layout.Principal, s.name), opts.Command, user, info.Environment.Workdir.MountPath, io)
}
