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

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// Runtime implements runtime.Runtime using the Docker SDK.
type Runtime struct {
	client *dockerclient.Client
}

// Compile-time check.
var _ runtime.Runtime = (*Runtime)(nil)

// New creates a Runtime and verifies the Docker daemon is reachable.
func New(ctx context.Context) (*Runtime, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker is not installed, install it from https://docs.docker.com/get-docker/")
	}

	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("create Docker client: %w", err)
	}

	_, err = cli.Ping(ctx)
	if err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker daemon is not responding, start Docker Desktop or run 'sudo systemctl start docker'")
	}

	return &Runtime{client: cli}, nil
}

// EnsureImage seeds Docker build resources and builds/rebuilds the
// yoloai-base image as needed.
func (r *Runtime) EnsureImage(ctx context.Context, sourceDir string, output io.Writer, logger *slog.Logger, force bool) error {
	// Seed Dockerfile.base, entrypoint.sh, tmux.conf
	seedResult, err := SeedResources(sourceDir)
	if err != nil {
		return fmt.Errorf("seed resources: %w", err)
	}

	if len(seedResult.Conflicts) > 0 {
		if seedResult.ManifestMissing {
			fmt.Fprintln(output, "NOTE: yoloAI has updated resource files, but some differ from the new version.") //nolint:errcheck // best-effort output
			fmt.Fprintln(output, "  If you have not customized these files, accept the new versions below.")       //nolint:errcheck // best-effort output
		} else {
			fmt.Fprintln(output, "NOTE: some resource files have local changes and were not overwritten.") //nolint:errcheck // best-effort output
		}
		for _, name := range seedResult.Conflicts {
			fmt.Fprintf(output, "  %s: new version written to ~/.yoloai/%s.new\n", name, name) //nolint:errcheck // best-effort output
			fmt.Fprintf(output, "    accept: mv ~/.yoloai/%s.new ~/.yoloai/%s\n", name, name)  //nolint:errcheck // best-effort output
			fmt.Fprintf(output, "    keep:   rm ~/.yoloai/%s.new\n", name)                     //nolint:errcheck // best-effort output
		}
		fmt.Fprintln(output, "  Then run 'yoloai build' to rebuild the base image.") //nolint:errcheck // best-effort output
	}

	// Check if image exists
	exists, err := r.ImageExists(ctx, "yoloai-base")
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

// ImageExists checks if a Docker image with the given tag exists locally.
func (r *Runtime) ImageExists(ctx context.Context, imageRef string) (bool, error) {
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
	mounts := convertMounts(cfg.Mounts)
	portBindings, exposedPorts := convertPorts(cfg.Ports)

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
	}

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

	c := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // G204: name and cmd are from validated sandbox state
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// Close releases the Docker client connection.
func (r *Runtime) Close() error {
	return r.client.Close()
}

// DiagHint returns a Docker-specific hint for checking logs.
func (r *Runtime) DiagHint(instanceName string) string {
	return fmt.Sprintf("run 'docker logs %s' to see what went wrong", instanceName)
}

// convertMounts converts runtime.MountSpec to Docker mount.Mount.
func convertMounts(specs []runtime.MountSpec) []mount.Mount {
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

// convertPorts converts runtime.PortMapping to Docker port types.
func convertPorts(ports []runtime.PortMapping) (nat.PortMap, nat.PortSet) {
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
