// ABOUTME: IsolationMode — typed enum for the sandbox isolation surface
// ABOUTME: (container / container-enhanced / container-privileged / vm /
// ABOUTME: vm-enhanced / process). Closed set; consumed by isolation.go
// ABOUTME: and the meta/options chain.

package runtime

// IsolationMode names the sandbox isolation mode. Closed set: the
// constants below are the only valid values. The empty value means
// "backend default" — every backend declares a BaseModeName for the
// no-isolation path; the conversion happens in the create chain.
//
// JSON round-trip works via the underlying string type: meta.Isolation
// serialises as "vm-enhanced" / "container-enhanced" / "" etc., matching
// the format that existed before F11 (no migration needed).
type IsolationMode string

const (
	// IsolationModeDefault is the empty sentinel meaning "use the
	// backend's BaseMode". Kept as a named constant so call sites read
	// `IsolationModeDefault` rather than `""`.
	IsolationModeDefault IsolationMode = ""

	IsolationModeContainer           IsolationMode = "container"
	IsolationModeContainerEnhanced   IsolationMode = "container-enhanced"
	IsolationModeContainerPrivileged IsolationMode = "container-privileged"
	IsolationModeVM                  IsolationMode = "vm"
	IsolationModeVMEnhanced          IsolationMode = "vm-enhanced"
	IsolationModeProcess             IsolationMode = "process" // seatbelt's base mode
)
