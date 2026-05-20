// Package runtime defines the pluggable Runtime interface for sandbox backends.
package runtime

// IsolationContainerRuntime returns the OCI runtime name for the given isolation
// mode, or "" for the backend default (standard runc).
func IsolationContainerRuntime(isolation string) string {
	switch isolation {
	case "container-enhanced":
		return "runsc"
	case "vm":
		return "io.containerd.kata.v2"
	case "vm-enhanced":
		return "io.containerd.kata-fc.v2"
	case "container-privileged":
		return "" // standard runc, no OCI runtime change
	default:
		return ""
	}
}

// IsolationSnapshotter returns the containerd snapshotter for the given isolation
// mode, or "" to use the backend default (overlayfs).
func IsolationSnapshotter(isolation string) string {
	if isolation == "vm-enhanced" {
		return "devmapper"
	}
	return ""
}

// IsolationEnforcesInSandboxIptables reports whether the OCI runtime used for
// the given isolation mode honors iptables rules applied inside the sandbox.
//
// Network isolation in yoloai is currently implemented by entrypoint.py
// installing iptables + ipset rules inside the running container. That only
// works when the in-sandbox kernel actually evaluates iptables.
//
//   - "container", "container-privileged", "" — standard runc; host kernel
//     enforces iptables in the container's netns. Works.
//   - "vm", "vm-enhanced" — Kata Containers; the guest Linux kernel inside
//     the VM enforces iptables exactly like bare metal. Works.
//   - "container-enhanced" — gVisor (runsc). gVisor implements its own
//     userspace netstack (the "Sentry") and does not evaluate iptables rules
//     installed inside the sandbox. Does NOT work — a sandbox configured
//     with --network-isolated would be wide open.
//
// The redesign in docs/design/network-isolation.md moves enforcement to the
// host netns, which removes the dependency on the in-sandbox kernel. Until
// that lands, the broken combination must be rejected at sandbox creation.
func IsolationEnforcesInSandboxIptables(isolation string) bool {
	return isolation != "container-enhanced"
}
