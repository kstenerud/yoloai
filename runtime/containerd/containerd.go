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

// New connects to the containerd daemon and returns a Runtime.
// It does not validate isolation prerequisites — that is done via ValidateIsolation.
func New(_ context.Context) (*Runtime, error) {
	c, err := client.New("/run/containerd/containerd.sock")
	if err != nil {
		return nil, fmt.Errorf("connect to containerd: %w\n  Is containerd running? Try: sudo systemctl start containerd", err)
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

// Close releases the containerd client connection.
func (r *Runtime) Close() error { return r.client.Close() }

// ValidateIsolation checks that all prerequisites for VM isolation are satisfied.
// Implements runtime.IsolationValidator.
func (r *Runtime) ValidateIsolation(_ context.Context, isolation string) error {
	var missing []string

	// Use net.Dial to test actual connectivity — os.Open on a socket returns
	// ENXIO (not EACCES) on Linux, so it can't distinguish permission from absence.
	if conn, err := net.Dial("unix", "/run/containerd/containerd.sock"); err != nil {
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
			missing = append(missing, "containerd socket not found at /run/containerd/containerd.sock\n    Fix: sudo systemctl start containerd")
		}
	} else {
		_ = conn.Close()
	}

	if _, err := exec.LookPath("containerd-shim-kata-v2"); err != nil {
		missing = append(missing, "kata shim not found: install kata-containers")
	}

	if _, err := os.Stat("/opt/cni/bin/bridge"); err != nil {
		missing = append(missing, "CNI plugins not found: sudo apt install containernetworking-plugins")
	}

	if !hasCapNetAdmin() {
		missing = append(missing, "CAP_NET_ADMIN not available (required to create network namespaces for CNI)\n    Fix: run yoloai with sudo for vm isolation")
	}

	if _, err := os.Stat("/dev/kvm"); err != nil {
		if isWSL2() {
			missing = append(missing, "nested virtualization not enabled — see WSL2 nested virt steps in docs")
		} else {
			missing = append(missing, "/dev/kvm not found: enable KVM in BIOS or check hypervisor settings")
		}
	}

	// vm-enhanced devmapper check deferred to Phase 3
	_ = isolation

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

// hasCapNetAdmin reports whether the current process has CAP_NET_ADMIN
// (bit 12 of CapEff in /proc/self/status). Required to create network namespaces.
func hasCapNetAdmin() bool {
	data, err := os.ReadFile("/proc/self/status")
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
