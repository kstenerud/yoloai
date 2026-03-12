// ABOUTME: Shared helpers for network allowlist management: allow, allowed, deny.
package cli

import (
	"context"
	"fmt"

	"github.com/kstenerud/yoloai/sandbox"
)

// ipsetResolveDomains is the shell script fragment that resolves domains to IPs
// and adds them to the ipset. Used by both add and remove live-patching.
const ipsetResolveDomains = `for domain in "$@"; do
  for ip in $(dig +short A "$domain" 2>/dev/null); do
    echo "$ip" | grep -qE "^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$" && \
      ipset add allowed-domains "$ip" 2>/dev/null || true
  done
done`

// loadIsolatedMeta loads sandbox metadata and validates that the sandbox uses
// network isolation. Returns the sandbox directory and metadata.
func loadIsolatedMeta(name string) (string, *sandbox.Meta, error) {
	sandboxDir, err := sandbox.RequireSandboxDir(name)
	if err != nil {
		return "", nil, err
	}
	meta, err := sandbox.LoadMeta(sandboxDir)
	if err != nil {
		return "", nil, err
	}

	switch meta.NetworkMode {
	case "isolated":
		// ok
	case "none":
		return "", nil, fmt.Errorf("sandbox %q uses --network-none; cannot modify network access", name)
	default:
		return "", nil, fmt.Errorf("sandbox %q is not using network isolation", name)
	}

	return sandboxDir, meta, nil
}

// saveNetworkAllowlist persists an updated allowlist to both environment.json and runtime-config.json.
func saveNetworkAllowlist(sandboxDir string, meta *sandbox.Meta) error {
	if err := sandbox.SaveMeta(sandboxDir, meta); err != nil {
		return err
	}
	return sandbox.PatchConfigAllowedDomains(sandboxDir, meta.NetworkAllow)
}

// tryLivePatchNetwork attempts to live-patch ipset rules in a running container.
// Creates a runtime connection only if needed. Returns (live, patchErr) where
// live is true if the patch was applied successfully, and patchErr is non-nil
// if the exec failed (runtime unavailable or container not running are not errors).
func tryLivePatchNetwork(ctx context.Context, backend, name, script string, scriptArgs []string) (bool, error) {
	rt, err := newRuntime(ctx, backend)
	if err != nil {
		return false, nil // Docker unavailable — not an error
	}
	defer rt.Close() //nolint:errcheck // best-effort cleanup

	info, err := sandbox.InspectSandbox(ctx, rt, name)
	if err != nil || (info.Status != sandbox.StatusActive && info.Status != sandbox.StatusIdle) {
		return false, nil // can't inspect or not running — skip
	}

	execArgs := []string{"sh", "-c", script, "_"}
	execArgs = append(execArgs, scriptArgs...)
	_, err = rt.Exec(ctx, sandbox.InstanceName(name), execArgs, "0")
	if err != nil {
		return false, err // exec failed — caller decides how to report
	}
	return true, nil
}
