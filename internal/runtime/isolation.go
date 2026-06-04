// ABOUTME: Isolation-mode helpers mapping isolation strings to OCI runtime names,
// ABOUTME: containerd snapshotters, iptables enforcement, and OS/host availability.
// Package runtime defines the pluggable Runtime interface for sandbox backends.
package runtime

import "fmt"

// IsolationContainerRuntime returns the OCI runtime name for the given isolation
// mode, or "" for the backend default (standard runc).
func IsolationContainerRuntime(isolation IsolationMode) string {
	switch isolation {
	case IsolationModeContainerEnhanced:
		return "runsc"
	case IsolationModeVM:
		return "io.containerd.kata.v2"
	case IsolationModeVMEnhanced:
		return "io.containerd.kata-fc.v2"
	case IsolationModeContainerPrivileged:
		return "" // standard runc, no OCI runtime change
	case IsolationModeDefault, IsolationModeContainer, IsolationModeProcess:
		return ""
	default:
		return ""
	}
}

// IsolationSnapshotter returns the containerd snapshotter for the given isolation
// mode, or "" to use the backend default (overlayfs).
func IsolationSnapshotter(isolation IsolationMode) string {
	if isolation == IsolationModeVMEnhanced {
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
// The redesign in docs/contributors/design/network-isolation.md moves enforcement to the
// host netns, which removes the dependency on the in-sandbox kernel. Until
// that lands, the broken combination must be rejected at sandbox creation.
func IsolationEnforcesInSandboxIptables(isolation IsolationMode) bool {
	return isolation != IsolationModeContainerEnhanced
}

// SupportsOverlayDirs reports whether the given isolation mode is compatible
// with :overlay-mounted directories. Returns false for "container-enhanced"
// (gVisor; runsc does not implement the in-container overlayfs(2) mount that
// yoloai's entrypoint installs). All other modes (container,
// container-privileged, vm, vm-enhanced) run a Linux kernel with overlayfs
// support — though the backend itself must additionally declare
// BackendCaps.OverlayDirs for the feature to actually be enabled.
func SupportsOverlayDirs(isolation IsolationMode) bool {
	return isolation != IsolationModeContainerEnhanced
}

// IsolationAvailability reports whether the given isolation mode is available
// on the host/target-OS combination. Returns available=true with empty
// reason/help when supported. Otherwise reason is a single user-facing
// sentence explaining why, and help is optional follow-up text (suggested
// alternatives, issue links). Both strings are pre-formatted for direct use
// in an error message.
//
// hostOS is runtime.GOOS-style ("darwin", "linux", "windows"); targetOS is
// the --os flag value ("mac", "linux", or "").
func IsolationAvailability(isolation IsolationMode, targetOS, hostOS string) (available bool, reason string, help string) {
	macAlternatives := "Available isolation modes with --os mac:\n" +
		"  container   macOS sandbox-exec (seatbelt)\n" +
		"  vm          Full macOS VM (Tart)"

	// Cases are ordered by precedence: the first matching rule wins.
	switch {
	case hostOS == "darwin" && targetOS != "mac" && (isolation == IsolationModeVM || isolation == IsolationModeVMEnhanced):
		return false,
			fmt.Sprintf("--isolation %s requires containerd, which is not available on macOS.", isolation),
			"Use a Linux host for VM isolation, or use --os mac for macOS-native sandboxing:\n" +
				"  container   macOS sandbox-exec (seatbelt)\n" +
				"  vm          Full macOS VM (Tart)"

	case targetOS == "mac" && (isolation == IsolationModeContainerEnhanced || isolation == IsolationModeVMEnhanced):
		return false,
			fmt.Sprintf("--isolation %s is not available with --os mac.", isolation),
			macAlternatives

	case isolation == IsolationModeContainerEnhanced && targetOS != "mac" && hostOS == "darwin":
		return false,
			"--isolation container-enhanced (gVisor) is not supported on macOS due to a bug\n" +
				"that causes Claude Code to hang indefinitely during initialization.",
			"Workaround: Omit --isolation (use default container isolation) or use\n" +
				"--os mac for lightweight macOS sandboxing.\n\n" +
				"For details, see: https://github.com/anthropics/claude-code/issues/35454"

	case isolation == IsolationModeContainerPrivileged && hostOS == "darwin":
		return false,
			fmt.Sprintf("--isolation %s is Linux-only (Docker or Podman required).", isolation),
			"macOS backends (Seatbelt, Tart) do not support this mode.\n" +
				"Use a Linux host or omit --isolation for the default mode."

	case isolation == IsolationModeContainerPrivileged && targetOS == "mac":
		return false,
			fmt.Sprintf("--isolation %s is not available with --os mac.", isolation),
			macAlternatives
	}

	return true, "", ""
}
