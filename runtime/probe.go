// ABOUTME: Backend availability probing and container-backend selection — the
// ABOUTME: registry-level glue that lets generic callers ask "is X usable?" or
// ABOUTME: "which container backend should I use?" without naming concrete packages.

package runtime

import (
	"context"
	"fmt"
	goruntime "runtime"
)

// DaemonEnvVars names the host-env keys that container-backend probes and
// clients consult for daemon-socket discovery — the union of what the Docker
// SDK config reads (context/TLS/host) and what Podman socket discovery reads.
// TMPDIR is in the set because `podman machine inspect` on macOS derives the
// machine API socket path from it ($TMPDIR/podman/...); without it podman
// reports the /tmp fallback path, which doesn't exist, and discovery fails.
// Callers curate their threaded snapshot to this set (Layout.CuratedEnv) before
// handing it to Probe / SelectBackend / SelectContainerBackend, so backend
// selection sees the daemon settings without the whole ambient env leaking in
// (§12). Curating to a superset is safe: each backend reads only its own keys,
// and CuratedEnv drops any key absent from the snapshot.
var DaemonEnvVars = []string{
	"DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_CONTEXT",
	"DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY", "DOCKER_API_VERSION",
	"CONTAINER_HOST", "XDG_RUNTIME_DIR", "HOME", "TMPDIR",
}

// Probe reports the named backend's availability tier on this host (Absent /
// Installed / Running) plus a reason when not Running. A backend not registered
// on this platform is ProbeAbsent; a registered backend with no descriptor Probe
// is treated as ProbeRunning (always usable, e.g. built-ins).
//
// Distinct from IsAvailable: IsAvailable is static — "compiled in for this
// platform" — while Probe is dynamic — installed (binary present) and/or running
// (daemon reachable) right now.
//
// env is the caller's threaded host-env snapshot, forwarded to the backend's
// probe so socket discovery stays principal-scoped (§12). May be nil.
func Probe(ctx context.Context, name BackendType, env map[string]string) (status ProbeStatus, reason string) {
	desc, ok := Descriptor(name)
	if !ok {
		return ProbeAbsent, fmt.Sprintf("backend %q is not available on this platform", name)
	}
	if desc.Probe == nil {
		return ProbeRunning, ""
	}
	return desc.Probe(ctx, env)
}

// Installed reports whether the named backend is at least installed (its tool is
// present, whether or not its daemon is running). This is the tier auto-pick
// selects on. reason is non-empty when not installed.
func Installed(ctx context.Context, name BackendType, env map[string]string) (installed bool, reason string) {
	status, r := Probe(ctx, name, env)
	return status >= ProbeInstalled, r
}

// Running reports whether the named backend is usable right now (daemon
// reachable). reason is non-empty when not running.
func Running(ctx context.Context, name BackendType, env map[string]string) (running bool, reason string) {
	status, r := Probe(ctx, name, env)
	return status == ProbeRunning, r
}

// SelectBackend resolves the backend to use from an isolation mode, a
// target OS, and a container-slot preference. It is the single source of
// truth for backend routing — both the CLI (which reads --isolation /
// --os flags + config) and library embedders (via yoloai.ClientConfiguration) call
// it, so the routing rules live in one place rather than being
// duplicated across cli/cliutil and the yoloai package (F21).
//
// Routing precedence:
//
//   - targetOS == "mac": macOS-native backends. isolation vm → tart
//     (full VM); anything else → seatbelt (lightweight sandbox-exec).
//     Checked first so "--os mac --isolation vm" picks tart, not
//     containerd.
//   - isolation vm / vm-enhanced: Linux VM isolation via containerd
//     (Kata). Falls through to container-slot selection when containerd
//     isn't available (e.g. a macOS host where containerd is Linux-only).
//   - otherwise (container / container-enhanced / default): the
//     container slot — preferred first, then any available container
//     backend (see SelectContainerBackend).
//
// isolation and targetOS may both be empty, in which case SelectBackend
// is equivalent to SelectContainerBackend(ctx, preferred): pure
// container-slot selection with no routing.
//
// The warning return mirrors SelectContainerBackend: non-empty when a
// container-slot preference was named but unavailable. OS/isolation
// routing itself emits no warning — the CLI validates those combos
// up-front via IsolationAvailability.
//
// env is the caller's threaded host-env snapshot, forwarded to container-slot
// probes for principal-scoped socket discovery (§12). May be nil.
func SelectBackend(ctx context.Context, preferred BackendType, isolation IsolationMode, targetOS string, env map[string]string) (backend BackendType, warning string) {
	// OS-based routing: macOS-native backends. Checked before isolation
	// so "--os mac --isolation vm" routes to tart rather than containerd.
	if targetOS == "mac" {
		if isolation == IsolationModeVM {
			return BackendTart, ""
		}
		return BackendSeatbelt, ""
	}

	// macOS host, Linux workload: the apple backend (fast per-container VM) is
	// the default — preferred over the container slot — when installed, and is
	// the macOS Linux-VM backend for `--isolation vm` (which would otherwise
	// degrade to the container slot, since containerd is Linux-only). An explicit
	// container_backend preference or `--isolation container` keeps the user in
	// the container slot. See plans/apple-container-backend.md.
	if goruntime.GOOS == "darwin" && darwinPrefersApple(ctx, preferred, isolation, env) {
		return BackendApple, ""
	}

	// microvm: Linux/KVM lightweight VM isolation via QEMU -M microvm.
	// Reached only via --isolation microvm; Linux-only with no container
	// fallback — IsolationAvailability rejects it off-Linux before we get
	// here, and on Linux a registered-but-unprovisioned backend surfaces its
	// own prerequisite error via RequiredCapabilities.
	if isolation == IsolationModeMicroVM {
		if IsAvailable(BackendMicroVM) {
			return BackendMicroVM, ""
		}
	}

	// Isolation-based routing: vm / vm-enhanced prefer containerd (Kata),
	// falling through to the container slot when containerd isn't
	// available on this host.
	if isolation == IsolationModeVM || isolation == IsolationModeVMEnhanced {
		if IsAvailable(BackendContainerd) {
			return BackendContainerd, ""
		}
	}

	return SelectContainerBackend(ctx, preferred, env)
}

// darwinPrefersApple reports whether a Linux workload on a macOS host should use
// the apple backend instead of the container slot. It only fires when apple is
// installed (the vm tier is cheap on macOS, so it's the default — but never
// forced onto a host that can't run it). Cases:
//   - container_backend=apple (explicit) → yes
//   - --isolation vm → yes (apple is the macOS Linux-VM backend)
//   - default isolation with no container preference → yes (the macOS default)
//   - --isolation container/-enhanced/-privileged, or an explicit container
//     preference → no (stay in the container slot)
func darwinPrefersApple(ctx context.Context, preferred BackendType, isolation IsolationMode, env map[string]string) bool {
	if installed, _ := Installed(ctx, BackendApple, env); !installed {
		return false
	}
	if preferred == BackendApple {
		return true
	}
	switch isolation {
	case IsolationModeVM:
		return true
	case IsolationModeDefault:
		return preferred == ""
	default:
		return false
	}
}

// SelectContainerBackend picks the best container backend (`BaseModeName ==
// "container"`) by the **installed** tier — the highest-priority backend whose
// tool is present, whether or not its daemon is running (point-of-use starts it
// on demand). It tries `preferred` first when non-empty and registered, then
// falls back to any other installed container backend, in alphabetical order.
//
// If a preferred backend is named but not installed, the returned warning string
// explains the fallback. If no container backend is installed at all, the
// returned name is the preferred one (or the first candidate alphabetically), so
// the caller fails downstream in `runtime.New` with a clear backend-specific
// error rather than a generic "no backend" message.
//
// env is the caller's threaded host-env snapshot, forwarded to each backend's
// probe so socket discovery stays principal-scoped (§12). May be nil.
func SelectContainerBackend(ctx context.Context, preferred BackendType, env map[string]string) (backend BackendType, warning string) {
	candidates := containerBackends()
	if len(candidates) == 0 {
		// No container backends registered on this platform (e.g. macOS without
		// docker/podman). The caller's next runtime.New() will surface the real
		// error; we just pick a stable name so the error path is deterministic.
		if preferred != "" {
			return preferred, ""
		}
		return BackendDocker, ""
	}

	// Move preferred to the front of the candidate list if it's a known
	// container backend. Otherwise the user typed a name that doesn't match
	// any container backend (e.g. "tart") — ignore the preference silently;
	// preference is for the docker/podman slot and "tart" isn't in it.
	ordered := orderCandidates(candidates, preferred)

	// Pick the first *installed* candidate (not "running" — an installed-but-
	// stopped backend is preferred over a lower-priority running one and is
	// started on demand at point-of-use).
	for _, name := range ordered {
		if ok, _ := Installed(ctx, name, env); ok {
			if preferred != "" && name != preferred {
				warning = fmt.Sprintf("Warning: container_backend=%s not available; falling back to %s", preferred, name)
			}
			return name, warning
		}
	}

	// Nothing available. Return the preferred (or first) candidate so the
	// caller's runtime.New fails with the backend-specific error message.
	if preferred != "" && contains(candidates, preferred) {
		return preferred, ""
	}
	return ordered[0], ""
}

// containerBackends returns the names of all registered backends whose
// BaseModeName is "container" (docker, podman; not containerd's vm mode).
func containerBackends() []BackendType {
	var out []BackendType
	for _, d := range Descriptors() {
		if d.BaseModeName == IsolationModeContainer {
			out = append(out, d.Type)
		}
	}
	return out
}

// orderCandidates returns candidates with preferred moved to the front when
// it's in the list. candidates is already sorted alphabetically by
// Descriptors(), preserved for the non-preferred tail.
func orderCandidates(candidates []BackendType, preferred BackendType) []BackendType {
	if preferred == "" || !contains(candidates, preferred) {
		return candidates
	}
	out := make([]BackendType, 0, len(candidates))
	out = append(out, preferred)
	for _, c := range candidates {
		if c != preferred {
			out = append(out, c)
		}
	}
	return out
}

func contains(s []BackendType, v BackendType) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
