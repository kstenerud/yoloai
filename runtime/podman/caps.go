package podman

// ABOUTME: HostCapability constructors for the Podman backend — rootless check and gVisor runsc.

import (
	"context"
	"fmt"

	"github.com/kstenerud/yoloai/runtime/caps"
)

// buildRootlessCheckCap returns a HostCapability that fails permanently when
// Podman is running in rootless mode, since rootless Podman cannot run gVisor.
func buildRootlessCheckCap(rootless bool) caps.HostCapability {
	return caps.HostCapability{
		ID:      "podman-root-mode",
		Summary: "Podman root mode",
		Detail:  "rootless Podman cannot run gVisor due to cgroup v2 delegation.",
		Check: func(_ context.Context) error {
			if rootless {
				return fmt.Errorf("rootless Podman cannot run gVisor (cgroup v2 delegation)")
			}
			return nil
		},
		Permanent: func(_ caps.Environment) bool {
			return rootless // permanently unavailable for rootless mode
		},
		Fix: func(_ caps.Environment) []caps.FixStep {
			return []caps.FixStep{
				{
					Description: "Switch to root Podman",
					Command:     "sudo podman ...",
					NeedsRoot:   true,
				},
				{
					Description: "Use Docker instead of Podman for container-enhanced isolation",
					URL:         "https://docs.docker.com/get-docker/",
				},
			}
		},
	}
}
