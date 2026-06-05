package docker

// ABOUTME: HostCapability constructors for the Docker backend — gVisor runsc binary and
// ABOUTME: gVisor registered with the Docker daemon.

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strings"

	"github.com/kstenerud/yoloai/internal/runtime/caps"
)

// buildGVisorRegisteredCap returns a HostCapability that checks whether runsc is
// registered as a Docker runtime. Uses the injectable dockerInfoOutput var.
func buildGVisorRegisteredCap(binaryName string) caps.HostCapability {
	return caps.HostCapability{
		ID:      "gvisor-registered",
		Summary: "gVisor registered with Docker daemon",
		Detail:  "Required for --isolation container-enhanced. runsc must be registered in daemon.json.",
		Check: func(ctx context.Context) error {
			out, err := dockerInfoOutput(ctx, binaryName)
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
