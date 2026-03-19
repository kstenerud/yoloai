// Package containerdrt implements the runtime.Runtime interface using containerd.
// ABOUTME: Manages container/VM lifecycle via the containerd API for Kata Containers (vm isolation).
package containerdrt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

	if f, err := os.Open("/run/containerd/containerd.sock"); err != nil {
		if os.IsPermission(err) {
			missing = append(missing, "no permission to access containerd socket\n"+
				"    Option 1 (simplest): run yoloai with sudo\n"+
				"    Option 2: create a group and configure containerd to use it:\n"+
				"      sudo groupadd containerd\n"+
				"      sudo usermod -aG containerd $USER\n"+
				"      # add to /etc/containerd/config.toml: [grpc] gid = <containerd-gid>\n"+
				"      sudo systemctl restart containerd\n"+
				"      (then log out and back in, or: newgrp containerd)")
		} else {
			missing = append(missing, "containerd socket not found at /run/containerd/containerd.sock\n    Fix: sudo systemctl start containerd")
		}
	} else {
		f.Close()
	}

	if _, err := exec.LookPath("containerd-shim-kata-v2"); err != nil {
		missing = append(missing, "kata shim not found: install kata-containers")
	}

	if _, err := os.Stat("/opt/cni/bin/bridge"); err != nil {
		missing = append(missing, "CNI plugins not found: sudo apt install containernetworking-plugins")
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
