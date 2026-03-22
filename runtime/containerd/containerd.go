// Package containerdrt implements the runtime.Runtime interface using containerd.
// ABOUTME: Manages container/VM lifecycle via the containerd API for Kata Containers (vm isolation).
package containerdrt

import (
	"context"
	"os"
	"strings"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/vishvananda/netns"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
)

// Runtime implements runtime.Runtime using the containerd API.
type Runtime struct {
	client    *client.Client
	namespace string // always "yoloai"

	// Capability fields — built once in New(), returned by RequiredCapabilities.
	kataShimV2           caps.HostCapability
	kataFCShimV2         caps.HostCapability
	cniBridge            caps.HostCapability
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
	kataShimName       = "containerd-shim-kata-v2"
	kataFCShimName     = "containerd-shim-kata-fc-v2"
	cniBridgePath      = "/opt/cni/bin/bridge"
	kvmDevPath         = "/dev/kvm"
	canCreateNetNSFunc = canCreateNetNS
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

// isWSL2 returns true if running inside a WSL2 environment.
func isWSL2() bool {
	data, _ := os.ReadFile("/proc/version")
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}
