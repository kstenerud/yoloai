// ABOUTME: Engine-level network-allowlist verbs — persist the allowlist and
// ABOUTME: live-patch the in-container ipset, so the Network sub-handle never
// ABOUTME: threads layout/runtime.

package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/netpolicycfg"
	"github.com/kstenerud/yoloai/internal/orchestrator/launch"
	"github.com/kstenerud/yoloai/internal/orchestrator/runtimeconfig"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
)

// SaveNetworkAllowlist persists the sandbox's netpolicy.json and the matching
// runtime-config allowed-domains patch. The caller mutates np.Allow then hands
// the whole Netpolicy here (D90: network policy lives in netpolicy.json, not
// the substrate environment.json).
func (e *Engine) SaveNetworkAllowlist(name string, np *netpolicycfg.Netpolicy) error {
	sandboxDir := e.layout.SandboxDir(name)
	if err := netpolicycfg.Save(sandboxDir, np); err != nil {
		return err
	}
	return PatchConfigAllowedDomains(sandboxDir, np.Allow)
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

	cname := store.InstanceName(e.layout.Principal, name)
	execArgs := []string{"sh", "-c", script, "_"}
	execArgs = append(execArgs, scriptArgs...)

	// A sidecar-firewalled sandbox has no CAP_NET_ADMIN inside the agent
	// container, so an in-container ipset patch would fail. Route the same script
	// through a netns-sharing sidecar that brings its own NET_ADMIN — it operates
	// on the per-netns `allowed-domains` ipset the launch sidecar created, so live
	// allow/deny stays live without granting the agent firewall control.
	if launch.UsesSidecarFirewall(rt, e.sandboxIsolation(name), "isolated") {
		if runner, ok := runtime.NetnsSidecarRunnerOf(rt); ok {
			if err := runner.RunNetnsSidecar(ctx, runtime.NetnsSidecarSpec{
				Target: cname,
				Argv:   execArgs,
				CapAdd: []string{"NET_ADMIN"},
			}); err != nil {
				return false, err
			}
			return true, nil
		}
	}

	if _, err := rt.Exec(ctx, cname, execArgs, "0"); err != nil {
		return false, err
	}
	return true, nil
}

// sandboxIsolation reads a sandbox's isolation mode from its runtime-config.json.
// Returns the empty (default) mode if the config can't be read, which keeps the
// caller on the in-container live-patch path rather than guessing a sidecar.
func (e *Engine) sandboxIsolation(name string) runtime.IsolationMode {
	data, err := os.ReadFile(filepath.Join(e.layout.SandboxDir(name), store.RuntimeConfigFile)) //nolint:gosec // sandbox-controlled path
	if err != nil {
		return ""
	}
	var cfg runtimeconfig.ContainerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.Isolation
}
