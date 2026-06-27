// ABOUTME: Engine lifecycle/create verbs (Start/Stop/Restart/Reset/Destroy/
// ABOUTME: Create/DestroyForOverwrite): self-ensuring wrappers over the
// ABOUTME: lifecycle/create free functions so callers stop assembling Deps.

package orchestrator

import (
	"context"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/orchestrator/create"
	"github.com/kstenerud/yoloai/internal/orchestrator/lifecycle"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// Start launches (or relaunches) the container for an existing sandbox. Opens
// the backend on first use; a backend-less Engine returns ErrBackendRequired.
func (e *Engine) Start(ctx context.Context, name string, opts StartOptions) (*StartResult, error) {
	if err := e.ensure(ctx); err != nil {
		return nil, err
	}
	return lifecycle.Start(ctx, e.deps(), name, opts)
}

// Stop stops the running container without destroying the sandbox.
func (e *Engine) Stop(ctx context.Context, name string) error {
	if err := e.ensure(ctx); err != nil {
		return err
	}
	return lifecycle.Stop(ctx, e.deps(), name)
}

// Restart stops then starts the sandbox under a single backend open, applying
// opts on the way back up.
func (e *Engine) Restart(ctx context.Context, name string, opts StartOptions) (*StartResult, error) {
	if err := e.ensure(ctx); err != nil {
		return nil, err
	}
	if err := lifecycle.Stop(ctx, e.deps(), name); err != nil {
		return nil, err
	}
	return lifecycle.Start(ctx, e.deps(), name, opts)
}

// Reset re-copies the workdir, resets the diff baseline, and (per opts)
// optionally restarts the container and wipes agent state.
func (e *Engine) Reset(ctx context.Context, opts ResetOptions) (*ResetResult, error) {
	if err := e.ensure(ctx); err != nil {
		return nil, err
	}
	return lifecycle.Reset(ctx, e.deps(), opts)
}

// Destroy removes the sandbox and its container. The active-work guard is the
// caller's policy (the library boundary turns it into a typed *ActiveWorkError);
// this is the unconditional teardown.
func (e *Engine) Destroy(ctx context.Context, name string) (*DestroyResult, error) {
	if err := e.ensure(ctx); err != nil {
		return nil, err
	}
	return lifecycle.Destroy(ctx, e.deps(), name)
}

// Create provisions a new dormant sandbox from create.Options and returns its
// name. The container is not started.
func (e *Engine) Create(ctx context.Context, opts create.Options) (string, error) {
	if err := e.ensure(ctx); err != nil {
		return "", err
	}
	return create.Run(ctx, e.deps(), opts)
}

// NeedsConfirmation reports whether destroying the sandbox would lose unapplied
// work — a dirty workdir or commits beyond the baseline — and a human-readable
// reason. The work signal is read through the backend's git context (in-VM for
// Tart, whose working copy never lives on the host), so it opens the runtime
// best-effort first; if the backend is unavailable or the VM is stopped the
// probe fails safe and reports the sandbox as needing confirmation.
func (e *Engine) NeedsConfirmation(ctx context.Context, name string) (bool, string) {
	e.TryEnsure(ctx)
	return lifecycle.NeedsConfirmation(ctx, e.deps(), name)
}

// DestroyForOverwrite tears down a pre-existing destination sandbox so a clone
// can take its place. A missing destination is a no-op. The destination may have
// been created on a different backend than this Engine's, so it destroys through
// the backend recorded in the destination's environment.json (falling back to
// this Engine's runtime when that metadata is unreadable). Work is abandoned
// unconditionally — an Overwrite clone is an explicit replace.
func (e *Engine) DestroyForOverwrite(ctx context.Context, dest string) error {
	if err := e.ensure(ctx); err != nil {
		return err
	}
	dstDir := e.layout.SandboxDir(dest)
	if _, err := os.Stat(dstDir); os.IsNotExist(err) {
		return nil
	}

	deps := e.deps()
	if meta, err := store.LoadEnvironment(dstDir); err == nil && meta.BackendType != "" {
		rt, rtErr := runtime.New(ctx, meta.BackendType, e.layout)
		if rtErr != nil {
			return fmt.Errorf("connect to %s backend to overwrite %q: %w", meta.BackendType, dest, rtErr)
		}
		defer rt.Close() //nolint:errcheck // best-effort close after teardown
		deps = state.Deps{Runtime: rt, Layout: e.layout, Input: e.input}
	}

	if _, err := lifecycle.Destroy(ctx, deps, dest); err != nil {
		return fmt.Errorf("overwrite existing destination %q: %w", dest, err)
	}
	return nil
}
