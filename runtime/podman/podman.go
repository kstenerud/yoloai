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
	goruntime "runtime"
	"strings"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/docker"
)

// Runtime implements runtime.Runtime by embedding the Docker runtime
// and connecting to Podman's Docker-compatible socket.
type Runtime struct {
	*docker.Runtime
}

// Compile-time checks.
var _ runtime.Runtime = (*Runtime)(nil)
var _ runtime.IsolationValidator = (*Runtime)(nil)
var _ runtime.UsernsProvider = (*Runtime)(nil)

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
// Exception: overlay mode requires CAP_SYS_ADMIN and root privileges inside the
// container, so we skip keep-id when SYS_ADMIN is in CapAdd.
//
// keep-id is also skipped on macOS. Podman on macOS runs via Podman Machine (a
// Linux VM): keep-id maps the VM user (UID 1000) into the container, not the
// macOS user (e.g. UID 501). The container then runs as UID 1000, but
// /home/yoloai is owned by UID 1001 (yoloai), preventing agents from writing
// their config. Without keep-id the container starts as root, and entrypoint.py
// remaps yoloai to the macOS user's UID via gosu — the same path Docker takes.
func (r *Runtime) Create(ctx context.Context, cfg runtime.InstanceConfig) error {
	if isRootless() && cfg.UsernsMode == "" && goruntime.GOOS != "darwin" {
		// Check if overlay mode is active (indicated by SYS_ADMIN capability)
		hasOverlay := false
		for _, cap := range cfg.CapAdd {
			if cap == "SYS_ADMIN" {
				hasOverlay = true
				break
			}
		}
		// Only use keep-id for normal mounts; overlay needs root in container
		if !hasOverlay {
			cfg.UsernsMode = "keep-id"
		}
	}
	return r.Runtime.Create(ctx, cfg)
}

// SocketExists returns true if a Podman socket can be found without dialing it.
// Used by backend auto-detection in cli/helpers.go.
func SocketExists() bool {
	_, err := discoverSocket()
	return err == nil
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
	if _, err := os.Stat(systemSockPath); err == nil {
		return "unix://" + systemSockPath, nil
	}

	// WSL2: Podman Desktop on Windows exposes sockets under /mnt/wsl
	for _, p := range wsl2SockPaths {
		if _, err := os.Stat(p); err == nil { //nolint:gosec // G703: fixed known paths
			return "unix://" + p, nil
		}
	}

	// macOS: try podman machine inspect
	sock, err := machineSocketDiscovery()
	if err == nil {
		return sock, nil
	}

	return "", fmt.Errorf("no podman socket found (checked $CONTAINER_HOST, $DOCKER_HOST, $XDG_RUNTIME_DIR/podman/podman.sock, /run/podman/podman.sock)")
}

// systemSockPath is the system-wide Podman socket path. Variable for testing.
var systemSockPath = "/run/podman/podman.sock"

// wsl2SockPaths lists the Podman socket paths exposed by Podman Desktop on
// Windows via the WSL2 machine provider. Variable for testing.
var wsl2SockPaths = []string{
	"/mnt/wsl/podman-sockets/podman-machine-default/podman-root.sock",
	"/mnt/wsl/podman-sockets/podman-machine-default/podman-user.sock",
}

// runscLookPath resolves the runsc binary path. Variable for testing.
var runscLookPath = exec.LookPath

// machineSocketDiscovery tries to get the socket path from `podman machine inspect`.
// Variable for testing - can be mocked to avoid executing podman commands.
var machineSocketDiscovery = defaultMachineSocketDiscovery

func defaultMachineSocketDiscovery() (string, error) {
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

// ValidateIsolation checks that the host has the required runtime for the
// given isolation mode. For container-enhanced (gVisor), it verifies that
// the "runsc" OCI runtime is in containers.conf and that Podman is not
// running in rootless mode (rootless Podman + runsc fails due to cgroup
// delegation issues).
func (r *Runtime) ValidateIsolation(_ context.Context, isolation string) error {
	if isolation != "container-enhanced" {
		return nil
	}
	if isRootless() {
		return fmt.Errorf("--isolation container-enhanced requires Podman running as root\n" +
			"  gVisor (runsc) with rootless Podman fails due to cgroup v2 delegation\n" +
			"  Run as root or use Docker for container-enhanced isolation")
	}
	if _, err := runscLookPath("runsc"); err != nil {
		return fmt.Errorf("--isolation container-enhanced requires gVisor (runsc) in PATH\n" +
			"  Install: https://gvisor.dev/docs/user_guide/install/\n" +
			"  Then add to /etc/containers/containers.conf: [engine.runtimes] runsc = [\"/usr/local/sbin/runsc\"]")
	}
	return nil
}

// UsernsMode returns the user namespace mode for a new container.
// Rootless Podman on Linux uses "keep-id" to map container uid to the host
// user, which is required for correct file ownership. Exceptions:
//   - hasSysAdmin=true: overlay mounts require real root in the container
//   - macOS: Podman Machine maps the VM user (uid 1000) into the container,
//     not the macOS user. Without keep-id, entrypoint.py remaps yoloai to
//     the correct uid via gosu — the same path Docker takes.
//   - root: keep-id is irrelevant when already running as root.
func (r *Runtime) UsernsMode(hasSysAdmin bool) string {
	if !isRootless() || hasSysAdmin || goruntime.GOOS == "darwin" {
		return ""
	}
	return "keep-id"
}

// isRootless returns true when not running as root, indicating Podman
// rootless mode where --userns=keep-id is needed for correct file ownership.
var isRootless = defaultIsRootless

func defaultIsRootless() bool {
	return os.Getuid() != 0
}
