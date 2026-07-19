// Package podman implements the runtime.Backend interface using Podman's
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
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
	"github.com/kstenerud/yoloai/runtime/docker"
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
	SupportedIsolationModes:   []runtime.IsolationMode{runtime.IsolationModeContainerEnhanced, runtime.IsolationModeContainerPrivileged},
	Capabilities: runtime.BackendCaps{
		// IPv4 only — see the note in runtime/docker/docker.go (DF104).
		NetworkIsolation:     true,
		CapAdd:               true,
		HostFilesystem:       false,
		FilesystemLocality:   runtime.LocalityHostSide,
		GitExecInConfinement: true, // copy-mode work-copy git runs in-container (audit C1); GitExec inherited from docker.Runtime
		ContainerAttach:      true,
		KeepAliveModel:       runtime.KeepAliveContainerInit,
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
	runtime.Register(func(ctx context.Context, layout config.Layout) (runtime.Backend, error) {
		return New(ctx, layout)
	}, descriptor)
}

// Runtime implements runtime.Backend by embedding the Docker runtime
// and connecting to Podman's Docker-compatible socket.
type Runtime struct {
	*docker.Runtime
	rootless bool // true when connected to a rootless (user) Podman socket

	// Capability fields — built once in New(), returned by RequiredCapabilities.
	rootlessCheck    caps.HostCapability
	gvisorRunsc      caps.HostCapability
	crunVersionFloor caps.HostCapability
}

// Compile-time checks.
var _ runtime.Backend = (*Runtime)(nil)
var _ runtime.UsernsProvider = (*Runtime)(nil)
var _ runtime.IsolationCapabilityProvider = (*Runtime)(nil)
var _ runtime.InteractiveSession = (*Runtime)(nil)
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
	r.crunVersionFloor = buildCrunVersionFloorCap()

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
// Exception: recipe/profile cap_add may request CAP_SYS_ADMIN, which requires
// real root in the container, so we skip keep-id when SYS_ADMIN is in CapAdd.
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
		// SYS_ADMIN (from recipe/profile cap_add) requires real root in container.
		hasSysAdmin := slices.Contains(cfg.CapAdd, "SYS_ADMIN")
		// Only use keep-id when SYS_ADMIN is not requested. Map the host user onto
		// the image's yoloai user (UID 1001), not 1:1 — privileged and
		// non-privileged alike. Plain keep-id would run the agent as the host UID
		// (e.g. 1000), but /home/yoloai and the seeded agent config are owned by
		// yoloai (1001), and the entrypoint UID-remap only runs when the container
		// starts as root (it doesn't under keep-id). The agent then can't read its
		// own home config — for Claude Code that means its folder-trust/onboarding/
		// permissions gating blocks on interactive dialogs and the sandbox hangs.
		// Mapping onto 1001 also gives the agent yoloai's passwordless sudo +
		// docker-group membership, and host-written 0600 files (prompt, credentials)
		// still map to a UID the agent owns.
		if !hasSysAdmin {
			cfg.UsernsMode = "keep-id:uid=1001,gid=1001"
		}
	}
	return r.Runtime.Create(ctx, cfg)
}

// discoverSocket finds the Podman API socket path.
// Search order:
//  1. $CONTAINER_HOST env var (podman's own explicit socket override)
//  2. $XDG_RUNTIME_DIR/podman/podman.sock (rootless)
//  3. /run/podman/podman.sock (system-wide)
//  4. WSL2 podman-machine sockets
//  5. macOS: `podman machine inspect` (Podman Machine)
//  6. $DOCKER_HOST env var (LAST-RESORT fallback)
//
// $DOCKER_HOST is checked LAST, after every native podman socket, on purpose:
// it is docker's variable, and in a mixed host it points at the DOCKER daemon.
// Honoring it ahead of the real podman socket (the original bug) silently routed
// the podman backend to docker — where podman-only container options like the
// rootless slirp4netns network mode don't exist, so a brokered sandbox's
// container failed to start with a misleading "instance not found". A user who
// genuinely wants to target a remote/alternate podman uses $CONTAINER_HOST (which
// still wins). $DOCKER_HOST stays only as a last resort for the "podman emulating
// docker via DOCKER_HOST, no native socket present" setup.
func discoverSocket(env map[string]string) (string, error) {
	// Check the caller's env snapshot first (§12: the daemon-socket location
	// is threaded data, not a live os.Getenv — see New's layout.CuratedEnv /
	// the probe's probeEnv boundary).
	if host := env["CONTAINER_HOST"]; host != "" {
		return host, nil
	}

	// Rootless socket via XDG_RUNTIME_DIR
	if xdg := env["XDG_RUNTIME_DIR"]; xdg != "" {
		sock := filepath.Join(xdg, "podman", "podman.sock")
		if _, err := os.Stat(sock); err == nil {
			return "unix://" + sock, nil
		}
	}

	// System-wide socket
	if _, err := os.Stat(systemSockPath); err == nil {
		return "unix://" + systemSockPath, nil
	}

	// WSL2: Podman Desktop on Windows exposes sockets under /mnt/wsl
	for _, p := range wsl2SockPaths {
		if _, err := os.Stat(p); err == nil {
			return "unix://" + p, nil
		}
	}

	// macOS: try podman machine inspect
	sock, err := machineSocketDiscovery(env)
	if err == nil {
		return sock, nil
	}

	// Last resort: $DOCKER_HOST (a docker pointer in a mixed host; only trustworthy
	// for podman when no native podman socket exists — see the doc comment).
	if host := env["DOCKER_HOST"]; host != "" {
		return host, nil
	}

	return "", fmt.Errorf("no podman socket found (checked $CONTAINER_HOST, $XDG_RUNTIME_DIR/podman/podman.sock, /run/podman/podman.sock, $DOCKER_HOST)")
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
		return "", fmt.Errorf("podman machine inspect: %w", sysexec.EnrichExitError(err))
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
	case runtime.IsolationModeDefault, runtime.IsolationModeContainer, runtime.IsolationModeContainerPrivileged:
		return []caps.HostCapability{r.crunVersionFloor}
	default:
		return nil
	}
}

// UsernsMode returns the user namespace mode for a new container.
// Rootless Podman on Linux uses "keep-id" to map container uid to the host
// user, which is required for correct file ownership. Exceptions:
//   - hasSysAdmin=true: recipe/profile cap_add requests CAP_SYS_ADMIN, which
//     requires real root in the container
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
