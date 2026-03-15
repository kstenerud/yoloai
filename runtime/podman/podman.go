// Package podman implements the runtime.Runtime interface using Podman's
// Docker-compatible API socket. It embeds the Docker runtime and overrides
// only what differs.
// ABOUTME: Podman backend — wraps Docker runtime with Podman socket discovery and rootless support.
package podman

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/docker"
)

// Runtime implements runtime.Runtime by embedding the Docker runtime
// and connecting to Podman's Docker-compatible socket.
type Runtime struct {
	*docker.Runtime
}

// Compile-time check.
var _ runtime.Runtime = (*Runtime)(nil)

// New creates a Podman Runtime by discovering the Podman socket and
// connecting via the Docker SDK.
func New(ctx context.Context) (*Runtime, error) {
	if _, err := exec.LookPath("podman"); err != nil {
		return nil, fmt.Errorf("podman is not installed, install it from https://podman.io/docs/installation")
	}

	sock, err := discoverSocket()
	if err != nil {
		return nil, fmt.Errorf("podman socket not found: %w\nhint: run 'systemctl --user start podman.socket' or 'podman machine start'", err)
	}

	dockerRT, err := docker.NewWithSocket(ctx, sock, "podman")
	if err != nil {
		return nil, fmt.Errorf("connect to podman: %w", err)
	}

	return &Runtime{Runtime: dockerRT}, nil
}

// Create wraps the Docker Create to inject --userns=keep-id for rootless mode.
func (r *Runtime) Create(ctx context.Context, cfg runtime.InstanceConfig) error {
	if isRootless() && cfg.UsernsMode == "" {
		cfg.UsernsMode = "keep-id"
	}
	return r.Runtime.Create(ctx, cfg)
}

// discoverSocket finds the Podman API socket path.
// Search order:
//  1. $CONTAINER_HOST env var
//  2. $DOCKER_HOST env var
//  3. $XDG_RUNTIME_DIR/podman/podman.sock (rootless)
//  4. /run/podman/podman.sock (system-wide)
//  5. macOS: `podman machine inspect` (Podman Machine)
func discoverSocket() (string, error) {
	// Check env vars first
	if host := os.Getenv("CONTAINER_HOST"); host != "" {
		return host, nil
	}
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		return host, nil
	}

	// Rootless socket via XDG_RUNTIME_DIR
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		sock := filepath.Join(xdg, "podman", "podman.sock")
		if _, err := os.Stat(sock); err == nil { //nolint:gosec // G703: path is from trusted env var
			return "unix://" + sock, nil
		}
	}

	// System-wide socket
	const systemSock = "/run/podman/podman.sock"
	if _, err := os.Stat(systemSock); err == nil {
		return "unix://" + systemSock, nil
	}

	// macOS: try podman machine inspect
	sock, err := discoverMachineSocket()
	if err == nil {
		return sock, nil
	}

	return "", fmt.Errorf("no podman socket found (checked $CONTAINER_HOST, $DOCKER_HOST, $XDG_RUNTIME_DIR/podman/podman.sock, /run/podman/podman.sock)")
}

// discoverMachineSocket tries to get the socket path from `podman machine inspect`.
func discoverMachineSocket() (string, error) {
	out, err := exec.Command("podman", "machine", "inspect", "--format", "{{.ConnectionInfo.PodmanSocket.Path}}").Output() //nolint:gosec // trusted binary path
	if err != nil {
		return "", err
	}
	sock := strings.TrimSpace(string(out))
	if sock == "" || sock == "<no value>" {
		return "", fmt.Errorf("podman machine inspect returned empty socket path")
	}
	if _, err := os.Stat(sock); err != nil {
		return "", fmt.Errorf("podman machine socket not found: %s", sock)
	}
	return "unix://" + sock, nil
}

// isRootless returns true when not running as root, indicating Podman
// rootless mode where --userns=keep-id is needed for correct file ownership.
var isRootless = defaultIsRootless

func defaultIsRootless() bool {
	return os.Getuid() != 0
}
