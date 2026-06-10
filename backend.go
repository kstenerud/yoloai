// ABOUTME: Package-level backend-selection functions — the public Backend
// ABOUTME: surface that isn't catalog metadata (discovery.go) or a report
// ABOUTME: (doctor_report.go). Backend has no handle; these resolve a concrete
// ABOUTME: BackendType at the embedder's boundary before constructing a Client.

package yoloai

import (
	"context"

	"github.com/kstenerud/yoloai/internal/runtime"
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
// its --isolation / --os flags).
func IsolationAvailability(isolation IsolationMode, targetOS, hostOS string) (available bool, reason, help string) {
	return runtime.IsolationAvailability(isolation, targetOS, hostOS)
}
