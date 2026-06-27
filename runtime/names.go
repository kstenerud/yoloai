// ABOUTME: Typed enum for backend names. Open-set: registry is the source of
// ABOUTME: truth; the constants document the shipped backends for parse-don't-validate.

package runtime

// BackendType names a runtime backend. Open-set typed string — the
// constants document the backends that ship with yoloai; future
// backends (registered via Register) supply their own name. The
// registry is the source of truth for which names are recognised at
// runtime.
//
// This type exists so the public Client API surface can take a typed
// parameter rather than `string`, preventing typo-style bugs at call
// sites. Internal code that uses plain string keys to look up backends
// keeps working; callers convert with `BackendType(s)` / `string(b)` at
// the boundary.
type BackendType string

const (
	BackendDocker     BackendType = "docker"
	BackendPodman     BackendType = "podman"
	BackendTart       BackendType = "tart"
	BackendSeatbelt   BackendType = "seatbelt"
	BackendContainerd BackendType = "containerd"
	BackendApple      BackendType = "apple"   // Apple `container` — Linux OCI in per-container VMs (macOS 26+)
	BackendMicroVM    BackendType = "microvm" // QEMU -M microvm — Linux/KVM lightweight VMs (Linux host only)
)
