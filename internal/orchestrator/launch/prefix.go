// ABOUTME: Per-backend agent-launch command wrap. Launch-assembly knowledge that
// ABOUTME: lives here, not on the public runtime descriptor (the substrate is agent-free).
package launch

import "github.com/kstenerud/yoloai/runtime"

// agentLaunchPrefixes maps a backend type to the constant shell wrap prepended to
// the agent launch command for that backend. A backend with no entry needs no
// wrap (the common case — container backends launch the agent directly).
//
// This is launch-assembly knowledge — *how the orchestrator wraps the agent
// command* for a given backend — not a substrate fact, so it lives in the
// orchestrator's launch layer rather than on the public runtime.BackendDescriptor
// (the substrate stays agent-free; D97 / architecture-principles §4). The value is
// a compile-time constant keyed by backend type, host-derivable from the type
// alone (no Runtime instance, no backend binary), so the v1->v2 launch-prefix
// migration can recompute a stored sandbox's prefix on any host, cross-platform.
//
//   - Tart: prepend the provisioned tool dirs to PATH so the agent launches from
//     a non-login shell (`tart exec bash -c` does not source ~/.zprofile). Claude
//     Code is installed natively in ~/.local/bin; node@22 is keg-only at
//     /opt/homebrew/opt/node@22/bin. Mirrors the login PATH composed in the base
//     image's ~/.zprofile (see the tart backend's build.go provisionCommands).
//   - Seatbelt: source the generated swift wrapper (which auto-adds
//     --disable-sandbox for Swift PM commands) before the agent so the command
//     runs under the seatbelt profile.
var agentLaunchPrefixes = map[runtime.BackendType]string{
	runtime.BackendTart:     `PATH="$HOME/.local/bin:/opt/homebrew/opt/node@22/bin:/opt/homebrew/bin:$PATH" `,
	runtime.BackendSeatbelt: "source ~/.swift-wrapper.sh && ",
}

// AgentLaunchPrefix returns the agent-launch command wrap for a backend type, or
// "" for backends that need none. Used at create time (to persist the wrap into
// runtime-config.json) and by the launch-prefix migration's resolver.
func AgentLaunchPrefix(backendType runtime.BackendType) string {
	return agentLaunchPrefixes[backendType]
}
