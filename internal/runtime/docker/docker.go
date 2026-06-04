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
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	goruntime "runtime"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-connections/tlsconfig"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/caps"
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
// An explicit DOCKER_HOST is treated as a positive signal (caller knows where
// the daemon is); otherwise the default /var/run/docker.sock must exist.
func probe(_ context.Context, env map[string]string) (bool, string) {
	if env["DOCKER_HOST"] != "" {
		return true, ""
	}
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		return true, ""
	}
	return false, "docker socket not found (set DOCKER_HOST or start the docker daemon)"
}

// versionString runs `docker version` and returns a "Client: X / Server: Y"
// summary. Returns "" if the docker binary is missing or the daemon is
// unreachable — callers (bug reports, yoloai info) treat empty as "no
// version known" and fall back to the probe's availability verdict.
func versionString(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "docker", "version", "--format",
		"Client: {{.Client.Version}} / Server: {{.Server.Version}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func init() {
	runtime.Register(func(ctx context.Context, layout config.Layout) (runtime.Runtime, error) {
		return New(ctx, layout.Env)
	}, descriptor)
}

// Runtime implements runtime.Runtime using the Docker SDK.
type Runtime struct {
	client     *dockerclient.Client
	binaryName string // CLI binary name ("docker" or "podman")

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

// New creates a Runtime and verifies the Docker daemon is reachable. env is
// the caller's threaded environment snapshot (layout.Env); the daemon socket
// and TLS settings are read from it rather than os.Environ (§12). A nil/empty
// env means "default socket, no TLS" — exactly the SDK's behavior when the
// DOCKER_* vars are unset.
func New(ctx context.Context, env map[string]string) (*Runtime, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, yoerrors.NewDependencyError("docker is not installed, install it from https://docs.docker.com/get-docker/")
	}
	return NewWithSocket(ctx, "", "docker", env)
}

// NewWithSocket creates a Runtime connected to a specific Docker-compatible socket.
// If host is non-empty it pins the connection to that socket. If host is empty,
// the client is configured from env (DOCKER_HOST / DOCKER_CERT_PATH /
// DOCKER_TLS_VERIFY / DOCKER_API_VERSION) — the threaded snapshot, not
// os.Environ (§12). binaryName is the CLI binary to use for interactive exec
// and image builds (e.g., "docker" or "podman").
func NewWithSocket(ctx context.Context, host string, binaryName string, env map[string]string) (*Runtime, error) {
	opts := []dockerclient.Opt{
		dockerclient.WithAPIVersionNegotiation(),
	}
	if host != "" {
		opts = append(opts, dockerclient.WithHost(host))
	} else {
		envOpts, err := optsFromEnv(env)
		if err != nil {
			return nil, fmt.Errorf("configure docker client from env: %w", err)
		}
		opts = append(opts, envOpts...)
	}

	cli, err := dockerclient.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("create Docker client: %w", err)
	}

	_, err = cli.Ping(ctx)
	if err != nil {
		_ = cli.Close()
		if runtime.IsPermissionDenied(err) {
			return nil, yoerrors.NewPermissionError("%s socket permission denied: add your user to the %s group or run with sudo", binaryName, binaryName)
		}
		var hint string
		switch binaryName {
		case "podman":
			hint = "start Podman Desktop or run 'systemctl --user start podman.socket'"
		default:
			hint = "start Docker Desktop or run 'sudo systemctl start docker'"
		}
		return nil, yoerrors.NewDependencyError("%s daemon is not responding, %s", binaryName, hint)
	}

	r := &Runtime{client: cli, binaryName: binaryName}
	r.gvisorRunsc = caps.NewGVisorRunsc(exec.LookPath)
	r.gvisorRegistered = buildGVisorRegisteredCap(binaryName)
	return r, nil
}

// optsFromEnv reproduces dockerclient.FromEnv, but sources the DOCKER_*
// settings from the caller's threaded env snapshot rather than os.Environ
// (§12). Behavior matches the SDK exactly: an empty DOCKER_CERT_PATH means no
// TLS, an empty DOCKER_HOST means the default socket, an empty
// DOCKER_API_VERSION means version negotiation. So a nil/blank env degrades to
// a plain local connection — present-but-blank is the same code path as absent.
func optsFromEnv(env map[string]string) ([]dockerclient.Opt, error) {
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
	if host := env["DOCKER_HOST"]; host != "" {
		opts = append(opts, dockerclient.WithHost(host))
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
		return buildBaseImage(ctx, layout, r.client, sourceDir, output, logger)
	}

	if NeedsBuild(layout, sourceDir) {
		fmt.Fprintln(output, "Base image resources updated, rebuilding...") //nolint:errcheck // best-effort output
		return buildBaseImage(ctx, layout, r.client, sourceDir, output, logger)
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
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("remove container: %w", err)
	}
	return nil
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

// InteractiveExec runs an interactive command inside a Docker container
// by shelling out to `docker exec`. The IOStreams determine PTY allocation
// (-it vs -i) and where stdio is wired. Caller-supplied non-PTY streams
// (TTY=false) get plain pipes; TTY=true requires the streams to BE
// terminals on the host side for the docker -t flag to work end-to-end.
func (r *Runtime) InteractiveExec(ctx context.Context, name string, cmd []string, user string, workDir string, io runtime.IOStreams) error {
	args := []string{"exec"}
	if io.TTY {
		args = append(args, "-it")
	} else {
		args = append(args, "-i")
	}
	if user != "" {
		args = append(args, "-u", user)
	}
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, name)
	args = append(args, cmd...)

	c := exec.CommandContext(ctx, r.binaryName, args...) //nolint:gosec // G204: name and cmd are from validated sandbox state
	c.Stdin = io.In
	c.Stdout = io.Out
	c.Stderr = io.Err
	return c.Run()
}

// StdioExec runs cmd inside the container with stdio connected to the
// caller-supplied reader and writers. Implements runtime.StdioExecer; used by
// the MCP proxy to bridge stdio between an outer agent and an inner MCP server
// running in the sandbox.
func (r *Runtime) StdioExec(ctx context.Context, name string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	args := []string{"exec", "-i", name}
	args = append(args, cmd...)
	c := exec.CommandContext(ctx, r.binaryName, args...) //nolint:gosec // G204: name and cmd are from validated sandbox state
	c.Stdin = stdin
	c.Stdout = stdout
	c.Stderr = stderr
	return c.Run()
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
// Docker daemon. Variable for testing.
var dockerInfoOutput = func(ctx context.Context, binaryName string) ([]byte, error) {
	return exec.CommandContext(ctx, binaryName, "info", "--format", "{{range $k, $v := .Runtimes}}{{$k}}\n{{end}}").Output() //nolint:gosec // G204: binaryName is "docker" or "podman"
}

// RequiredCapabilities returns the host capabilities needed for the given isolation mode.
func (r *Runtime) RequiredCapabilities(isolation runtime.IsolationMode) []caps.HostCapability {
	switch isolation {
	case runtime.IsolationModeContainerEnhanced:
		// gvisorRunsc first: if the binary isn't present, registration can't work.
		return []caps.HostCapability{r.gvisorRunsc, r.gvisorRegistered}
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
