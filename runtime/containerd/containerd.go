// Package containerdrt implements the runtime.Runtime interface using containerd.
// ABOUTME: Manages container/VM lifecycle via the containerd API for Kata Containers (vm isolation).
package containerdrt

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
)

// Runtime implements runtime.Runtime using the containerd API.
type Runtime struct {
	client    *client.Client
	namespace string // always "yoloai"
}

// Compile-time checks.
var _ runtime.Runtime = (*Runtime)(nil)
var _ runtime.IsolationValidator = (*Runtime)(nil)

// Capabilities returns the containerd backend's feature set.
func (r *Runtime) Capabilities() runtime.BackendCaps {
	return runtime.BackendCaps{
		NetworkIsolation: true,
		OverlayDirs:      false, // overlayfs-in-container not supported with Kata shim
		CapAdd:           true,
	}
}

// AgentProvisionedByBackend returns true — containerd containers use an npm-installed agent.
func (r *Runtime) AgentProvisionedByBackend() bool { return true }

// ResolveCopyMount returns hostPath unchanged — containerd bind-mounts the copy
// at the original host path inside the container.
func (r *Runtime) ResolveCopyMount(_, hostPath string) string { return hostPath }

const containerdSock = "/run/containerd/containerd.sock"

// New connects to the containerd daemon and returns a Runtime.
// It does not validate isolation prerequisites — that is done via ValidateIsolation.
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
	return &Runtime{client: c, namespace: "yoloai"}, nil
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

// Prerequisite check overrides — variable for testing.
var (
	containerdSockPath   = "/run/containerd/containerd.sock"
	kataShimName         = "containerd-shim-kata-v2"
	kataFCShimName       = "containerd-shim-kata-fc-v2"
	cniBridgePath        = "/opt/cni/bin/bridge"
	kvmDevPath           = "/dev/kvm"
	capNetAdminCheckFunc = hasCapNetAdmin
	wsl2CheckFunc        = isWSL2
	devmakerCheckFunc    = checkDevmakerSnapshotter
)

// checkDevmakerSnapshotter probes the devmapper snapshotter by calling Stat with a
// non-existent key. A "not found" error means the snapshotter is registered and working;
// any other error means it is not configured.
func checkDevmakerSnapshotter(ctx context.Context, r *Runtime) error {
	ctx = r.withNamespace(ctx)
	_, err := r.client.SnapshotService("devmapper").Stat(ctx, "probe")
	if err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("devmapper snapshotter not configured: %w\n"+
			"    Run the devmapper setup script and restart containerd", err)
	}
	return nil
}

// ValidateIsolation checks that all prerequisites for VM isolation are satisfied.
// Implements runtime.IsolationValidator.
func (r *Runtime) ValidateIsolation(ctx context.Context, isolation string) error {
	var missing []string

	// Use net.Dial to test actual connectivity — os.Open on a socket returns
	// ENXIO (not EACCES) on Linux, so it can't distinguish permission from absence.
	if conn, err := net.Dial("unix", containerdSockPath); err != nil {
		if os.IsPermission(err) || strings.Contains(err.Error(), "permission denied") {
			missing = append(missing, "no permission to access containerd socket\n"+
				"    Option 1 (simplest): run yoloai with sudo\n"+
				"    Option 2: configure containerd socket group access (run as root or with sudo):\n"+
				"      sudo groupadd -f containerd\n"+
				"      sudo usermod -aG containerd $USER\n"+
				"      GID=$(getent group containerd | cut -d: -f3)\n"+
				"      sudo mkdir -p /etc/containerd\n"+
				"      printf '\\n[grpc]\\n  gid = %s\\n' \"$GID\" | sudo tee -a /etc/containerd/config.toml\n"+
				"      sudo systemctl restart containerd\n"+
				"      newgrp containerd   # activate without logging out")
		} else {
			missing = append(missing, fmt.Sprintf("containerd socket not found at %s\n    Fix: sudo systemctl start containerd", containerdSockPath))
		}
	} else {
		_ = conn.Close()
	}

	shimName := kataShimName
	if isolation == "vm-enhanced" {
		shimName = kataFCShimName
	}
	if _, err := exec.LookPath(shimName); err != nil {
		missing = append(missing, "kata shim not found: install kata-containers")
	}

	if _, err := os.Stat(cniBridgePath); err != nil {
		missing = append(missing, "CNI plugins not found: sudo apt install containernetworking-plugins")
	}

	if !capNetAdminCheckFunc() {
		missing = append(missing, "CAP_NET_ADMIN not available (required to create network namespaces for CNI)\n    Fix: run yoloai with sudo for vm isolation")
	}

	if _, err := os.Stat(kvmDevPath); err != nil {
		if wsl2CheckFunc() {
			missing = append(missing, "nested virtualization not enabled — see WSL2 nested virt steps in docs")
		} else {
			missing = append(missing, "/dev/kvm not found: enable KVM in BIOS or check hypervisor settings")
		}
	}

	if isolation == "vm-enhanced" {
		if err := devmakerCheckFunc(ctx, r); err != nil {
			missing = append(missing, err.Error())
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("VM isolation mode requires additional setup:\n  - %s",
			strings.Join(missing, "\n  - "))
	}
	return nil
}

// isWSL2 returns true if running inside a WSL2 environment.
func isWSL2() bool {
	data, _ := os.ReadFile("/proc/version")
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}

// procSelfStatusPath is the path to /proc/self/status. Variable for testing.
var procSelfStatusPath = "/proc/self/status"

// hasCapNetAdmin reports whether the current process has CAP_NET_ADMIN
// (bit 12 of CapEff in /proc/self/status). Required to create network namespaces.
func hasCapNetAdmin() bool {
	data, err := os.ReadFile(procSelfStatusPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "CapEff:") {
			hexStr := strings.TrimSpace(strings.TrimPrefix(line, "CapEff:"))
			caps, err := strconv.ParseUint(hexStr, 16, 64)
			if err != nil {
				return false
			}
			const capNetAdmin = 12
			return caps&(1<<capNetAdmin) != 0
		}
	}
	return false
}
