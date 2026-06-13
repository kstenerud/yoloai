// ABOUTME: Engine-level network-allowlist verbs — persist the allowlist and
// ABOUTME: live-patch the in-container ipset, so the Network sub-handle never
// ABOUTME: threads layout/runtime.

package orchestrator

import (
	"context"

	"github.com/kstenerud/yoloai/internal/store"
)

// SaveNetworkAllowlist persists the sandbox's environment.json and the matching
// runtime-config allowed-domains patch. The caller mutates meta.NetworkAllow
// then hands the whole meta here.
func (e *Engine) SaveNetworkAllowlist(name string, meta *store.Environment) error {
	sandboxDir := e.layout.SandboxDir(name)
	if err := store.SaveEnvironment(sandboxDir, meta); err != nil {
		return err
	}
	return PatchConfigAllowedDomains(sandboxDir, meta.NetworkAllow)
}

// LivePatchNetwork best-effort execs a shell script inside the running sandbox
// container to live-update ipset rules. Returns (live, err) where live is true
// iff the exec succeeded. Soft-fails (backend-less Engine, sandbox not running,
// runtime unreachable) return (false, nil) so the caller treats them like a
// successful no-op — the on-disk allowlist is the source of truth and the change
// is queued for the next start.
func (e *Engine) LivePatchNetwork(ctx context.Context, name, script string, scriptArgs []string) (bool, error) {
	e.TryEnsure(ctx)
	rt := e.Runtime()
	if rt == nil {
		return false, nil
	}

	info, err := e.Inspect(ctx, name)
	if err != nil {
		return false, nil //nolint:nilerr // soft-fail: not running, can't live-patch
	}
	if info.Status != StatusActive && info.Status != StatusIdle {
		return false, nil
	}

	execArgs := []string{"sh", "-c", script, "_"}
	execArgs = append(execArgs, scriptArgs...)
	if _, err := rt.Exec(ctx, store.InstanceName(e.layout.Principal, name), execArgs, "0"); err != nil {
		return false, err
	}
	return true, nil
}
