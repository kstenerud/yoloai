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
	"slices"
	"strings"

	"github.com/docker/docker/api/types"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
	"github.com/kstenerud/yoloai/internal/runtime/docker"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/yoerrors"
)

// descriptor holds the static facts for the podman backend; shared by the
// registry registration and the Runtime.Descriptor() method.
var descriptor = runtime.BackendDescriptor{
	Type:                      runtime.BackendPodman,
	Description:               "Linux containers; daemonless, rootless by default",
	Platforms:                 []string{"linux", "darwin"},
	Requires:                  "Podman installed with API socket activated",
	InstallHint:               "https://podman.io/docs/installation",
	BaseModeName:              runtime.IsolationModeContainer,
	AgentProvisionedByBackend: true,
	AgentInstallMethod:        "npm-global",
	SupportedIsolationModes:   []runtime.IsolationMode{runtime.IsolationModeContainerEnhanced, runtime.IsolationModeContainerPrivileged},
	Capabilities: runtime.BackendCaps{
		NetworkIsolation:   true,
		OverlayDirs:        true,
		CapAdd:             true,
		HostFilesystem:     false,
		FilesystemLocality: runtime.LocalityHostSide,
		ContainerAttach:    true,
	},
	Probe:             probe,
	CleanupHint:       func(image string) string { return "podman rmi " + image },
	HostFromContainer: "host.docker.internal",
	VersionString:     versionString,
}

// versionString returns the podman client version string from `podman version`.
// Uses a minimal explicit env (PATH only) per DEV §12 — version probes need no secrets.
func versionString(ctx context.Context) string {
	env := sysexec.Curated(nil, []string{"PATH"}, nil)
	out, err := sysexec.CommandContext(ctx, env, "podman", "version", "--format", "{{.Client.Version}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// probe reports whether Podman is usable. discoverSocket is stat-only across
// the known socket paths plus CONTAINER_HOST/DOCKER_HOST/podman machine; no
// dial, matching docker's probe contract.
func probe(_ context.Context, env map[string]string) (runtime.ProbeStatus, string) {
	if _, err := discoverSocket(env); err == nil {
		return runtime.ProbeRunning, ""
	}
	if _, err := exec.LookPath("podman"); err == nil {
		return runtime.ProbeInstalled, "podman installed but socket not reachable ('podman machine start' or 'systemctl --user start podman.socket')"
	}
	return runtime.ProbeAbsent, "podman not found (install from https://podman.io/docs/installation)"
}

func init() {
	runtime.Register(func(ctx context.Context, layout config.Layout) (runtime.Runtime, error) {
		return New(ctx, layout)
	}, descriptor)
}

// Runtime implements runtime.Runtime by embedding the Docker runtime
// and connecting to Podman's Docker-compatible socket.
type Runtime struct {
	*docker.Runtime
	rootless bool // true when connected to a rootless (user) Podman socket

	// Capability fields — built once in New(), returned by RequiredCapabilities.
	rootlessCheck caps.HostCapability
	gvisorRunsc   caps.HostCapability
}

// Compile-time checks.
var _ runtime.Runtime = (*Runtime)(nil)
var _ runtime.UsernsProvider = (*Runtime)(nil)
var _ runtime.IsolationCapabilityProvider = (*Runtime)(nil)
var _ runtime.CachePruner = (*Runtime)(nil)       // inherited from embedded docker.Runtime
var _ runtime.DiskUsageReporter = (*Runtime)(nil) // inherited; image bytes via podmanImageBytes (LayersSize=0 workaround)

// New creates a Podman Runtime by discovering the Podman socket and
// connecting via the Docker SDK.
func New(ctx context.Context, layout config.Layout) (*Runtime, error) {
	if _, err := exec.LookPath("podman"); err != nil {
		return nil, yoerrors.NewDependencyError("podman is not installed, install it from https://podman.io/docs/installation")
	}

	sock, err := discoverSocket(layout.Env().EnvForDaemonDiscovery())
	if err != nil {
		return nil, yoerrors.NewDependencyError("podman socket not found: %w\nhint: run 'systemctl --user start podman.socket' or 'podman machine start'", err)
	}

	dockerRT, err := docker.NewWithSocket(ctx, sock, "podman", layout)
	if err != nil {
		return nil, fmt.Errorf("connect to podman: %w", err)
	}

	rootless := socketIsRootless(sock)
	r := &Runtime{Runtime: dockerRT, rootless: rootless}
	r.rootlessCheck = buildRootlessCheckCap(rootless)
	r.gvisorRunsc = caps.NewGVisorRunsc(runscLookPath)

	// Podman's docker-compat /system/df reports LayersSize=0, so the inherited
	// docker CacheUsage would report 0 image bytes. Inject a per-image dedup.
	dockerRT.SetImageBytesFunc(podmanImageBytes)
	return r, nil
}

// podmanImageBytes computes the deduplicated on-disk image-layer total for
// Podman from its per-image Size/SharedSize fields, since Podman's
// docker-compat /system/df returns the aggregate LayersSize as 0.
//
// Summing each image's Size multiply-counts shared layers (38 build stages
// sharing one ~5.5 GiB base read as ~150 GiB). The deduplicated total is the
// sum of every image's unique bytes plus the shared layer set counted once.
// SharedSize is per-image "bytes shared with ≥1 other image"; for yoloai's
// single-base build chain the largest SharedSize captures the full shared
// union, so total ≈ Σ(Size − SharedSize) + max(SharedSize). With multiple
// independent bases this slightly underestimates the shared tier.
func podmanImageBytes(du types.DiskUsage) int64 {
	var unique, maxShared int64
	for _, img := range du.Images {
		if img == nil {
			continue
		}
		unique += img.Size - img.SharedSize
		if img.SharedSize > maxShared {
			maxShared = img.SharedSize
		}
	}
	return unique + maxShared
}

// Create wraps the Docker Create to inject --userns=keep-id for rootless mode.
// Exception: overlay mode requires CAP_SYS_ADMIN and root privileges inside the
// container, so we skip keep-id when SYS_ADMIN is in CapAdd.
//
// Privileged mode (dind) maps to the yoloai UID (1001) rather than the host
// user. Plain keep-id maps the host user 1:1, so the container runs as that user
// (e.g. UID 1000) — which lacks yoloai's passwordless sudo and docker-group
// membership, so `sudo dockerd` fails. keep-id:uid=1001,gid=1001 instead maps the
// host user onto yoloai: the agent gets sudo+docker AND host-written 0600 files
// (prompt, credentials) still map to a UID the agent owns, so they stay readable.
//
// keep-id is also skipped on macOS. Podman on macOS runs via Podman Machine (a
// Linux VM): keep-id maps the VM user (UID 1000) into the container, not the
// macOS user (e.g. UID 501). The container then runs as UID 1000, but
// /home/yoloai is owned by UID 1001 (yoloai), preventing agents from writing
// their config. Without keep-id the container starts as root, and entrypoint.py
// remaps yoloai to the macOS user's UID via gosu — the same path Docker takes.
func (r *Runtime) Create(ctx context.Context, cfg runtime.InstanceConfig) error {
	if r.rootless && cfg.UsernsMode == "" && goruntime.GOOS != "darwin" {
		// Check if overlay mode is active (indicated by SYS_ADMIN capability)
		hasOverlay := slices.Contains(cfg.CapAdd, "SYS_ADMIN")
		// Only use keep-id for normal mounts; overlay needs root in container
		if !hasOverlay {
			if cfg.Privileged {
				cfg.UsernsMode = "keep-id:uid=1001,gid=1001"
			} else {
				cfg.UsernsMode = "keep-id"
			}
		}
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
func discoverSocket(env map[string]string) (string, error) {
	// Check the caller's env snapshot first (§12: the daemon-socket location
	// is threaded data, not a live os.Getenv — see New's layout.CuratedEnv /
	// the probe's probeEnv boundary).
	if host := env["CONTAINER_HOST"]; host != "" {
		return host, nil
	}
	if host := env["DOCKER_HOST"]; host != "" {
		return host, nil
	}

	// Rootless socket via XDG_RUNTIME_DIR
	if xdg := env["XDG_RUNTIME_DIR"]; xdg != "" {
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
	sock, err := machineSocketDiscovery(env)
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
// env is the explicit subprocess env (DEV §12); pass the curated daemon env (layout.CuratedEnv) from caller.
var machineSocketDiscovery = defaultMachineSocketDiscovery

func defaultMachineSocketDiscovery(env map[string]string) (string, error) {
	// Minimal allowlist: PATH for binary lookup, CONTAINER_HOST and XDG_RUNTIME_DIR
	// for socket resolution on rootless Linux, TMPDIR because macOS `podman machine
	// inspect` derives the machine API socket path from it ($TMPDIR/podman/...) and
	// reports a stale /tmp fallback without it. HOME is deliberately dropped so
	// podman does not read the ambient home's containers.conf (DEV §12).
	podmanEnv := sysexec.Curated(env, []string{"PATH", "TMPDIR", "CONTAINER_HOST", "XDG_RUNTIME_DIR"}, nil)
	out, err := sysexec.Command(podmanEnv, "podman", "machine", "inspect",
		"--format", "{{.ConnectionInfo.PodmanSocket.Path}}").Output()
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

// Descriptor returns a BackendDescriptor with the static facts for this backend.
// Overrides the embedded docker.Runtime.Descriptor() to return the podman descriptor.
func (r *Runtime) Descriptor() runtime.BackendDescriptor {
	return descriptor
}

// RequiredCapabilities returns the host capabilities needed for the given isolation mode.
func (r *Runtime) RequiredCapabilities(isolation runtime.IsolationMode) []caps.HostCapability {
	switch isolation {
	case runtime.IsolationModeContainerEnhanced:
		// rootlessCheck first: it's a permanent blocker; surfacing it before
		// gvisorRunsc avoids a confusing "install runsc" suggestion when the
		// real answer is "rootless Podman can never run gVisor."
		return []caps.HostCapability{r.rootlessCheck, r.gvisorRunsc}
	default:
		return nil
	}
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
	if !r.rootless || hasSysAdmin || goruntime.GOOS == "darwin" {
		return ""
	}
	return "keep-id"
}

// socketIsRootless reports whether the given Podman socket URL points to a
// rootless (user-space) daemon. The system socket (/run/podman/podman.sock)
// is the only known non-rootless path; everything else (XDG_RUNTIME_DIR,
// WSL2, Podman Machine, user-supplied CONTAINER_HOST) is treated as rootless.
// Detect via the socket path, never os.Getuid(): under `sudo -E yoloai`
// Getuid() is 0 yet the socket is still the user's rootless one, and
// --userns=keep-id is wrong for a system socket but required for a rootless one.
func socketIsRootless(sock string) bool {
	path := strings.TrimPrefix(sock, "unix://")
	return path != systemSockPath
}
