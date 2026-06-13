// ABOUTME: Free functions Stop, Start, Reset, Destroy, and NeedsConfirmation:
// ABOUTME: the four core lifecycle verbs that drive sandbox containers through
// ABOUTME: their state transitions. Takes state.Deps instead of Engine receiver.
package lifecycle

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/sandbox/launch"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/store"
)

// Stop stops a sandbox's instance.
// Returns nil if the instance is already stopped or removed.
func Stop(ctx context.Context, d state.Deps, name string) error {
	unlock, err := store.AcquireLock(d.Layout, name)
	if err != nil {
		return err
	}
	defer unlock()
	return stop(ctx, d, name)
}

func stop(ctx context.Context, d state.Deps, name string) error {
	if err := store.RequireSandboxDir(d.Layout.SandboxDir(name)); err != nil {
		return err
	}
	slog.Info("stopping sandbox", "event", "sandbox.stop", "container", store.InstanceName(d.Layout.Principal, name))
	return d.Runtime.Stop(ctx, store.InstanceName(d.Layout.Principal, name))
}

// Destroy stops the container, removes it, and deletes the sandbox directory.
// Always succeeds — confirmation logic is handled by the CLI layer via
// NeedsConfirmation before calling this function.
func Destroy(ctx context.Context, d state.Deps, name string) (*DestroyResult, error) {
	unlock, err := store.AcquireLock(d.Layout, name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	res, derr := destroy(ctx, d, name)
	// Remove the per-sandbox lock file while we still hold the flock so
	// the <name>.lock file doesn't accumulate after the sandbox dir is
	// gone. Best-effort: a leftover lock file is harmless, not corruption.
	_ = store.RemoveLockFile(d.Layout, name)
	return res, derr
}

func destroy(ctx context.Context, d state.Deps, name string) (*DestroyResult, error) {
	warnings, err := launch.Teardown(ctx, d, name)
	if err != nil {
		return nil, err
	}
	var n notices
	for _, w := range warnings {
		n.warnf("%s", w)
	}
	return &DestroyResult{Notices: n.list}, nil
}

// syncLifecycleMarker checks for the Python on-create-done marker file and
// persists the flag to sandbox-state.json if found and not yet recorded.
func syncLifecycleMarker(sandboxDir string) {
	markerPath := filepath.Join(sandboxDir, "lifecycle-on-create-done")
	if _, markerErr := os.Stat(markerPath); markerErr != nil {
		return
	}
	state, stateErr := store.LoadSandboxState(sandboxDir)
	if stateErr == nil && !state.OnCreateCommandsDone {
		state.OnCreateCommandsDone = true
		if saveErr := store.SaveSandboxState(sandboxDir, state); saveErr != nil {
			slog.Warn("lifecycle: could not save sandbox state", "err", saveErr)
		}
	}
}

// resolveAgentArgs loads agent_args for the given agent from config and profile.
// Returns empty string if no args are configured.
func resolveAgentArgs(layout config.Layout, agentName, profileName string) string {
	cfg, err := config.LoadConfig(layout)
	if err != nil {
		return ""
	}
	if profileName != "" {
		chain, chainErr := config.ResolveProfileChain(layout, profileName)
		if chainErr == nil {
			merged, mergeErr := config.MergeProfileChain(layout, cfg, chain)
			if mergeErr == nil {
				return merged.AgentArgs[agentName]
			}
		}
	}
	return cfg.AgentArgs[agentName]
}
