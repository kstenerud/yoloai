// ABOUTME: Typed enum for backend names. Open-set: registry is the source of
// ABOUTME: truth; the constants document the shipped backends for parse-don't-validate.

package runtime

// BackendName names a runtime backend. Open-set typed string — the
// constants document the backends that ship with yoloai; future
// backends (registered via Register) supply their own name. The
// registry is the source of truth for which names are recognised at
// runtime.
//
// This type exists so the public Client API surface (added in
// W-L8b/c/d) can take a typed parameter rather than `string`,
// preventing typo-style bugs at call sites. Internal code that
// already uses plain string keys to look up backends keeps working;
// callers convert with `BackendName(s)` / `string(b)` at the
// boundary as they migrate.
//
// Established by W-L8a Q-Y.
type BackendName string

const (
	BackendDocker     BackendName = "docker"
	BackendPodman     BackendName = "podman"
	BackendTart       BackendName = "tart"
	BackendSeatbelt   BackendName = "seatbelt"
	BackendContainerd BackendName = "containerd"
)
