// Package docker implements the runtime.Runtime interface using Docker SDK.
// ABOUTME: Wraps Docker SDK client for container lifecycle, exec, and image ops.
package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	goruntime "runtime"

	cerrdefs "github.com/containerd/errdefs"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-connections/tlsconfig"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
	"github.com/kstenerud/yoloai/internal/sysexec"
	"github.com/kstenerud/yoloai/yoerrors"
)

// descriptor holds the static facts for the docker backend; shared by the
// registry registration and the Runtime.Descriptor() method.
var descriptor = runtime.BackendDescriptor{
	Type:                      runtime.BackendDocker,
	Description:               "Linux containers; portable, lightweight, fast",
	Platforms:                 []string{"linux", "darwin", "windows"},
	Requires:                  "Docker Engine or Docker Desktop installed and running",
	InstallHint:               "https://docs.docker.com/get-docker/",
	BaseModeName:              runtime.IsolationModeContainer,
	AgentProvisionedByBackend: true,
	AgentInstallMethod:        "npm-global",
	SupportedIsolationModes:   []runtime.IsolationMode{runtime.IsolationModeContainerEnhanced, runtime.IsolationModeContainerPrivileged},
	Capabilities: runtime.BackendCaps{
		NetworkIsolation: true,
		OverlayDirs:      true,
		CapAdd:           true,
		HostFilesystem:   false,
		ContainerAttach:  true,
	},
	Probe:             probe,
	CleanupHint:       func(image string) string { return "docker rmi " + image },
	HostFromContainer: "host.docker.internal",
	VersionString:     versionString,
}

// probe reports whether Docker is usable. Stat-only — never dials the socket —
// because it runs on every `yoloai info` call and inside auto-detect dispatch.
// It mirrors the connection priority: an explicit DOCKER_HOST or a resolvable
// active-context endpoint is a positive signal; otherwise any well-known local
// socket existing on disk counts (the default socket may be a stale symlink, so
// a sibling provider's socket is an equally valid signal).
func probe(_ context.Context, env map[string]string) (bool, string) {
	if env["DOCKER_HOST"] != "" {
		return true, ""
	}
	if host := resolveDockerHost(env); sockExists(host) {
		return true, ""
	}
	for _, cand := range wellKnownDockerSockets(env) {
		if sockExists(cand) {
			return true, ""
		}
	}
	return false, "docker socket not found (set DOCKER_HOST or start the docker daemon)"
}

// versionString runs `docker version` and returns a "Client: X / Server: Y"
// summary. Returns "" if the docker binary is missing or the daemon is
// unreachable — callers (bug reports, yoloai info) treat empty as "no
// version known" and fall back to the probe's availability verdict.
// Uses a minimal explicit env (PATH only) per DEV §12 — version probes need no secrets.
func versionString(ctx context.Context) string {
	env := sysexec.Curated(nil, []string{"PATH"}, nil)
	out, err := sysexec.CommandContext(ctx, env, "docker", "version", "--format",
		"Client: {{.Client.Version}} / Server: {{.Server.Version}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func init() {
	runtime.Register(func(ctx context.Context, layout config.Layout) (runtime.Runtime, error) {
		return New(ctx, layout)
	}, descriptor)
}

// Runtime implements runtime.Runtime using the Docker SDK.
type Runtime struct {
	client     *dockerclient.Client
	binaryName string                  // CLI binary name ("docker" or "podman")
	principal  config.PrincipalSegment // namespaces the orphan sweep (DF19)
	execEnv    []string                // explicit subprocess env (DEV §12); from layout, never inherited

	// imageBytesFn computes the rebuild-forcing image-layer total from a
	// DiskUsage snapshot. nil means "use du.LayersSize" (the daemon's
	// deduplicated layer-store total). Podman injects its own because its
	// docker-compat API reports LayersSize=0 (see backend-idiosyncrasies.md).
	imageBytesFn imageBytesFunc

	// Capability fields — built once in New(), returned by RequiredCapabilities.
	gvisorRunsc      caps.HostCapability
	gvisorRegistered caps.HostCapability
}

// Compile-time check.
var _ runtime.Runtime = (*Runtime)(nil)
var _ runtime.IsolationCapabilityProvider = (*Runtime)(nil)
var _ runtime.CachePruner = (*Runtime)(nil)
var _ runtime.DiskUsageReporter = (*Runtime)(nil)

// New creates a Runtime and verifies the Docker daemon is reachable. layout
// carries the threaded environment snapshot; the daemon socket and TLS settings
// are read from the curated daemon subset (layout.CuratedEnv) rather than
// os.Environ (§12). An empty curated subset means "default socket, no TLS" —
// exactly the SDK's behavior when the DOCKER_* vars are unset.
func New(ctx context.Context, layout config.Layout) (*Runtime, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, yoerrors.NewDependencyError("docker is not installed, install it from https://docs.docker.com/get-docker/")
	}
	return NewWithSocket(ctx, "", "docker", layout)
}

// NewWithSocket creates a Runtime connected to a specific Docker-compatible socket.
// If host is non-empty it pins the connection to that socket. If host is empty,
// the client is configured from the curated daemon subset of the threaded snapshot
// (layout.CuratedEnv: DOCKER_HOST / DOCKER_CERT_PATH / DOCKER_TLS_VERIFY /
// DOCKER_API_VERSION), not os.Environ (§12). binaryName is the CLI binary to use
// for interactive exec and image builds (e.g., "docker" or "podman").
func NewWithSocket(ctx context.Context, host string, binaryName string, layout config.Layout) (*Runtime, error) {
	env := layout.Env().EnvForDaemonDiscovery()
	baseOpts := []dockerclient.Opt{dockerclient.WithAPIVersionNegotiation()}
	tlsOpts, err := tlsOptsFromEnv(env)
	if err != nil {
		return nil, fmt.Errorf("configure docker client from env: %w", err)
	}
	baseOpts = append(baseOpts, tlsOpts...)

	// An explicit host (e.g. the podman backend pinning its discovered socket)
	// is used verbatim. Otherwise resolve the endpoint the way the docker CLI
	// does — honoring the active context, not just the default socket.
	explicit := host != ""
	if !explicit {
		host = resolveDockerHost(env)
	}

	cli, pingErr := dialDocker(ctx, baseOpts, host)
	if pingErr == nil {
		return newDockerRuntime(cli, binaryName, layout), nil
	}

	// Self-heal the auto path only: if the resolved socket is dead, adopt the
	// first well-known daemon that answers. Covers the stale
	// /var/run/docker.sock symlink left behind when switching Docker providers
	// (e.g. OrbStack ⇄ Docker Desktop) without a `docker context use`.
	if !explicit {
		if cli, used := dialFirstAlive(ctx, baseOpts, env, host); cli != nil {
			slog.Warn("docker daemon unreachable at resolved socket; using a live fallback",
				"binary", binaryName, "resolved", displayHost(host), "using", used)
			return newDockerRuntime(cli, binaryName, layout), nil
		}
	}

	return nil, pingFailureError(pingErr, binaryName)
}

// dialDocker builds a client for host ("" = SDK default socket) and verifies
// the daemon answers Ping. On failure it closes the client and returns the
// error so the caller can try an alternative.
func dialDocker(ctx context.Context, baseOpts []dockerclient.Opt, host string) (*dockerclient.Client, error) {
	opts := baseOpts
	if host != "" {
		opts = append(slices.Clone(baseOpts), dockerclient.WithHost(host))
	}
	cli, err := dockerclient.NewClientWithOpts(opts...)
	if err != nil {
		return nil, err
	}
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		return nil, err
	}
	return cli, nil
}

// dialFirstAlive probes the well-known local sockets (skipping skip and any
// that don't exist on disk) under a short per-candidate timeout, returning the
// first live client and the host it used. Returns nil if none answer.
func dialFirstAlive(ctx context.Context, baseOpts []dockerclient.Opt, env map[string]string, skip string) (*dockerclient.Client, string) {
	for _, cand := range wellKnownDockerSockets(env) {
		if cand == skip || !sockExists(cand) {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		cli, err := dialDocker(cctx, baseOpts, cand)
		cancel()
		if err == nil {
			return cli, cand
		}
	}
	return nil, ""
}

func newDockerRuntime(cli *dockerclient.Client, binaryName string, layout config.Layout) *Runtime {
	execEnv := layout.Env().EnvForDockerExec()
	r := &Runtime{client: cli, binaryName: binaryName, principal: layout.Principal, execEnv: execEnv}
	r.gvisorRunsc = caps.NewGVisorRunsc(exec.LookPath)
	r.gvisorRegistered = buildGVisorRegisteredCap(execEnv, binaryName)
	return r
}

func pingFailureError(err error, binaryName string) error {
	if runtime.IsPermissionDenied(err) {
		return yoerrors.NewPermissionError("%s socket permission denied: add your user to the %s group or run with sudo", binaryName, binaryName)
	}
	var hint string
	switch binaryName {
	case "podman":
		hint = "start Podman Desktop or run 'systemctl --user start podman.socket'"
	default:
		hint = "start Docker Desktop or run 'sudo systemctl start docker'"
	}
	// Wrap the underlying ping error (%w) rather than discarding it: the hint is
	// the common cause, but when it isn't, the real error is the only thing that
	// explains the failure.
	return yoerrors.NewDependencyError("%s daemon is not responding (%w); %s", binaryName, err, hint)
}

// tlsOptsFromEnv reproduces the TLS and API-version halves of
// dockerclient.FromEnv, sourced from the threaded env snapshot rather than
// os.Environ (§12). The host/socket selection is handled separately by
// resolveDockerHost. An empty DOCKER_CERT_PATH means no TLS and an empty
// DOCKER_API_VERSION means version negotiation, so a nil/blank env degrades to
// a plain local connection.
func tlsOptsFromEnv(env map[string]string) ([]dockerclient.Opt, error) {
	var opts []dockerclient.Opt

	// TLS first, mirroring FromEnv's WithTLSClientConfigFromEnv: only engaged
	// when DOCKER_CERT_PATH is set; DOCKER_TLS_VERIFY toggles verification.
	if certPath := env["DOCKER_CERT_PATH"]; certPath != "" {
		tlsc, err := tlsconfig.Client(tlsconfig.Options{
			CAFile:             filepath.Join(certPath, "ca.pem"),
			CertFile:           filepath.Join(certPath, "cert.pem"),
			KeyFile:            filepath.Join(certPath, "key.pem"),
			InsecureSkipVerify: env["DOCKER_TLS_VERIFY"] == "",
		})
		if err != nil {
			return nil, err
		}
		opts = append(opts, dockerclient.WithHTTPClient(&http.Client{
			Transport:     &http.Transport{TLSClientConfig: tlsc},
			CheckRedirect: dockerclient.CheckRedirect,
		}))
	}
	if v := env["DOCKER_API_VERSION"]; v != "" {
		opts = append(opts, dockerclient.WithVersion(v))
	}
	return opts, nil
}

// Client returns the underlying Docker SDK client.
// Exported for use by Docker-compatible backends (e.g., Podman integration tests).
func (r *Runtime) Client() *dockerclient.Client {
	return r.client
}

// Setup builds/rebuilds the yoloai-base image as needed.
// sourceDir is unused for the Docker backend (build inputs are embedded);
// it is accepted for interface compatibility with other runtimes.
//
// Holds an advisory base-image-build lock across the existence check
// and the build to serialize concurrent Setup callers. Without this,
// two `yoloai new` invocations can both observe "image missing,"
// both call buildBaseImage, and the second one races to re-tag
// yoloai-base:latest while the first is doing so — producing
// "AlreadyExists: image already exists" from the Docker daemon.
// Mirrors runtime/tart/base_lock.go.
func (r *Runtime) Setup(ctx context.Context, layout config.Layout, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error {
	unlock, err := AcquireBaseLock(layout, "yoloai-base")
	if err != nil {
		return fmt.Errorf("acquire base lock: %w", err)
	}
	defer unlock()

	// Re-check inside the lock — a concurrent process that held the
	// lock before us may have just finished the build, in which case
	// we skip rebuilding.
	exists, err := r.imageExists(ctx, "yoloai-base")
	if err != nil {
		return fmt.Errorf("check base image: %w", err)
	}

	if force || !exists {
		if !exists {
			fmt.Fprintln(output, "Building base image (first run only, this may take a few minutes)...") //nolint:errcheck // best-effort output
		}
		return r.buildBaseImage(ctx, layout, output, logger)
	}

	if NeedsBuild(layout, sourceDir) {
		fmt.Fprintln(output, "Base image resources updated, rebuilding...") //nolint:errcheck // best-effort output
		return r.buildBaseImage(ctx, layout, output, logger)
	}

	return nil
}

// IsReady returns true if the yoloai-base Docker image exists locally.
func (r *Runtime) IsReady(ctx context.Context) (bool, error) {
	return r.imageExists(ctx, "yoloai-base")
}

// imageExists checks if a Docker image with the given tag exists locally.
func (r *Runtime) imageExists(ctx context.Context, imageRef string) (bool, error) {
	_, err := r.client.ImageInspect(ctx, imageRef)
	if err == nil {
		return true, nil
	}
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

// Create creates a new Docker container from the given InstanceConfig.
func (r *Runtime) Create(ctx context.Context, cfg runtime.InstanceConfig) error {
	mounts := ConvertMounts(cfg.Mounts)
	portBindings, exposedPorts := ConvertPorts(cfg.Ports)

	// Docker-in-Docker (privileged): give the nested daemon a real-filesystem
	// /var/lib/docker via a managed named volume so it can use the native overlay
	// driver (see ensureDindVolumeMount).
	if cfg.Privileged {
		m, err := r.ensureDindVolumeMount(ctx, cfg)
		if err != nil {
			return err
		}
		mounts = append(mounts, m)
	}

	containerConfig := &container.Config{
		Image:        cfg.ImageRef,
		WorkingDir:   cfg.WorkingDir,
		ExposedPorts: exposedPorts,
		Labels:       cfg.Labels,
	}

	if len(cfg.ContainerEnv) > 0 {
		containerConfig.Env = cfg.ContainerEnv
	}

	// Translate "isolated" to default bridge network. Network isolation is
	// implemented via iptables inside the container (entrypoint.py), not by
	// Docker's network layer. Docker doesn't have a network named "isolated",
	// so passing it directly causes "network isolated not found" on start.
	dockerNetworkMode := cfg.NetworkMode
	if dockerNetworkMode == "isolated" {
		dockerNetworkMode = "" // default bridge network
	}

	hostConfig := &container.HostConfig{
		Init:         &cfg.UseInit,
		NetworkMode:  container.NetworkMode(dockerNetworkMode),
		PortBindings: portBindings,
		Mounts:       mounts,
		CapAdd:       cfg.CapAdd,
		UsernsMode:   container.UsernsMode(cfg.UsernsMode),
		Runtime:      cfg.ContainerRuntime,
		Privileged:   cfg.Privileged,
		CgroupnsMode: container.CgroupnsMode(cfg.CgroupnsMode),
	}

	// CAP_SYS_ADMIN is required for overlay mounts inside the container.
	// Docker's default AppArmor profile blocks mount(2) even with SYS_ADMIN;
	// disable it so the entrypoint can mount overlayfs.
	// Privileged mode already disables AppArmor so no SecurityOpt is needed.
	if !cfg.Privileged {
		if slices.Contains(cfg.CapAdd, "SYS_ADMIN") {
			hostConfig.SecurityOpt = append(hostConfig.SecurityOpt, "apparmor=unconfined")
		}
	}

	// Apply seccomp profile when explicitly requested.
	// "unconfined" disables the default seccomp filter — required for Docker-in-Docker
	// (rootless: allows unshare(CLONE_NEWUSER); rootful: allows namespace syscalls).
	// Privileged mode already disables seccomp so we skip the opt to avoid redundancy.
	if cfg.Seccomp != "" && !cfg.Privileged {
		hostConfig.SecurityOpt = append(hostConfig.SecurityOpt, "seccomp="+cfg.Seccomp)
	}

	if len(cfg.Devices) > 0 {
		devices := make([]container.DeviceMapping, len(cfg.Devices))
		for i, d := range cfg.Devices {
			devices[i] = container.DeviceMapping{
				PathOnHost:        d,
				PathInContainer:   d,
				CgroupPermissions: "rwm",
			}
		}
		hostConfig.Devices = devices
	}

	if cfg.Resources != nil {
		if cfg.Resources.NanoCPUs > 0 {
			hostConfig.NanoCPUs = cfg.Resources.NanoCPUs
		}
		if cfg.Resources.Memory > 0 {
			hostConfig.Memory = cfg.Resources.Memory
		}
	}

	// Pre-clear any stale container with this name from a previous failed run.
	_ = r.client.ContainerRemove(ctx, cfg.Name, container.RemoveOptions{Force: true})

	_, err := r.client.ContainerCreate(ctx, containerConfig, hostConfig, &network.NetworkingConfig{}, nil, cfg.Name)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}

	return nil
}

// Start starts a stopped Docker container. Returns nil if already running.
func (r *Runtime) Start(ctx context.Context, name string) error {
	if err := r.client.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		if cerrdefs.IsConflict(err) {
			return nil // already running
		}
		if cerrdefs.IsNotFound(err) {
			return runtime.ErrNotFound
		}
		return err
	}
	return nil
}

// Stop stops a running Docker container. Returns nil if already stopped.
func (r *Runtime) Stop(ctx context.Context, name string) error {
	if err := r.client.ContainerStop(ctx, name, container.StopOptions{}); err != nil {
		if cerrdefs.IsNotFound(err) || cerrdefs.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("stop container: %w", err)
	}
	return nil
}

// Remove removes a Docker container. Returns nil if already removed.
func (r *Runtime) Remove(ctx context.Context, name string) error {
	if err := r.client.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil {
		if !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("remove container: %w", err)
		}
	}
	// Best-effort cleanup of the per-sandbox docker-in-docker storage volume
	// (privileged sandboxes only; a no-op otherwise). It carries the
	// com.yoloai.managed label, so `yoloai system prune` reclaims any leak.
	_ = r.client.VolumeRemove(ctx, dockerLibVolumeName(name), false)
	return nil
}

// dockerLibVolumeName derives the managed /var/lib/docker volume name for a
// privileged sandbox from its instance name. Deterministic so Remove can find
// it without extra state.
func dockerLibVolumeName(instanceName string) string {
	return instanceName + "-varlibdocker"
}

// ensureDindVolumeMount creates (idempotently) the managed named volume that
// backs the nested daemon's /var/lib/docker and returns the mount to attach.
// The container rootfs is overlay, and overlay2 can't nest on overlay (and
// fuse-overlayfs can't exec on Docker Desktop's LinuxKit kernel) — a real-fs
// volume sidesteps both, working on Linux + all macOS VMs. The volume carries
// com.yoloai.managed (plus the instance's labels) so Remove/prune can reclaim
// it. See docs/contributors/design/research/dind-storage-drivers.md.
func (r *Runtime) ensureDindVolumeMount(ctx context.Context, cfg runtime.InstanceConfig) (mount.Mount, error) {
	volName := dockerLibVolumeName(cfg.Name)
	labels := map[string]string{managedLabel: "true"}
	for k, v := range cfg.Labels {
		labels[k] = v
	}
	if _, err := r.client.VolumeCreate(ctx, volume.CreateOptions{Name: volName, Labels: labels}); err != nil {
		return mount.Mount{}, fmt.Errorf("create docker-in-docker storage volume: %w", err)
	}
	return mount.Mount{Type: mount.TypeVolume, Source: volName, Target: "/var/lib/docker"}, nil
}

// Inspect returns the state of a Docker container.
func (r *Runtime) Inspect(ctx context.Context, name string) (runtime.InstanceInfo, error) {
	info, err := r.client.ContainerInspect(ctx, name)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return runtime.InstanceInfo{}, runtime.ErrNotFound
		}
		return runtime.InstanceInfo{}, fmt.Errorf("inspect container: %w", err)
	}

	return runtime.InstanceInfo{
		Running: info.State.Running,
	}, nil
}

// Exec runs a command inside a running Docker container and returns the result.
func (r *Runtime) Exec(ctx context.Context, name string, cmd []string, user string) (runtime.ExecResult, error) {
	execResp, err := r.client.ContainerExecCreate(ctx, name, container.ExecOptions{
		Cmd:          cmd,
		User:         user,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return runtime.ExecResult{}, fmt.Errorf("exec create: %w", err)
	}

	resp, err := r.client.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return runtime.ExecResult{}, fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, resp.Reader); err != nil {
		return runtime.ExecResult{}, fmt.Errorf("exec read: %w", err)
	}

	inspectResp, err := r.client.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return runtime.ExecResult{}, fmt.Errorf("exec inspect: %w", err)
	}

	result := runtime.ExecResult{
		Stdout:   strings.TrimSpace(stdout.String()),
		ExitCode: inspectResp.ExitCode,
	}

	if inspectResp.ExitCode != 0 {
		return result, fmt.Errorf("exec exited with code %d: %s", inspectResp.ExitCode, strings.TrimSpace(stderr.String()))
	}

	return result, nil
}

// InteractiveExec runs a command inside a Docker container over the SDK's
// exec-attach socket — the same control plane as Create/Inspect/Exec — rather
// than shelling out to `docker exec`. Routing through the API socket keeps the
// exec's view of the container identical to Inspect's: a name that Inspect
// resolves to a running container can always be exec'd, even under concurrent
// load where a freshly-spawned bare CLI process can race the rootless-Podman
// store and report "no such container" for a container the socket sees running.
//
// streams.In/Out/Err are treated as opaque byte streams — the library never
// inspects In's FD, sets raw mode, or installs signal handlers (§12). The caller
// owns terminal management (raw mode, SIGWINCH → streams.Resize) before handing
// the streams in. Initial PTY geometry comes from streams.Rows/streams.Cols
// (zero → daemon default); live resizes arrive as TermSize on streams.Resize.
func (r *Runtime) InteractiveExec(ctx context.Context, name string, cmd []string, user string, workDir string, streams runtime.IOStreams) error {
	return r.execAttach(ctx, name, cmd, user, workDir, streams)
}

// StdioExec runs cmd inside the container with stdio connected to the
// caller-supplied reader and writers. Implements runtime.StdioExecer; used by
// the MCP proxy to bridge stdio between an outer agent and an inner MCP server
// running in the sandbox. Like InteractiveExec it goes through the SDK socket,
// not a `docker exec` subprocess, so the MCP bridge shares the same container
// view as the rest of the runtime.
func (r *Runtime) StdioExec(ctx context.Context, name string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return r.execAttach(ctx, name, cmd, "", "", runtime.IOStreams{In: stdin, Out: stdout, Err: stderr})
}

// execAttach creates an exec on the container, attaches over the hijacked
// socket, bridges the caller's streams, and reports the inner exit code as a
// *runtime.ExecError (nil on exit 0). It is the shared core of InteractiveExec
// (TTY) and StdioExec (raw stdio).
func (r *Runtime) execAttach(ctx context.Context, name string, cmd []string, user, workDir string, streams runtime.IOStreams) error {
	execID, err := r.createExec(ctx, name, cmd, user, workDir, streams)
	if err != nil {
		return err
	}

	resp, err := r.client.ContainerExecAttach(ctx, execID, container.ExecAttachOptions{Tty: streams.TTY})
	if err != nil {
		return fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	if streams.TTY && streams.Rows > 0 && streams.Cols > 0 {
		_ = r.resizeExec(ctx, execID, streams.Rows, streams.Cols)
	}
	if streams.Resize != nil {
		go r.forwardExecResizes(ctx, execID, streams.Resize)
	}

	bridgeExecStreams(resp, streams)

	inspect, err := r.client.ContainerExecInspect(ctx, execID)
	if err != nil {
		return fmt.Errorf("exec inspect: %w", err)
	}
	if inspect.ExitCode != 0 {
		return &runtime.ExecError{ExitCode: inspect.ExitCode}
	}
	return nil
}

// createExec builds the exec configuration and returns its ID. A TTY exec
// advertises TERM (caller-supplied via streams.Term, defaulting to a safe
// modern terminal — §12: the library never reads the process's own $TERM) and
// seeds the initial console size so ncurses/tmux read the right dimensions
// before the post-attach resize lands.
func (r *Runtime) createExec(ctx context.Context, name string, cmd []string, user, workDir string, streams runtime.IOStreams) (string, error) {
	opts := container.ExecOptions{
		Cmd:          cmd,
		User:         user,
		WorkingDir:   workDir,
		Tty:          streams.TTY,
		AttachStdin:  streams.In != nil,
		AttachStdout: true,
		AttachStderr: true,
	}
	if streams.TTY {
		term := streams.Term
		if term == "" {
			term = "xterm-256color"
		}
		opts.Env = []string{"TERM=" + term}
		if streams.Rows > 0 && streams.Cols > 0 {
			opts.ConsoleSize = &[2]uint{uint(streams.Rows), uint(streams.Cols)} //nolint:gosec // G115: terminal dimensions fit uint
		}
	}
	resp, err := r.client.ContainerExecCreate(ctx, name, opts)
	if err != nil {
		return "", fmt.Errorf("exec create: %w", err)
	}
	return resp.ID, nil
}

// bridgeExecStreams wires the hijacked connection to the caller's streams:
// stdin is copied in a goroutine (closing the write half when it drains), and
// the container's output is copied on the calling goroutine until the daemon
// closes the stream on process exit. TTY output is a single raw stream; non-TTY
// output is demultiplexed into Out/Err. Copy errors are ignored — the
// authoritative exit signal is ContainerExecInspect, matching the docker CLI.
func bridgeExecStreams(resp dockertypes.HijackedResponse, streams runtime.IOStreams) {
	if streams.In != nil {
		go func() {
			_, _ = io.Copy(resp.Conn, streams.In)
			_ = resp.CloseWrite()
		}()
	}
	if streams.TTY {
		_, _ = io.Copy(streams.Out, resp.Reader)
	} else {
		_, _ = stdcopy.StdCopy(streams.Out, streams.Err, resp.Reader)
	}
}

// resizeExec applies a terminal geometry to the running exec's PTY.
func (r *Runtime) resizeExec(ctx context.Context, execID string, rows, cols int) error {
	return r.client.ContainerExecResize(ctx, execID, container.ResizeOptions{
		Height: uint(rows), //nolint:gosec // G115: terminal dimensions fit uint
		Width:  uint(cols), //nolint:gosec // G115: terminal dimensions fit uint
	})
}

// forwardExecResizes applies caller-supplied geometry updates to the exec's PTY
// until the channel closes or ctx is cancelled (the latter fires when execAttach
// returns and its context is torn down).
func (r *Runtime) forwardExecResizes(ctx context.Context, execID string, resize <-chan runtime.TermSize) {
	for {
		select {
		case <-ctx.Done():
			return
		case sz, ok := <-resize:
			if !ok {
				return
			}
			if sz.Rows > 0 && sz.Cols > 0 {
				_ = r.resizeExec(ctx, execID, sz.Rows, sz.Cols)
			}
		}
	}
}

// Close releases the Docker client connection.
func (r *Runtime) Close() error {
	return r.client.Close()
}

// Logs returns the last n lines of a container's combined stdout+stderr output.
// Returns empty string if the container does not exist or logs are unavailable.
func (r *Runtime) Logs(ctx context.Context, name string, tail int) string {
	out, err := r.client.ContainerLogs(ctx, name, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tail),
	})
	if err != nil {
		return ""
	}
	defer out.Close() //nolint:errcheck // best-effort close
	var buf bytes.Buffer
	_, _ = stdcopy.StdCopy(&buf, &buf, out)
	return strings.TrimSpace(buf.String())
}

// DiagHint returns a backend-specific hint for checking logs.
func (r *Runtime) DiagHint(instanceName string) string {
	return fmt.Sprintf("run '%s logs %s' to see what went wrong", r.binaryName, instanceName)
}

// Descriptor returns a BackendDescriptor with the static facts for this backend.
// Always returns the package-level descriptor; podman's Runtime overrides this
// method to return its own descriptor.
func (r *Runtime) Descriptor() runtime.BackendDescriptor {
	return descriptor
}

// dockerInfoOutput fetches the list of registered OCI runtime names from the
// Docker daemon. Variable for testing. env is the explicit subprocess env
// (DEV §12); pass r.execEnv from a Runtime.
var dockerInfoOutput = func(ctx context.Context, env []string, binaryName string) ([]byte, error) {
	return sysexec.CommandContext(ctx, env, binaryName, "info", "--format", "{{range $k, $v := .Runtimes}}{{$k}}\n{{end}}").Output()
}

// RequiredCapabilities returns the host capabilities needed for the given isolation mode.
func (r *Runtime) RequiredCapabilities(isolation runtime.IsolationMode) []caps.HostCapability {
	switch isolation {
	case runtime.IsolationModeContainerEnhanced:
		// runsc must live wherever the daemon runs. On Linux the daemon shares
		// the host filesystem, so verify the binary on PATH (gvisorRunsc first:
		// if it's missing, registration can't work) and that it's registered.
		// On macOS/Windows the daemon runs in a VM (Docker Desktop / OrbStack /
		// Podman Machine); the host PATH says nothing about the daemon's
		// runtimes, so registration with the daemon is the authoritative — and
		// only host-checkable — signal. The daemon verifies the binary itself at
		// container-create time.
		if goruntime.GOOS == "linux" {
			return []caps.HostCapability{r.gvisorRunsc, r.gvisorRegistered}
		}
		return []caps.HostCapability{r.gvisorRegistered}
	default:
		return nil
	}
}

// TmuxSocket returns the fixed tmux socket path for Docker/Podman containers.
// A fixed path ensures exec'd processes find the same server as the container
// init process (the uid-based default may differ under gVisor). sandboxDir is
// ignored — Docker containers always use the same socket path.
func (r *Runtime) TmuxSocket(_ string) string { return "/tmp/yoloai-tmux.sock" }

// AttachCommand returns the command to attach to the tmux session.
// For gVisor on ARM64, setsid is used to work around missing TIOCSCTTY in
// gVisor's exec path. For all other cases, script creates a fresh PTY and
// controlling terminal that tmux can use cleanly.
func (r *Runtime) AttachCommand(tmuxSocket string, _ int, _ int, isolation runtime.IsolationMode) []string {
	// gVisor on ARM64: docker exec -it does NOT call TIOCSCTTY, so the exec'd
	// process has no controlling terminal and tmux exits with EACCES on /dev/tty.
	// setsid creates a new session with no CTY; /dev/tty returns ENXIO, which
	// tmux handles by falling back to stdin (the PTY).
	if isolation == runtime.IsolationModeContainerEnhanced && goruntime.GOARCH == "arm64" {
		cmd := []string{"setsid", "tmux"}
		if tmuxSocket != "" {
			cmd = append(cmd, "-S", tmuxSocket)
		}
		return append(cmd, "attach", "-t", "main")
	}
	// Standard: script -q -e -c <cmd> /dev/null — quiet, propagate exit status,
	// run cmd, discard transcript. Creates a fresh PTY + controlling terminal.
	var tmuxArgs string
	if tmuxSocket != "" {
		tmuxArgs = fmt.Sprintf("exec tmux -S %s attach -t main", tmuxSocket)
	} else {
		tmuxArgs = "exec tmux attach -t main"
	}
	return []string{"/usr/bin/script", "-q", "-e", "-c", tmuxArgs, "/dev/null"}
}

// convertMounts converts runtime.MountSpec to Docker mount.Mount.
// ConvertMounts converts runtime.MountSpec to Docker SDK mount types.
// Exported for use by Docker-compatible backends (e.g., Podman).
func ConvertMounts(specs []runtime.MountSpec) []mount.Mount {
	if len(specs) == 0 {
		return nil
	}
	mounts := make([]mount.Mount, len(specs))
	for i, s := range specs {
		mounts[i] = mount.Mount{
			Type:     mount.TypeBind,
			Source:   s.HostPath,
			Target:   s.ContainerPath,
			ReadOnly: s.ReadOnly,
		}
	}
	return mounts
}

// ConvertPorts converts runtime.PortMapping to Docker SDK port types.
// Exported for use by Docker-compatible backends (e.g., Podman).
func ConvertPorts(ports []runtime.PortMapping) (nat.PortMap, nat.PortSet) {
	if len(ports) == 0 {
		return nil, nil
	}

	portMap := nat.PortMap{}
	portSet := nat.PortSet{}

	for _, p := range ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		port, err := nat.NewPort(proto, strconv.Itoa(p.ContainerPort))
		if err != nil {
			continue // skip invalid (already validated upstream)
		}
		// nat.PortBinding's HostPort field is a string (Docker SDK shape);
		// our PortMapping.HostPort is the typed int — convert at the boundary.
		portMap[port] = append(portMap[port], nat.PortBinding{
			HostPort: strconv.Itoa(p.HostPort),
		})
		portSet[port] = struct{}{}
	}

	return portMap, portSet
}
