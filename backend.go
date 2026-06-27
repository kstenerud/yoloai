// ABOUTME: Package-level backend-selection functions — the public Backend
// ABOUTME: surface that isn't catalog metadata (discovery.go) or a report
// ABOUTME: (doctor_report.go). Backend has no handle; these resolve a concrete
// ABOUTME: BackendType at the embedder's boundary before constructing a Client.

package yoloai

import (
	"context"

	"github.com/kstenerud/yoloai/runtime"
)

// DaemonEnvVars names the host-env keys backend selection consults for
// daemon-socket discovery. Embedders curate their snapshot to this set
// (config.Layout.CuratedEnv) before passing it to SelectBackend /
// SelectContainerBackend, so probing sees the daemon settings without the whole
// ambient env leaking in (§12). See runtime.DaemonEnvVars for the rationale.
var DaemonEnvVars = runtime.DaemonEnvVars

// SelectBackend resolves a concrete backend from a preferred backend plus
// isolation / OS routing preferences, mirroring what the CLI does for its
// --backend / --isolation / --os flags. It probes which container daemons are
// installed and falls back accordingly, returning the chosen backend and a
// human-readable warning ("" when none).
//
// Backend selection is inherently ambient (it probes which container daemons
// are installed), so it belongs at the outermost boundary, not hidden inside
// Client construction (§4 / §12). Embedders that want the CLI's auto-detection
// call this at their boundary and pass the result as
// ClientCreateOptions.BackendType; those that leave BackendType empty get a
// backend-less Client (host-only reads + admin).
//
// env is the caller's host-env snapshot (the same map passed as ClientCreateOptions.Env):
// container-slot probes read DOCKER_HOST / CONTAINER_HOST / XDG_RUNTIME_DIR
// from it rather than the process environment, so selection stays
// principal-scoped (§12). May be nil to probe default socket paths only.
func SelectBackend(ctx context.Context, preferred BackendType, isolation IsolationMode, targetOS string, env map[string]string) (BackendType, string) {
	return runtime.SelectBackend(ctx, preferred, isolation, targetOS, env)
}

// SelectContainerBackend resolves a concrete container backend from a preferred
// backend, probing which container daemons are installed and falling back
// accordingly. It is the container-only counterpart to SelectBackend (no
// isolation/OS routing), mirroring what lifecycle commands do when resolving a
// backend for an existing sandbox. Returns the chosen backend and a
// human-readable warning ("" when none).
//
// env is the caller's host-env snapshot (the same map passed as ClientCreateOptions.Env);
// see SelectBackend. May be nil to probe default socket paths only.
func SelectContainerBackend(ctx context.Context, preferred BackendType, env map[string]string) (BackendType, string) {
	return runtime.SelectContainerBackend(ctx, preferred, env)
}

// IsolationAvailability reports whether the given isolation mode is usable for a
// target OS on the given host OS, returning a human-readable reason and a
// remediation hint when it is not. Embedders validate a requested isolation
// mode at their boundary before constructing a Client (the CLI does this for
// its --isolation / --os flags). hostMacOSMajor and containerInstalled describe
// the host's Apple-`container` situation (see AppleVMHostSignals) so the
// `--isolation vm` message can distinguish "not installed" from "macOS too old".
func IsolationAvailability(isolation IsolationMode, targetOS, hostOS string, hostMacOSMajor int, containerInstalled bool) (available bool, reason, help string) {
	return runtime.IsolationAvailability(isolation, targetOS, hostOS, hostMacOSMajor, containerInstalled)
}

// AppleVMHostSignals returns the host macOS major version and whether the Apple
// `container` CLI is installed — the inputs IsolationAvailability needs to craft
// the `--isolation vm` message on macOS. Re-exported from runtime.
func AppleVMHostSignals() (macOSMajor int, containerInstalled bool) {
	return runtime.AppleVMHostSignals()
}

// Container-system alias ids: user-facing names for the docker backend pinned to
// a specific local daemon socket. See ResolveContainerSystem.
const (
	ContainerSystemOrbstack      = runtime.ContainerSystemOrbstack
	ContainerSystemDockerDesktop = runtime.ContainerSystemDockerDesktop
)

// ResolveContainerSystem translates a user-facing container-system id (e.g.
// orbstack, docker-desktop) into the concrete (backend, DOCKER_HOST) pair to
// use: the docker-VM aliases resolve to the docker backend with a pinned unix
// socket so an explicit pick reaches that exact daemon; every other id passes
// through with an empty dockerHost. Embedders and the CLI resolve a backend
// preference through this before selection so an aliased preference both routes
// to docker and carries its socket pin. Re-exported from runtime.
func ResolveContainerSystem(id BackendType, homeDir string) (backend BackendType, dockerHost string) {
	return runtime.ResolveContainerSystem(id, homeDir)
}

// IsContainerSystemAlias reports whether id is a docker-VM alias (orbstack,
// docker-desktop) rather than a registered backend.
func IsContainerSystemAlias(id BackendType) bool {
	return runtime.IsContainerSystemAlias(id)
}

// ContainerSystems returns the docker-VM alias ids in display order.
func ContainerSystems() []BackendType {
	return runtime.ContainerSystems()
}

// ContainerSystemLabel returns the human-facing product name for an alias id, or
// the raw id when it is not an alias.
func ContainerSystemLabel(id BackendType) string {
	return runtime.ContainerSystemLabel(id)
}

// ContainerSystemSocket returns the pinned unix-socket DOCKER_HOST for an alias
// id under homeDir, or "" when id is not an alias or homeDir is empty.
func ContainerSystemSocket(id BackendType, homeDir string) string {
	return runtime.ContainerSystemSocket(id, homeDir)
}
