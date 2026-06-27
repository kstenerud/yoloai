//go:build linux

package microvm

// ABOUTME: Host-prerequisite capability builders for the microvm backend — QEMU, KVM (/dev/kvm),
// ABOUTME: skopeo, umoci, virtiofsd. Drive `yoloai doctor` output and the actionable launch-time error.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
)

// virtiofsdSearchPaths are the non-PATH locations distros install virtiofsd
// (it is frequently shipped outside $PATH alongside QEMU's helpers).
var virtiofsdSearchPaths = []string{
	"/usr/libexec/virtiofsd",
	"/usr/lib/qemu/virtiofsd",
	"/usr/lib/virtiofsd",
}

// RequiredCapabilities returns the host capabilities the microvm backend needs
// to *run* a sandbox: QEMU, KVM, and virtiofsd. The kernel and rootfs are
// provisioned by Setup, which builds + converts the image inside Docker — the
// OCI tooling runs in-container, so there is no skopeo/umoci host prerequisite.
// Docker is a build-time dependency surfaced by Setup, not a per-launch capability.
func (r *Runtime) RequiredCapabilities(_ runtime.IsolationMode) []caps.HostCapability {
	return []caps.HostCapability{
		r.qemuCap,
		r.kvmCap,
		r.virtiofsdCap,
	}
}

// buildQemuCap checks for the QEMU system emulator with microvm support.
func buildQemuCap() caps.HostCapability {
	return caps.HostCapability{
		ID:      "qemu-microvm",
		Summary: "QEMU system emulator",
		Detail:  "Required to boot -M microvm VMs. Install qemu-system-x86 and qemu-utils.",
		Check: func(_ context.Context) error {
			if _, err := exec.LookPath(qemuBin); err != nil {
				return fmt.Errorf("%s not found in PATH", qemuBin)
			}
			return nil
		},
		Permanent: func(env caps.Environment) bool { return env.InContainer },
		Fix: func(_ caps.Environment) []caps.FixStep {
			return []caps.FixStep{{
				Description: "Install QEMU (system emulator + image tools)",
				Command:     "sudo apt install qemu-system-x86 qemu-utils",
				NeedsRoot:   true,
			}}
		},
	}
}

// buildKVMCap checks for KVM hardware acceleration via /dev/kvm.
func buildKVMCap() caps.HostCapability {
	return caps.HostCapability{
		ID:      "kvm-device",
		Summary: "KVM device access",
		Detail:  "Required for hardware VM acceleration. /dev/kvm must exist and be accessible.",
		Check:   func(_ context.Context) error { return checkKVM() },
		Permanent: func(_ caps.Environment) bool {
			// Permanent only when /dev/kvm is absent AND the CPU lacks virt flags.
			if _, err := os.Stat(kvmDevPath); err == nil {
				return false
			}
			return !hasCPUVirtFlags()
		},
		Fix: func(_ caps.Environment) []caps.FixStep {
			if _, err := os.Stat(kvmDevPath); os.IsNotExist(err) {
				return []caps.FixStep{{
					Description: "KVM hardware not detected — if running in a VM, enable nested virtualization / KVM passthrough in your hypervisor",
				}}
			}
			return []caps.FixStep{{
				Description: "Add your user to the kvm group",
				Command:     "sudo usermod -aG kvm $USER\nnewgrp kvm   # or log out and back in",
				NeedsRoot:   true,
			}}
		},
	}
}

// buildVirtiofsdCap checks for virtiofsd (workdir sharing into the VM).
func buildVirtiofsdCap() caps.HostCapability {
	return caps.HostCapability{
		ID:      "virtiofsd",
		Summary: "virtiofsd",
		Detail:  "Required to share the working directory into the VM via virtiofs.",
		Check: func(_ context.Context) error {
			if findVirtiofsd() == "" {
				return errors.New("virtiofsd not found in PATH or " + strings.Join(virtiofsdSearchPaths, ", "))
			}
			return nil
		},
		Permanent: func(env caps.Environment) bool { return env.InContainer },
		Fix: func(_ caps.Environment) []caps.FixStep {
			return []caps.FixStep{{
				Description: "Install virtiofsd",
				Command:     "sudo apt install virtiofsd",
				NeedsRoot:   true,
			}}
		},
	}
}

// findVirtiofsd returns the virtiofsd path (PATH first, then known libexec
// locations), or "" if not found.
func findVirtiofsd() string {
	if p, err := exec.LookPath("virtiofsd"); err == nil {
		return p
	}
	for _, p := range virtiofsdSearchPaths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// checkKVM returns nil when /dev/kvm exists and is accessible to this process.
func checkKVM() error {
	info, err := os.Stat(kvmDevPath)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("/dev/kvm not found: KVM is not available")
		}
		return fmt.Errorf("/dev/kvm: %w", err)
	}
	// Device present — confirm we can open it (group membership).
	if info.Mode()&0o060 != 0 {
		f, openErr := os.Open(kvmDevPath) //nolint:gosec // G304: fixed well-known device path
		if openErr != nil {
			if errors.Is(openErr, fs.ErrPermission) {
				return errors.New("permission denied on /dev/kvm: add user to the 'kvm' group")
			}
			// Other open errors don't necessarily mean unusable; treat as OK.
			return nil
		}
		_ = f.Close()
	}
	return nil
}

// hasCPUVirtFlags reports whether /proc/cpuinfo advertises Intel VT-x (vmx) or
// AMD-V (svm), indicating the CPU can run KVM even if /dev/kvm isn't present yet.
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
	_ = scanner.Err() // best-effort probe; absence of flags reads as "no virt"
	return false
}
