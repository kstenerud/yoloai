//go:build linux

// Package microvm implements the runtime.Backend interface using QEMU's
// `-M microvm` machine type — lightweight, hardware-isolated Linux VMs on
// Linux/KVM hosts.
//
// ABOUTME: Boots OCI-profile images as QEMU -M microvm VMs directly (no containerd/Kata/CNI):
// ABOUTME: skopeo/umoci/mkfs.ext4 build the rootfs, a bundled PVH kernel boots it, QGA execs in it.
package microvm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
)

// errNotImplemented marks the lifecycle methods still being built out. The
// backend registers (so `yoloai doctor` reports its prerequisites) and selects
// via --isolation microvm, but Create/Start/Exec land in later increments.
// See docs/contributors/design/plans/microvm-backend.md (build sub-steps a–f).
var errNotImplemented = errors.New("microvm backend: not yet implemented")

const (
	// qemuBin is the QEMU system emulator for the microvm machine type.
	// microvm is x86-only in QEMU, so the backend is amd64-only.
	qemuBin = "qemu-system-x86_64"

	// kvmDevPath is the KVM device node required for hardware acceleration.
	kvmDevPath = "/dev/kvm"

	// backendSubdir is the per-host microvm state dir under DataDir
	// (bundled kernel, shared build artifacts).
	backendSubdir = "microvm"

	// kernelFileName is the bundled PVH-bootable kernel provisioned by Setup.
	kernelFileName = "vmlinuz"
)

// descriptor holds the static facts for the microvm backend; shared by the
// registry registration and the Runtime.Descriptor() method.
var descriptor = runtime.BackendDescriptor{
	Type:                      runtime.BackendMicroVM,
	Description:               "Linux/KVM lightweight VMs via QEMU -M microvm (--isolation microvm)",
	Platforms:                 []string{"linux"},
	Architectures:             []string{"amd64"}, // QEMU -M microvm is x86-only
	IsolationTargetOnly:       true,
	Requires:                  "QEMU, KVM (/dev/kvm), skopeo, umoci, virtiofsd",
	InstallHint:               "sudo apt install qemu-system-x86 qemu-utils skopeo umoci virtiofsd",
	BaseModeName:              runtime.IsolationModeMicroVM,
	AgentProvisionedByBackend: true,
	SupportedIsolationModes:   []runtime.IsolationMode{runtime.IsolationModeMicroVM},
	Capabilities: runtime.BackendCaps{
		NetworkIsolation:   false, // phase 1: yoloAI-managed TAP/bridge deferred behind the egress proxy
		OverlayDirs:        false,
		CapAdd:             false,
		HostFilesystem:     false,
		FilesystemLocality: runtime.LocalityHostSide, // workdir shared via virtiofs; host git works
		KeepAliveModel:     runtime.KeepAliveGuestOSInit,
		AgentFreeLaunch:    false,
	},
	// microvm boots in well under a second, but the OCI->ext4 rootfs build
	// (skopeo pull + umoci unpack + mkfs) on first launch can be slow; give
	// the in-guest setup a generous window to signal secrets consumed.
	SecretsConsumedTimeout: 120 * time.Second,
	Probe:                  probe,
	VersionString:          versionString,
}

// versionString returns the QEMU version (first line of `qemu-system-x86_64 --version`).
// Minimal env (PATH only) per DEV §12 — version probes need no secrets.
func versionString(ctx context.Context) string {
	env := sysexec.Curated(nil, []string{"PATH"}, nil)
	out, err := sysexec.CommandContext(ctx, env, qemuBin, "--version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}

// probe reports whether the microvm backend is usable. Pure host checks
// (LookPath + a /dev/kvm stat) — no fork of qemu, safe on every dispatch.
// "Running" requires both QEMU on PATH and an accessible /dev/kvm; the finer
// per-prerequisite breakdown (skopeo/umoci/virtiofsd/kernel) is reported by
// `yoloai doctor` via RequiredCapabilities.
func probe(_ context.Context, _ map[string]string) (runtime.ProbeStatus, string) {
	qemuPresent := false
	if _, err := exec.LookPath(qemuBin); err == nil {
		qemuPresent = true
	}
	if !qemuPresent {
		return runtime.ProbeAbsent, "QEMU not found (install with: sudo apt install qemu-system-x86)"
	}
	if err := checkKVM(); err != nil {
		return runtime.ProbeInstalled, "QEMU installed but KVM unavailable: " + err.Error()
	}
	return runtime.ProbeRunning, ""
}

func init() {
	runtime.Register(func(ctx context.Context, layout config.Layout) (runtime.Backend, error) {
		return New(ctx, layout)
	}, descriptor)
}

// Runtime implements runtime.Backend using QEMU -M microvm VMs.
type Runtime struct {
	layout  config.Layout // DataDir-rooted path resolver
	execEnv []string      // explicit subprocess env (DEV §12); from layout, never inherited

	// Capability fields — built once in New(), returned by RequiredCapabilities.
	qemuCap      caps.HostCapability
	kvmCap       caps.HostCapability
	virtiofsdCap caps.HostCapability
}

// Compile-time checks.
var _ runtime.Backend = (*Runtime)(nil)
var _ runtime.IsolationCapabilityProvider = (*Runtime)(nil)

// New constructs a microvm Runtime. It does not validate that the host
// prerequisites are installed — RequiredCapabilities does that, so `yoloai
// doctor` can build the backend and report exactly what's missing. layout is
// used for all host-path resolution so the backend never reads ambient HOME.
func New(_ context.Context, layout config.Layout) (*Runtime, error) {
	r := &Runtime{
		layout:  layout,
		execEnv: layout.Env().EnvForMicroVMExec(), // curated CLI env for qemu/virtiofsd (lifecycle)
	}
	r.qemuCap = buildQemuCap()
	r.kvmCap = buildKVMCap()
	r.virtiofsdCap = buildVirtiofsdCap()
	return r, nil
}

// Descriptor returns a BackendDescriptor with the static facts for this backend.
func (r *Runtime) Descriptor() runtime.BackendDescriptor { return descriptor }

// kernelPath is where Setup provisions the bundled PVH-bootable kernel.
func (r *Runtime) kernelPath() string {
	return filepath.Join(r.layout.DataDir, backendSubdir, kernelFileName)
}

// DiagHint returns a hint for inspecting a failed instance's QEMU/serial logs.
func (r *Runtime) DiagHint(instanceName string) string {
	return fmt.Sprintf("check the serial console log at %s",
		filepath.Join(r.layout.SandboxesDir(), instanceName, config.BackendDirName, "serial.log"))
}

// Close releases resources. The microvm backend holds no long-lived handles.
func (r *Runtime) Close() error { return nil }

// --- Lifecycle (Setup/IsReady in setup.go; rest in later increments) ---

// Create writes per-instance config and stages the workdir share. (sub-step b)
func (r *Runtime) Create(_ context.Context, _ runtime.InstanceConfig) error { return errNotImplemented }

// Start launches the daemonized QEMU process. (sub-step b)
func (r *Runtime) Start(_ context.Context, _ string) error { return errNotImplemented }

// Stop terminates the QEMU process (SIGTERM->SIGKILL). (sub-step b)
func (r *Runtime) Stop(_ context.Context, _ string) error { return errNotImplemented }

// Remove deletes the instance and its QEMU/virtiofsd state. (sub-step b)
func (r *Runtime) Remove(_ context.Context, _ string) error { return errNotImplemented }

// Inspect reports instance liveness from the QEMU PID. (sub-step b)
func (r *Runtime) Inspect(_ context.Context, _ string) (runtime.InstanceInfo, error) {
	return runtime.InstanceInfo{}, errNotImplemented
}

// Exec runs a command via the QEMU guest agent. (sub-step c)
func (r *Runtime) Exec(_ context.Context, _ string, _ []string, _ string) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, errNotImplemented
}

// InteractiveExec bridges IOStreams to the guest serial console. (sub-step c)
func (r *Runtime) InteractiveExec(_ context.Context, _ string, _ []string, _ string, _ string, _ runtime.IOStreams) error {
	return errNotImplemented
}

// Prune removes orphaned microvm instances. (sub-step b)
func (r *Runtime) Prune(_ context.Context, _ []string, _ bool, _ io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{}, errNotImplemented
}
