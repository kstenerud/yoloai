package podman

// ABOUTME: HostCapability constructors for the Podman backend — rootless check, gVisor
// ABOUTME: runsc, and the crun version floor.

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/kstenerud/yoloai/internal/sysexec"
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

// crunVersionFloorMeets reports whether a crun version fixes the confirmed
// symlink-escape container breakout (GHSA-f42g-r5jj-qh4j), fixed in 1.20.
func crunVersionFloorMeets(major, minor, _ int) bool {
	if major != 1 {
		return major > 1
	}
	return minor >= 20
}

// buildCrunVersionFloorCap returns an advisory capability warning when the
// host's crun is older than the version fixing a known container-escape CVE.
// Linux-only: on macOS the daemon runs inside Podman Machine (a VM), so the
// host PATH says nothing about the daemon's actual crun.
func buildCrunVersionFloorCap() caps.HostCapability {
	return caps.NewOCIRuntimeVersionFloor(
		"crun-version-floor",
		"crun",
		"crun version floor",
		"crun versions below 1.20 are missing a fix for a symlink-escape container "+
			"breakout (GHSA-f42g-r5jj-qh4j).",
		"https://github.com/containers/crun/releases",
		exec.LookPath,
		func(path string) ([]byte, error) { return sysexec.Command([]string{}, path, "--version").Output() },
		crunVersionFloorMeets,
	)
}
