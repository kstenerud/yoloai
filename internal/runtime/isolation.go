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

	case hostOS == "darwin" && isolation == IsolationModeContainerEnhanced:
		// gVisor (runsc) is not supported on a macOS host. The Docker daemon runs
		// inside a Linux VM (Docker Desktop / OrbStack / Podman Machine), and none
		// of them can run runsc turn-key: Docker Desktop's engine fails when runsc
		// is registered, OrbStack's /tmp→/private/tmp virtiofs symlink breaks
		// runsc's chroot, and there's a nested cgroup-v2 hazard on top. gVisor is
		// Linux-primary; see docs/contributors/design/plans/setup-gvisor.md (D71).
		return false,
			"--isolation container-enhanced (gVisor) is not supported on macOS.",
			"gVisor requires a Linux host. The macOS Docker VMs can't run runsc without\n" +
				"manual, unsupported setup. Use a Linux host for gVisor isolation, or on macOS:\n" +
				"  container             container sandbox (runc)\n" +
				"  container-privileged  privileged container (docker-in-docker, etc.)"

	case isolation == IsolationModeContainerPrivileged && targetOS == "mac":
		// Only the macOS-native target is unsupported: seatbelt/tart have no
		// privileged mode. A darwin *host* is fine — Docker Desktop / OrbStack /
		// Podman Machine run --privileged inside their Linux VM, so the container
		// backends accept it there exactly as on Linux. Backends that genuinely
		// can't (seatbelt/tart) are rejected by their SupportedIsolationModes.
		return false,
			fmt.Sprintf("--isolation %s is not available with --os mac.", isolation),
			macAlternatives
	}

	return true, "", ""
}
