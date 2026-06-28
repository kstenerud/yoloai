// ABOUTME: InjectorReach for seatbelt — the agent is a host process on the host
// ABOUTME: network stack (no netns), so it reaches a loopback-bound injector directly.
package seatbelt

import (
	"context"

	"github.com/kstenerud/yoloai/runtime"
)

var _ runtime.InjectorReachable = (*Runtime)(nil)

// InjectorReach reports how a seatbelt sandbox reaches a host-side injector. The
// agent runs as an ordinary macOS process under an SBPL profile — there is no
// network namespace and no VM, so it shares the host network stack. It therefore
// reaches a host-side listener on 127.0.0.1 directly, and the injector binds the
// same loopback address (never LAN-visible). BindHost == DialHost == 127.0.0.1.
//
// The real seatbelt profile emits an unrestricted "(allow network*)" whenever the
// network mode is not "none" (writeProfileNetwork), so no SBPL change is needed
// for the agent to reach the injector. Verified on the 2026-06-28 Mac spike: a
// sandbox-exec'd curl reaches a host 127.0.0.1 listener. Seatbelt rejects
// --network=isolated at the orchestration layer (BackendCaps.NetworkIsolation
// false), so there is no in-sandbox firewall to allowlist.
//
// This is a fixed host-loopback endpoint, knowable before any process is launched,
// so the broker can start the injector ahead of the agent on either launch path.
func (r *Runtime) InjectorReach(_ context.Context) (runtime.InjectorReach, error) {
	return runtime.InjectorReach{BindHost: "127.0.0.1", DialHost: "127.0.0.1"}, nil
}
