// Package docker implements the runtime.Runtime interface using Docker SDK.
// ABOUTME: Wraps Docker SDK client for container lifecycle, exec, and image ops.
package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	goruntime "runtime"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
)

// Runtime implements runtime.Runtime using the Docker SDK.
type Runtime struct {
	client     *dockerclient.Client
	binaryName string // CLI binary name ("docker" or "podman")
}

// Compile-time checks.
var _ runtime.Runtime = (*Runtime)(nil)
var _ runtime.IsolationValidator = (*Runtime)(nil)

// New creates a Runtime and verifies the Docker daemon is reachable.
func New(ctx context.Context) (*Runtime, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, config.NewDependencyError("docker is not installed, install it from https://docs.docker.com/get-docker/")
	}
	return NewWithSocket(ctx, "", "docker")
}

// NewWithSocket creates a Runtime connected to a specific Docker-compatible socket.
// If host is empty, the client uses the default Docker environment variables.
// binaryName is the CLI binary to use for interactive exec and image builds
// (e.g., "docker" or "podman").
func NewWithSocket(ctx context.Context, host string, binaryName string) (*Runtime, error) {
	opts := []dockerclient.Opt{
		dockerclient.WithAPIVersionNegotiation(),
	}
	if host != "" {
		opts = append(opts, dockerclient.WithHost(host))
	} else {
		opts = append(opts, dockerclient.FromEnv)
	}

	cli, err := dockerclient.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("create Docker client: %w", err)
	}

	_, err = cli.Ping(ctx)
	if err != nil {
		_ = cli.Close()
		if os.IsPermission(err) || strings.Contains(err.Error(), "permission denied") {
			return nil, config.NewPermissionError("%s socket permission denied: add your user to the %s group or run with sudo", binaryName, binaryName)
		}
		var hint string
		switch binaryName {
		case "podman":
			hint = "start Podman Desktop or run 'systemctl --user start podman.socket'"
		default:
			hint = "start Docker Desktop or run 'sudo systemctl start docker'"
		}
		return nil, config.NewDependencyError("%s daemon is not responding, %s", binaryName, hint)
	}

	return &Runtime{client: cli, binaryName: binaryName}, nil
}

// Client returns the underlying Docker SDK client.
// Exported for use by Docker-compatible backends (e.g., Podman integration tests).
func (r *Runtime) Client() *dockerclient.Client {
	return r.client
}

// Setup builds/rebuilds the yoloai-base image as needed.
// sourceDir is unused for the Docker backend (build inputs are embedded);
// it is accepted for interface compatibility with other runtimes.
func (r *Runtime) Setup(ctx context.Context, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error {
	// Check if image exists
	exists, err := r.imageExists(ctx, "yoloai-base")
	if err != nil {
		return fmt.Errorf("check base image: %w", err)
	}

	if force || !exists {
		if !exists {
			fmt.Fprintln(output, "Building base image (first run only, this may take a few minutes)...") //nolint:errcheck // best-effort output
		}
		return buildBaseImage(ctx, r.client, sourceDir, output, logger)
	}

	if NeedsBuild(sourceDir) {
		fmt.Fprintln(output, "Base image resources updated, rebuilding...") //nolint:errcheck // best-effort output
		return buildBaseImage(ctx, r.client, sourceDir, output, logger)
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
	}

	hostConfig := &container.HostConfig{
		Init:         &cfg.UseInit,
		NetworkMode:  container.NetworkMode(cfg.NetworkMode),
		PortBindings: portBindings,
		Mounts:       mounts,
		CapAdd:       cfg.CapAdd,
		UsernsMode:   container.UsernsMode(cfg.UsernsMode),
		Runtime:      cfg.ContainerRuntime,
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
// by shelling out to `docker exec -it`.
func (r *Runtime) InteractiveExec(ctx context.Context, name string, cmd []string, user string, workDir string) error {
	args := []string{"exec", "-it"}
	if user != "" {
		args = append(args, "-u", user)
	}
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, name)
	args = append(args, cmd...)

	c := exec.CommandContext(ctx, r.binaryName, args...) //nolint:gosec // G204: name and cmd are from validated sandbox state
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
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

// Name returns the backend name.
func (r *Runtime) Name() string { return r.binaryName }

// Capabilities returns the Docker backend's feature set.
func (r *Runtime) Capabilities() runtime.BackendCaps {
	return runtime.BackendCaps{
		NetworkIsolation: true,
		OverlayDirs:      true,
		CapAdd:           true,
		HostFilesystem:   false,
	}
}

// AgentProvisionedByBackend returns true — Docker containers use an npm-installed
// agent, so the home-seed .claude.json must be patched from "native" to "npm-global".
func (r *Runtime) AgentProvisionedByBackend() bool { return true }

// ResolveCopyMount returns hostPath unchanged — Docker bind-mounts the copy at
// the original host path inside the container.
func (r *Runtime) ResolveCopyMount(_, hostPath string) string { return hostPath }

// dockerInfoOutput fetches the list of registered OCI runtime names from the
// Docker daemon. Variable for testing.
var dockerInfoOutput = func(ctx context.Context, binaryName string) ([]byte, error) {
	return exec.CommandContext(ctx, binaryName, "info", "--format", "{{range $k, $v := .Runtimes}}{{$k}}\n{{end}}").Output() //nolint:gosec // G204: binaryName is "docker" or "podman"
}

// ValidateIsolation checks that the host has the required runtime for the
// given isolation mode. For container-enhanced (gVisor), verifies that the
// "runsc" OCI runtime is registered with the Docker daemon.
func (r *Runtime) ValidateIsolation(ctx context.Context, isolation string) error {
	if isolation != "container-enhanced" {
		return nil
	}
	out, err := dockerInfoOutput(ctx, r.binaryName)
	if err != nil {
		return fmt.Errorf("check runtimes: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "runsc" {
			return nil
		}
	}
	return fmt.Errorf("--isolation container-enhanced requires gVisor (runsc) registered as a Docker runtime\n" +
		"  Install: https://gvisor.dev/docs/user_guide/install/\n" +
		"  Then add to /etc/docker/daemon.json: {\"runtimes\": {\"runsc\": {\"path\": \"/usr/local/sbin/runsc\"}}}")
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
func (r *Runtime) AttachCommand(tmuxSocket string, _ int, _ int, isolation string) []string {
	// gVisor on ARM64: docker exec -it does NOT call TIOCSCTTY, so the exec'd
	// process has no controlling terminal and tmux exits with EACCES on /dev/tty.
	// setsid creates a new session with no CTY; /dev/tty returns ENXIO, which
	// tmux handles by falling back to stdin (the PTY).
	if isolation == "container-enhanced" && goruntime.GOARCH == "arm64" {
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
			Source:   s.Source,
			Target:   s.Target,
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
		port, err := nat.NewPort(proto, p.InstancePort)
		if err != nil {
			continue // skip invalid (already validated upstream)
		}
		portMap[port] = append(portMap[port], nat.PortBinding{
			HostPort: p.HostPort,
		})
		portSet[port] = struct{}{}
	}

	return portMap, portSet
}
