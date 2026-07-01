// ABOUTME: Isolation-mode helpers mapping isolation strings to OCI runtime names,
// ABOUTME: containerd snapshotters, iptables enforcement, and OS/host availability.
// Package runtime defines the pluggable Runtime interface for sandbox backends.
package runtime

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kstenerud/yoloai/internal/sysexec"
)

// appleMinMacOS mirrors the apple backend's macOS gate (apple.minMacOSMajor),
// duplicated here to avoid a runtime→apple import cycle. macOS 26 is the first
// release that drops x86, so "macOS >= appleMinMacOS" already implies Apple
// Silicon — no separate architecture check is needed.
const appleMinMacOS = 26

// AppleVMHostSignals returns the two host facts the `--isolation vm`
// availability message needs on macOS: the host macOS major version (0 when
// undeterminable or not macOS) and whether the Apple `container` CLI is on PATH.
// Pure host checks (LookPath + sw_vers), no daemon dial — safe at CLI
// validation time.
func AppleVMHostSignals() (macOSMajor int, containerInstalled bool) {
	_, err := exec.LookPath("container")
	containerInstalled = err == nil
	out, verr := sysexec.Command(sysexec.Curated(nil, []string{"PATH"}, nil), "sw_vers", "-productVersion").Output()
	if verr == nil {
		s := strings.TrimSpace(string(out))
		if i := strings.IndexByte(s, '.'); i >= 0 {
			s = s[:i]
		}
		macOSMajor, _ = strconv.Atoi(s)
	}
	return macOSMajor, containerInstalled
}

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

// SupportsAgentFreeLaunch reports whether the D88 keepalive-holder + host-side
// Launch bring-up works under the given isolation mode. Returns false for
// "container-enhanced" (gVisor/runsc): that path brings the box up on a neutral
// keepalive holder and then launches sandbox-setup.py over it with a host-side
// `exec` as the "yoloai" user. Under gVisor, `docker exec --user <name>`
// resolves the username against the image's ORIGINAL /etc/passwd (snapshotted at
// container start) and ignores the entrypoint's runtime uid-remap — so the
// launched process runs as the stale image UID (e.g. 1001), which no longer owns
// the remapped /yoloai dirs (now the host UID). sandbox-setup.py's first write
// (its log redirect) hits EACCES, so it never runs and the agent never welds —
// silently, since the detached launch swallows the error. runc re-reads the live
// passwd, so it resolves correctly. gVisor therefore uses the legacy in-entrypoint
// weld, which drops to "yoloai" internally (in-container, against the live passwd).
// See docs/contributors/backend-idiosyncrasies.md (gVisor docker exec --user).
func SupportsAgentFreeLaunch(isolation IsolationMode) bool {
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
func IsolationAvailability(isolation IsolationMode, targetOS, hostOS string, hostMacOSMajor int, containerInstalled bool) (available bool, reason string, help string) {
	macAlternatives := "Available isolation modes with --os mac:\n" +
		"  container   macOS sandbox-exec (seatbelt)\n" +
		"  vm          Full macOS VM (Tart)"
	macVMFallbacks := "Use a Linux host for VM isolation, or use --os mac for macOS-native sandboxing:\n" +
		"  container   macOS sandbox-exec (seatbelt)\n" +
		"  vm          Full macOS VM (Tart)"

	// Cases are ordered by precedence: the first matching rule wins.
	switch {
	case hostOS == "darwin" && targetOS != "mac" && isolation == IsolationModeVM:
		// Apple `container` is the macOS Linux-VM backend.
		return appleVMAvailability(hostMacOSMajor, containerInstalled)

	case hostOS == "darwin" && targetOS != "mac" && isolation == IsolationModeVMEnhanced:
		// vm-enhanced (gVisor-in-VM) has no macOS backend — apple is a plain VM.
		return false,
			"--isolation vm-enhanced requires containerd, which is not available on macOS.",
			macVMFallbacks

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

// appleVMAvailability is the `--isolation vm` verdict on a macOS host, where
// Apple `container` is the Linux-VM backend. Available when installed; otherwise
// the message distinguishes "macOS too old" (upgrade) from "not installed".
func appleVMAvailability(hostMacOSMajor int, containerInstalled bool) (available bool, reason, help string) {
	macFallback := "  container   macOS sandbox-exec (seatbelt)\n  vm          Full macOS VM (Tart)"
	switch {
	case hostMacOSMajor > 0 && hostMacOSMajor < appleMinMacOS:
		return false,
			fmt.Sprintf("--isolation vm on macOS needs Apple `container`, which requires macOS %d or newer on Apple Silicon (this Mac runs macOS %d).", appleMinMacOS, hostMacOSMajor),
			fmt.Sprintf("Upgrade to macOS %d+ (Apple Silicon), use a Linux host, or use --os mac for macOS-native sandboxing:\n", appleMinMacOS) + macFallback
	case containerInstalled:
		return true, "", ""
	default:
		return false,
			"--isolation vm on macOS needs Apple `container`, which isn't installed.",
			"Install it from https://github.com/apple/container, then re-run — or use a Linux host, or --os mac for macOS-native sandboxing:\n" + macFallback
	}
}
