package docker

// ABOUTME: HostCapability constructors for the Docker backend — gVisor runsc binary and
// ABOUTME: gVisor registered with the Docker daemon.

import (
	"context"
	"fmt"
	"os/exec"
	goruntime "runtime"
	"strings"

	"github.com/kstenerud/yoloai/internal/runtime"
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

// daemonOperatingSystem returns the daemon's reported OperatingSystem
// ("Docker Desktop", "OrbStack", a Linux distro, …), or "" if unavailable.
// Injectable for tests.
var daemonOperatingSystem = func(ctx context.Context, binaryName string) string {
	out, err := exec.CommandContext(ctx, binaryName, "info", "--format", "{{.OperatingSystem}}").Output() //nolint:gosec // G204: binaryName is "docker" or "podman"
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// DindAdvisory implements runtime.DindAdvisor: a non-fatal heads-up when
// docker-in-docker won't work under container-privileged on this host. The
// privileged mode itself is fine — only nesting a daemon fails.
func (r *Runtime) DindAdvisory(ctx context.Context, isolation runtime.IsolationMode) string {
	if isolation != runtime.IsolationModeContainerPrivileged {
		return ""
	}
	// On Linux the daemon shares the host kernel, which nests dind fine. Off
	// Linux the daemon runs in a VM; only its OperatingSystem (docker) tells us
	// the provider — podman always means Podman Machine on macOS.
	var os string
	if goruntime.GOOS != "linux" && r.binaryName != "podman" {
		os = daemonOperatingSystem(ctx, r.binaryName)
	}
	return dindAdvisory(goruntime.GOOS, r.binaryName, os)
}

// dindAdvisory is the pure provider→capability logic. Returns a heads-up string
// when nested docker-in-docker won't work, or "" when it should.
//
// dind needs the nested daemon's fuse-overlayfs graph driver (native overlay2
// can't nest here) to be exec-able. That depends on the host VM kernel: OrbStack
// and native Linux can exec from fuse-overlayfs; macOS Docker Desktop and Podman
// Machine can't — every nested execve returns EINVAL. See
// docs/contributors/backend-idiosyncrasies.md.
func dindAdvisory(goos, binaryName, operatingSystem string) string {
	if goos == "linux" {
		return "" // native overlay2 nests; dind works
	}
	var provider string
	switch {
	case binaryName == "podman":
		provider = "Podman Machine"
	case strings.Contains(strings.ToLower(operatingSystem), "orbstack"):
		return "" // OrbStack's kernel execs the nested fuse-overlayfs driver fine
	case operatingSystem != "":
		provider = operatingSystem // "Docker Desktop", and any other VM provider
	default:
		provider = "this Docker provider"
	}
	return fmt.Sprintf(
		"docker-in-docker likely won't work on %s: its VM kernel can't exec from "+
			"the nested fuse-overlayfs driver (EINVAL). dind works on OrbStack or a "+
			"Linux host; as a slower workaround run the nested daemon with "+
			"--storage-driver=vfs.", provider)
}
