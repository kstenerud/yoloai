package docker

// ABOUTME: HostCapability constructors for the Docker backend — gVisor runsc binary,
// ABOUTME: gVisor registered with the Docker daemon, and the runc version floor.

import (
	"context"
	"fmt"
	"os/exec"
	goruntime "runtime"
	"strings"

	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/runtime/caps"
)

// runcVersionFloorMeets reports whether a runc version fixes the Nov 2025
// masked-path/mount-race container-escape CVEs (CVE-2025-31133,
// CVE-2025-52565, CVE-2025-52881), fixed in 1.2.8, 1.3.3, and 1.4.0-rc.3.
func runcVersionFloorMeets(major, minor, patch int) bool {
	switch {
	case major != 1:
		return major > 1
	case minor == 2:
		return patch >= 8
	case minor == 3:
		return patch >= 3
	default:
		return minor >= 4
	}
}

// buildRuncVersionFloorCap returns an advisory capability warning when the
// host's runc is older than the version fixing known container-escape CVEs.
// Linux-only: on macOS/Windows the daemon runs inside a VM, so the host PATH
// says nothing about the daemon's actual runc.
func buildRuncVersionFloorCap() caps.HostCapability {
	return caps.NewOCIRuntimeVersionFloor(
		"runc-version-floor",
		"runc",
		"runc version floor",
		"runc versions below 1.2.8/1.3.3/1.4.0-rc.3 are missing fixes for known "+
			"mount-race container-escape CVEs (CVE-2025-31133, CVE-2025-52565, CVE-2025-52881).",
		"https://github.com/opencontainers/runc/releases",
		exec.LookPath,
		func(path string) ([]byte, error) { return sysexec.Command([]string{}, path, "--version").Output() },
		runcVersionFloorMeets,
	)
}

// buildGVisorRegisteredCap returns a HostCapability that checks whether runsc is
// registered as a Docker runtime. Uses the injectable dockerInfoOutput var.
// env is the explicit subprocess env (DEV §12); forwarded to dockerInfoOutput.
func buildGVisorRegisteredCap(env []string, binaryName string) caps.HostCapability {
	return caps.HostCapability{
		ID:      "gvisor-registered",
		Summary: "gVisor registered with Docker daemon",
		Detail:  "Required for --isolation container-enhanced. runsc must be registered in daemon.json.",
		Check: func(ctx context.Context) error {
			out, err := dockerInfoOutput(ctx, env, binaryName)
			if err != nil {
				return fmt.Errorf("check runtimes: %w", err)
			}
			for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
				if strings.TrimSpace(line) == "runsc" {
					return nil
				}
			}
			return fmt.Errorf("runsc not registered as a %s runtime", binaryName)
		},
		Permanent: func(env caps.Environment) bool {
			return env.InContainer // can't modify daemon.json inside a container
		},
		Fix: func(_ caps.Environment) []caps.FixStep {
			// runsc must live and be registered wherever the daemon runs. On
			// macOS/Windows the daemon is in a VM (Docker Desktop / OrbStack), so
			// the Linux "/etc/docker/daemon.json + systemctl" recipe is wrong —
			// point users at the in-VM setup instead.
			if goruntime.GOOS != "linux" {
				return []caps.FixStep{{
					Description: "Install and register runsc inside the Docker VM (not the macOS host). " +
						"See the gVisor macOS setup notes — `runsc` goes in the VM's daemon runtimes, " +
						"not /etc/docker/daemon.json on the host.",
					URL: "https://gvisor.dev/docs/user_guide/install/",
				}}
			}
			return []caps.FixStep{{
				Description: "Register runsc in /etc/docker/daemon.json and restart Docker",
				Command:     `echo '{"runtimes":{"runsc":{"path":"/usr/local/sbin/runsc"}}}' | sudo tee /etc/docker/daemon.json` + "\nsudo systemctl restart docker",
				NeedsRoot:   true,
			}}
		},
	}
}
