package caps

// ABOUTME: Shared HostCapability constructors reused across multiple backends.
// ABOUTME: Each constructor takes injectable function pointers for testability.

import "context"

// NewGVisorRunsc returns a capability that checks for the runsc binary in PATH.
// lookPath is injectable for testing (pass exec.LookPath in production).
func NewGVisorRunsc(lookPath func(string) (string, error)) HostCapability {
	return HostCapability{
		ID:      "gvisor-runsc",
		Summary: "gVisor runtime (runsc)",
		Detail:  "Required for --isolation container-enhanced.",
		Check: func(_ context.Context) error {
			_, err := lookPath("runsc")
			return err
		},
		Permanent: func(env Environment) bool {
			return env.InContainer // can't install binaries inside a container
		},
		Fix: func(_ Environment) []FixStep {
			return []FixStep{{
				Description: "Install gVisor",
				URL:         "https://gvisor.dev/docs/user_guide/install/",
				NeedsRoot:   true,
			}}
		},
	}
}
