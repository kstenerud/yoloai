//go:build linux

// Package containerdrt implements the runtime.Runtime interface using containerd.
// ABOUTME: Manages container/VM lifecycle via the containerd API for Kata Containers (vm isolation).
package containerdrt

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/vishvananda/netns"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
)

func init() {
	runtime.Register("containerd", func(ctx context.Context) (runtime.Runtime, error) {
		return New(ctx)
	})
}

// Runtime implements runtime.Runtime using the containerd API.
type Runtime struct {
	client    *client.Client
	namespace string // always "yoloai"

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
var _ runtime.Runtime = (*Runtime)(nil)

// Capabilities returns the containerd backend's feature set.
func (r *Runtime) Capabilities() runtime.BackendCaps {
	return runtime.BackendCaps{
		NetworkIsolation: true,
		OverlayDirs:      false, // overlayfs-in-container not supported with Kata shim
		CapAdd:           true,
		HostFilesystem:   false,
	}
}

// AgentProvisionedByBackend returns true — containerd containers use an npm-installed agent.
func (r *Runtime) AgentProvisionedByBackend() bool { return true }

// ResolveCopyMount returns hostPath unchanged — containerd bind-mounts the copy
// at the original host path inside the container.
func (r *Runtime) ResolveCopyMount(_, hostPath string) string { return hostPath }

const containerdSock = "/run/containerd/containerd.sock"

// New connects to the containerd daemon and returns a Runtime.
// It does not validate isolation prerequisites — use RequiredCapabilities for that.
func New(_ context.Context) (*Runtime, error) {
	// Fast-fail if the socket file doesn't exist — avoids a slow dial timeout
	// on systems where containerd is not installed (e.g. macOS).
	if _, err := os.Stat(containerdSock); err != nil {
		if os.IsPermission(err) {
			return nil, config.NewPermissionError("no permission to access containerd socket at %s\n  Fix: run yoloai with sudo or configure containerd group access", containerdSock)
		}
		return nil, config.NewDependencyError("containerd socket not found at %s\n  Is containerd running? Try: sudo systemctl start containerd", containerdSock)
	}
	c, err := client.New(containerdSock)
	if err != nil {
		if os.IsPermission(err) || strings.Contains(err.Error(), "permission denied") {
			return nil, config.NewPermissionError("no permission to access containerd socket at %s\n  Fix: run yoloai with sudo", containerdSock)
		}
		return nil, config.NewDependencyError("connect to containerd: %w\n  Is containerd running? Try: sudo systemctl start containerd", err)
	}
	r := &Runtime{client: c, namespace: "yoloai"}
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

// Name returns the backend name.
func (r *Runtime) Name() string { return "containerd" }

// TmuxSocket returns the fixed tmux socket path for containerd. A fixed path
// is required for Kata Containers because exec'd processes run inside the VM
// in a clean environment and may resolve a different uid-based socket path
// than the container init process. sandboxDir is ignored.
func (r *Runtime) TmuxSocket(_ string) string { return "/tmp/yoloai-tmux.sock" }

// Close releases the containerd client connection.
func (r *Runtime) Close() error { return r.client.Close() }

// BaseModeName returns "vm" — containerd in yoloai is exclusively for VM isolation.
func (r *Runtime) BaseModeName() string { return "vm" }

// SupportedIsolationModes returns the VM isolation modes this backend supports.
func (r *Runtime) SupportedIsolationModes() []string { return []string{"vm", "vm-enhanced"} }

// RequiredCapabilities returns the host capabilities needed for the given isolation mode.
// containerdSocket is intentionally omitted: New() already verified it.
func (r *Runtime) RequiredCapabilities(isolation string) []caps.HostCapability {
	base := []caps.HostCapability{
		r.kataShimV2,
		r.cniBridge,
		r.cniNetAdmin,
		r.netnsCreation,
		r.kvmDevice,
	}
	switch isolation {
	case "vm-enhanced":
		return append(base, r.kataFCShimV2, r.devmapperSnapshotter)
	default: // "vm"
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
func canCreateNetNS() error {
	probe := "yoloai-netns-probe"
	// Attempt to create; ignore "already exists" (from a previous failed probe).
	_, err := netns.NewNamed(probe)
	if err != nil {
		return err
	}
	_ = netns.DeleteNamed(probe)
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
	if os.Getuid() == 0 {
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
	for _, line := range strings.Split(string(data), "\n") {
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

// GitExec runs a git command on the host filesystem.
// For containerd (Kata VM) backends, :copy-mode work directories are stored on
// the host at ~/.yoloai/sandboxes/<name>/work/<encoded>/ and bind-mounted into
// the VM — the host copy is authoritative for diff/apply operations.
// workDir is a host path; name is unused (kept for interface compatibility).
func (r *Runtime) GitExec(ctx context.Context, _ string, workDir string, args ...string) (string, error) {
	cmdArgs := append([]string{"-c", "core.hooksPath=/dev/null", "-C", workDir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...) //nolint:gosec // G204: workDir from validated sandbox state
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if ok := errors.As(err, &exitErr); ok {
			return "", fmt.Errorf("exec exited with code %d: %s", exitErr.ExitCode(), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("git %v: %w", args, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// isWSL2 returns true if running inside a WSL2 environment.
func isWSL2() bool {
	data, _ := os.ReadFile("/proc/version")
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}
