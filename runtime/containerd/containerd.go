//go:build linux

// Package containerdrt implements the runtime.Backend interface using containerd.
// ABOUTME: Manages container/VM lifecycle via the containerd API for Kata Containers (vm isolation).
package containerdrt

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"syscall"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/vishvananda/netns"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
	"github.com/kstenerud/yoloai/yoerrors"
)

// descriptor holds the static facts for the containerd backend; shared by the
// registry registration and the Runtime.Descriptor() method.
var descriptor = runtime.BackendDescriptor{
	Type:                      runtime.BackendContainerd,
	Description:               "Linux VMs via Kata Containers (--isolation vm/vm-enhanced)",
	Platforms:                 []string{"linux"},
	IsolationTargetOnly:       true,
	Requires:                  "containerd, Kata Containers shim, CNI plugins, /dev/kvm",
	InstallHint:               "sudo apt install containerd kata-containers containernetworking-plugins",
	BaseModeName:              runtime.IsolationModeVM,
	AgentProvisionedByBackend: true,
	SupportedIsolationModes:   []runtime.IsolationMode{runtime.IsolationModeVM, runtime.IsolationModeVMEnhanced},
	Capabilities: runtime.BackendCaps{
		NetworkIsolation:     true,
		CapAdd:               true,
		HostFilesystem:       false,
		FilesystemLocality:   runtime.LocalityHostSide,
		GitExecInConfinement: true, // copy-mode work-copy git runs in-container (audit C1)
		KeepAliveModel:       runtime.KeepAliveGuestOSInit,
	},
	Probe:         probe,
	VersionString: versionString,
}

// versionString returns containerd's daemon version via `containerd --version`.
// Uses a minimal explicit env (PATH only) per DEV §12 — version probes need no secrets.
func versionString(ctx context.Context) string {
	env := sysexec.Curated(nil, []string{"PATH"}, nil)
	out, err := sysexec.CommandContext(ctx, env, "containerd", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// probe reports whether containerd is usable. Stat-only check on the daemon
// socket; never dials. Matches the fast-fail path in New() so callers get the
// same verdict without paying for client construction.
func probe(_ context.Context, _ map[string]string) (runtime.ProbeStatus, string) {
	if _, err := os.Stat(containerdSock); err == nil {
		return runtime.ProbeRunning, ""
	}
	if _, err := exec.LookPath("containerd"); err == nil {
		return runtime.ProbeInstalled, "containerd installed but socket not found (start with: sudo systemctl start containerd)"
	}
	return runtime.ProbeAbsent, "containerd not found (install containerd)"
}

func init() {
	runtime.Register(func(ctx context.Context, layout config.Layout) (runtime.Backend, error) {
		return New(ctx, layout)
	}, descriptor)
}

// Runtime implements runtime.Backend using the containerd API.
type Runtime struct {
	client    *client.Client
	namespace string        // always "yoloai"
	layout    config.Layout // DataDir-rooted path resolver (Q-W.6)
	execEnv   []string      // explicit subprocess env (DEV §12); from layout, never inherited

	// Capability fields — built once in New(), returned by RequiredCapabilities.
	kataShimV2           caps.HostCapability
	kataFCShimV2         caps.HostCapability
	cniBridge            caps.HostCapability
	cniNetAdmin          caps.HostCapability
	netnsCreation        caps.HostCapability
	kvmDevice            caps.HostCapability
	devmapperSnapshotter caps.HostCapability
}

// Compile-time check.
var _ runtime.Backend = (*Runtime)(nil)
var _ runtime.GitExecer = (*Runtime)(nil)
var _ runtime.IsolationCapabilityProvider = (*Runtime)(nil)
var _ runtime.CachePruner = (*Runtime)(nil)
var _ runtime.InteractiveSession = (*Runtime)(nil)
var _ runtime.DiskUsageReporter = (*Runtime)(nil)

// Descriptor returns a BackendDescriptor with the static facts for this backend.
func (r *Runtime) Descriptor() runtime.BackendDescriptor {
	return descriptor
}

const containerdSock = "/run/containerd/containerd.sock"

// New connects to the containerd daemon and returns a Runtime. layout is used
// for all host-path resolution so the backend never reads ambient HOME.
// It does not validate isolation prerequisites — use RequiredCapabilities for that.
func New(_ context.Context, layout config.Layout) (*Runtime, error) {
	// Fast-fail if the socket file doesn't exist — avoids a slow dial timeout
	// on systems where containerd is not installed (e.g. macOS).
	if _, err := os.Stat(containerdSock); err != nil {
		if os.IsPermission(err) {
			return nil, yoerrors.NewPermissionError("no permission to access containerd socket at %s\n  Fix: run yoloai with sudo or configure containerd group access", containerdSock)
		}
		return nil, yoerrors.NewDependencyError("containerd socket not found at %s\n  Is containerd running? Try: sudo systemctl start containerd", containerdSock)
	}
	c, err := client.New(containerdSock)
	if err != nil {
		if runtime.IsPermissionDenied(err) {
			return nil, yoerrors.NewPermissionError("no permission to access containerd socket at %s\n  Fix: run yoloai with sudo", containerdSock)
		}
		return nil, yoerrors.NewDependencyError("connect to containerd: %w\n  Is containerd running? Try: sudo systemctl start containerd", err)
	}
	execEnv := layout.Env().EnvForContainerdExec()
	r := &Runtime{client: c, namespace: "yoloai", layout: layout, execEnv: execEnv}
	r.kataShimV2 = buildKataShimV2Cap()
	r.kataFCShimV2 = buildKataFCShimV2Cap()
	r.cniBridge = buildCNIBridgeCap()
	r.cniNetAdmin = buildCNINetAdminCap()
	r.netnsCreation = buildNetnsCreationCap()
	r.kvmDevice = buildKVMDeviceCap()
	r.devmapperSnapshotter = buildDevmapperSnapshotterCap(r)
	return r, nil
}

// withNamespace injects the yoloai containerd namespace into the context.
// Every containerd API call must carry this namespace.
func (r *Runtime) withNamespace(ctx context.Context) context.Context {
	return namespaces.WithNamespace(ctx, r.namespace)
}

// TmuxSocket returns the fixed tmux socket path for containerd. A fixed path
// is required for Kata Containers because exec'd processes run inside the VM
// in a clean environment and may resolve a different uid-based socket path
// than the container init process. sandboxDir is ignored.
func (r *Runtime) TmuxSocket(_ string) string { return "/tmp/yoloai-tmux.sock" }

// Close releases the containerd client connection.
func (r *Runtime) Close() error { return r.client.Close() }

// RequiredCapabilities returns the host capabilities needed for the given isolation mode.
// containerdSocket is intentionally omitted: New() already verified it.
func (r *Runtime) RequiredCapabilities(isolation runtime.IsolationMode) []caps.HostCapability {
	base := []caps.HostCapability{
		r.kataShimV2,
		r.cniBridge,
		r.cniNetAdmin,
		r.netnsCreation,
		r.kvmDevice,
	}
	switch isolation {
	case runtime.IsolationModeVMEnhanced:
		return append(base, r.kataFCShimV2, r.devmapperSnapshotter)
	default: // "vm" or empty (default)
		return base
	}
}

// Prerequisite check overrides — variable for testing.
var (
	kataShimName        = "containerd-shim-kata-v2"
	kataFCShimName      = "containerd-shim-kata-fc-v2"
	cniBridgePath       = "/opt/cni/bin/bridge"
	kvmDevPath          = "/dev/kvm"
	canCreateNetNSFunc  = canCreateNetNS
	canRunCNIBridgeFunc = canRunCNIBridge
)

// canCreateNetNS tests whether the process can actually create a named network
// namespace by attempting to create and immediately remove a probe namespace.
// This is more reliable than checking individual capabilities because named
// netns creation requires CAP_SYS_ADMIN (unshare) + CAP_DAC_OVERRIDE (write
// to root-owned /var/run/netns/) — easier to test than enumerate.
//
// Locks the OS thread for the unshare → restore window so the probe cannot
// leave a Go runtime thread stuck in the new anonymous netns. Without this,
// libcni's later plugin exec can land on the poisoned thread and run the
// bridge/firewall plugin in the wrong netns — POSTROUTING + CNI-FORWARD rules
// land in an unreachable namespace, breaking outbound NAT for the sandbox.
func canCreateNetNS() error {
	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()

	origNS, err := netns.Get()
	if err != nil {
		return fmt.Errorf("get current netns: %w", err)
	}
	defer origNS.Close() //nolint:errcheck // G104: best-effort

	probe := "yoloai-netns-probe"
	ns, err := netns.NewNamed(probe)
	if err != nil {
		// On failure the thread may or may not have switched; restore
		// defensively before returning so we don't leak a stuck thread.
		_ = netns.Set(origNS)
		return err
	}
	_ = ns.Close()
	_ = netns.DeleteNamed(probe)
	if err := netns.Set(origNS); err != nil {
		return fmt.Errorf("restore original netns: %w", err)
	}
	return nil
}

// CNI bridge capability bits required for the bridge plugin to run.
// CAP_NET_ADMIN (12): create/configure bridge and veth pairs via RTNETLINK.
// CAP_SYS_ADMIN (21): setns() to enter the container's network namespace.
const (
	capNetAdmin = 12
	capSysAdmin = 21
	cniCapMask  = (1 << capNetAdmin) | (1 << capSysAdmin)
)

// canRunCNIBridge returns nil if the CNI bridge plugin will be able to complete
// the full CNI ADD workflow. The plugin runs as a subprocess with the same UID
// as yoloai and needs:
//   - CAP_NET_ADMIN: create/configure the bridge and veth pair
//   - CAP_SYS_ADMIN: setns() to enter the container's network namespace
//
// Root always has these. Non-root requires them on the bridge binary itself
// via setcap (file capabilities grant them at exec time regardless of the
// parent's capability set).
func canRunCNIBridge() error {
	if fileutil.ProcessIsRoot() {
		return nil // root: subprocess also runs as root
	}
	// Check CapEff in case the parent process already has both caps
	// (e.g. via ambient capabilities or some other mechanism).
	if capEff, ok := readCapEff(); ok && capEff&cniCapMask == cniCapMask {
		return nil
	}
	// Accept if the bridge binary has both required caps as file capabilities
	// (set via setcap). The plugin will receive them at exec time.
	if bridgeBinaryHasCNICaps(cniBridgePath) {
		return nil
	}
	return fmt.Errorf("CAP_NET_ADMIN+CAP_SYS_ADMIN not available: CNI bridge plugin cannot set up VM networking")
}

// readCapEff reads CapEff from /proc/self/status. Returns (0, false) on error.
func readCapEff() (capEff uint64, ok bool) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		if _, scanErr := fmt.Sscanf(fields[1], "%x", &capEff); scanErr != nil {
			return 0, false
		}
		return capEff, true
	}
	return 0, false
}

// bridgeBinaryHasCNICaps reads the security.capability xattr from the CNI
// bridge binary and returns true if both CAP_NET_ADMIN and CAP_SYS_ADMIN are
// in the permitted set. The xattr layout (vfs_cap_data) is:
//
//	bytes 0-3:  magic_etc (LE32) — revision in high 24 bits
//	bytes 4-7:  data[0].permitted  (LE32) — caps 0-31
//	bytes 8-11: data[0].inheritable (LE32)
//	bytes 12+:  data[1].* (caps 32-63, present in v2/v3 only)
func bridgeBinaryHasCNICaps(path string) bool {
	var buf [24]byte
	n, err := syscall.Getxattr(path, "security.capability", buf[:])
	if err != nil || n < 8 {
		return false
	}
	permitted0 := binary.LittleEndian.Uint32(buf[4:8])
	return permitted0&cniCapMask == cniCapMask
}
