package containerdrt

// ABOUTME: Capability field constructors for the containerd backend — kata shim, CNI bridge,
// ABOUTME: network namespace creation, KVM device, devmapper snapshotter, and Firecracker shim.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/containerd/errdefs"

	"github.com/kstenerud/yoloai/runtime/caps"
)

// buildKataShimV2Cap returns a HostCapability that checks for the Kata QEMU shim.
func buildKataShimV2Cap() caps.HostCapability {
	return caps.HostCapability{
		ID:      "kata-shim-v2",
		Summary: "kata-containers shim",
		Detail:  "Required for --isolation vm. Install kata-containers.",
		Check: func(_ context.Context) error {
			if _, err := exec.LookPath(kataShimName); err != nil {
				return fmt.Errorf("%s not found in PATH", kataShimName)
			}
			return nil
		},
		Permanent: func(env caps.Environment) bool {
			return env.InContainer // can't install shim inside a container
		},
		Fix: func(_ caps.Environment) []caps.FixStep {
			return []caps.FixStep{{
				Description: "Install kata-containers",
				Command:     "sudo apt install kata-containers",
				NeedsRoot:   true,
			}}
		},
	}
}

// buildKataFCShimV2Cap returns a HostCapability that checks for the Kata Firecracker shim.
func buildKataFCShimV2Cap() caps.HostCapability {
	return caps.HostCapability{
		ID:      "kata-fc-shim-v2",
		Summary: "kata-containers Firecracker shim",
		Detail:  "Required for --isolation vm-enhanced (Firecracker VMM).",
		Check: func(_ context.Context) error {
			if _, err := exec.LookPath(kataFCShimName); err != nil {
				return fmt.Errorf("%s not found in PATH", kataFCShimName)
			}
			return nil
		},
		Permanent: func(env caps.Environment) bool {
			return env.InContainer
		},
		Fix: func(_ caps.Environment) []caps.FixStep {
			return []caps.FixStep{{
				Description: "Install kata-containers with Firecracker support",
				Command:     "sudo apt install kata-containers",
				NeedsRoot:   true,
			}}
		},
	}
}

// buildCNIBridgeCap returns a HostCapability that checks for the CNI bridge plugin.
func buildCNIBridgeCap() caps.HostCapability {
	return caps.HostCapability{
		ID:      "cni-bridge",
		Summary: "CNI plugins",
		Detail:  "Required for VM networking. Install containernetworking-plugins.",
		Check: func(_ context.Context) error {
			if _, err := os.Stat(cniBridgePath); err != nil {
				return fmt.Errorf("CNI bridge plugin not found at %s", cniBridgePath)
			}
			return nil
		},
		Permanent: func(env caps.Environment) bool {
			return env.InContainer
		},
		Fix: func(_ caps.Environment) []caps.FixStep {
			return []caps.FixStep{{
				Description: "Install CNI plugins",
				Command:     "sudo apt install containernetworking-plugins",
				NeedsRoot:   true,
			}}
		},
	}
}

// buildNetnsCreationCap returns a HostCapability that checks whether the process
// can create named network namespaces.
func buildNetnsCreationCap() caps.HostCapability {
	return caps.HostCapability{
		ID:      "netns-creation",
		Summary: "network namespace creation",
		Detail:  "VM isolation requires CAP_SYS_ADMIN to create named network namespaces.",
		Check: func(_ context.Context) error {
			return canCreateNetNSFunc()
		},
		Permanent: func(_ caps.Environment) bool {
			return false // always fixable
		},
		Fix: func(_ caps.Environment) []caps.FixStep {
			return []caps.FixStep{
				{
					Description: "Run as root (simplest)",
					Command:     "sudo yoloai new mybox --isolation vm ...",
					NeedsRoot:   true,
				},
				{
					Description: "Grant capability to binary (lost on reinstall)",
					Command:     "sudo setcap cap_sys_admin,cap_dac_override+ep $(which yoloai)",
					NeedsRoot:   true,
				},
			}
		},
	}
}

// buildKVMDeviceCap returns a HostCapability that checks for KVM device access.
func buildKVMDeviceCap() caps.HostCapability {
	return caps.HostCapability{
		ID:      "kvm-device",
		Summary: "KVM device access",
		Detail:  "Required for hardware VM isolation. /dev/kvm must exist and be accessible.",
		Check: func(_ context.Context) error {
			info, err := os.Stat(kvmDevPath)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("/dev/kvm not found: KVM is not available")
				}
				return fmt.Errorf("/dev/kvm: %w", err)
			}
			// Device exists — check group membership via the file mode.
			// If the file is group-writable (most distros set kvm group), we need to be in kvm.
			mode := info.Mode()
			if mode&0o060 != 0 { // group read/write bits set
				// Check group membership via detectKVMGroup (simplified: just check if we can open it).
				f, openErr := os.Open(kvmDevPath) //nolint:gosec // G304: kvmDevPath is an injectable var pointing to /dev/kvm
				if openErr != nil {
					if strings.Contains(openErr.Error(), "permission denied") {
						return fmt.Errorf("permission denied on /dev/kvm: add user to 'kvm' group")
					}
					// Device exists but open failed for another reason; it may still be usable.
				} else {
					f.Close() //nolint:errcheck,gosec // best-effort
				}
			}
			return nil
		},
		Permanent: func(env caps.Environment) bool {
			// Permanent when /dev/kvm is absent AND no vmx/svm in cpuinfo.
			if _, err := os.Stat(kvmDevPath); err == nil {
				return false // device present — not permanent
			}
			return !hasCPUVirtFlags()
		},
		Fix: func(_ caps.Environment) []caps.FixStep {
			if _, err := os.Stat(kvmDevPath); os.IsNotExist(err) {
				return []caps.FixStep{{
					Description: "KVM hardware not detected — if running in a VM, enabling KVM passthrough in your hypervisor may resolve this",
				}}
			}
			// Device present but permission issue.
			return []caps.FixStep{{
				Description: "Add your user to the kvm group",
				Command:     "sudo usermod -aG kvm $USER\nnewgrp kvm   # or log out and back in",
				NeedsRoot:   true,
			}}
		},
	}
}

// buildDevmapperSnapshotterCap returns a HostCapability that checks the devmapper snapshotter.
func buildDevmapperSnapshotterCap(r *Runtime) caps.HostCapability {
	return caps.HostCapability{
		ID:      "devmapper-snapshotter",
		Summary: "devmapper snapshotter",
		Detail:  "Required for --isolation vm-enhanced (Firecracker VMM needs devmapper).",
		Check: func(ctx context.Context) error {
			return checkDevmapperSnapshotter(ctx, r)
		},
		Permanent: func(_ caps.Environment) bool {
			return false // can be set up on any Linux with block devices
		},
		Fix: func(_ caps.Environment) []caps.FixStep {
			return []caps.FixStep{{
				Description: "Run the devmapper setup script and restart containerd",
				URL:         "https://github.com/kata-containers/kata-containers/blob/main/docs/how-to/containerd-kata-fc-for-ubuntu.md",
				NeedsRoot:   true,
			}}
		},
	}
}

// hasCPUVirtFlags checks /proc/cpuinfo for vmx (Intel VT-x) or svm (AMD-V) flags.
// Returns true if either is found, indicating the CPU supports virtualization.
func hasCPUVirtFlags() bool {
	f, err := os.Open("/proc/cpuinfo") //nolint:gosec // G304: fixed well-known path
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck // best-effort close

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "flags") {
			return strings.Contains(line, " vmx") || strings.Contains(line, " svm")
		}
	}
	return false
}

// checkDevmapperSnapshotter probes the devmapper snapshotter by calling Stat with a
// non-existent key. A "not found" error means the snapshotter is registered and working;
// any other error means it is not configured.
func checkDevmapperSnapshotter(ctx context.Context, r *Runtime) error {
	ctx = r.withNamespace(ctx)
	_, err := r.client.SnapshotService("devmapper").Stat(ctx, "probe")
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("devmapper snapshotter not configured: %w\n"+
			"    Run the devmapper setup script and restart containerd", err)
	}
	return nil
}
