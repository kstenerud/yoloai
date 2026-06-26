// ABOUTME: strategy.go defines the enforcement-strategy type and CanEnforce,
// ABOUTME: the capability model that decides whether a (backend, isolation-mode)
// ABOUTME: pair can actually enforce a network allowlist inside the sandbox.

package netpolicy

import (
	"fmt"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// Strategy names a mechanism for enforcing the network allowlist inside a sandbox.
type Strategy string

const (
	// StrategyIPFilter enforces the allowlist with iptables+ipset rules installed
	// inside the sandbox by entrypoint.py. This is the only strategy yoloai ships
	// today; it depends on the in-sandbox kernel honoring iptables (see CanEnforce).
	StrategyIPFilter Strategy = "ip-filter"

	// StrategyEgressProxy (future, not yet implemented) would route egress through a
	// host-side filtering proxy, removing the dependency on the in-sandbox kernel.
	// Reserved as a design seam — see docs/contributors/design/netpolicy.md.
	StrategyEgressProxy Strategy = "egress-proxy"
)

// CanEnforce reports whether `strategy` can actually enforce a network allowlist
// for a sandbox running on backend `backend` with isolation mode `isolation`.
// When it cannot, reason is a pre-formatted, user-facing sentence suitable for
// direct use in an error message; ok==true implies reason=="".
//
// For StrategyIPFilter this composes two runtime facts:
//   - caps.NetworkIsolation — the backend supports --network=isolated at all.
//   - runtime.IsolationEnforcesInSandboxIptables(isolation) — the OCI runtime for
//     this isolation mode honors iptables rules applied inside the sandbox. gVisor
//     (runsc, --isolation=container-enhanced) uses a userspace netstack that ignores
//     them, so in-sandbox enforcement would be a silent no-op; refuse rather than lie.
func CanEnforce(strategy Strategy, caps runtime.BackendCaps, backend runtime.BackendType, isolation runtime.IsolationMode) (ok bool, reason string) {
	switch strategy {
	case StrategyIPFilter:
		if !caps.NetworkIsolation {
			return false, fmt.Sprintf("--network=isolated is not supported by the %s backend", backend)
		}
		if !runtime.IsolationEnforcesInSandboxIptables(isolation) {
			return false, fmt.Sprintf(
				"--network=isolated cannot be enforced with --isolation=%s: "+
					"gVisor's userspace netstack ignores in-sandbox iptables rules. "+
					"Use --isolation=container (default) or a VM-based isolation mode "+
					"(--isolation=vm or --isolation=vm-enhanced) instead",
				isolation,
			)
		}
		return true, ""
	default:
		return false, fmt.Sprintf("network strategy %q is not implemented", strategy)
	}
}
