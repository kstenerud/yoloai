// ABOUTME: RunNetnsSidecar — run an ephemeral privileged helper container that
// ABOUTME: shares a target container's network namespace. The primitive behind
// ABOUTME: tamper-resistant network isolation: the firewall is installed from
// ABOUTME: here (CAP_NET_ADMIN) while the agent container is denied that cap.
package docker

import (
	"bytes"
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/kstenerud/yoloai/runtime"
)

// compile-time assertion: *Runtime must satisfy NetnsSidecarRunner.
var _ runtime.NetnsSidecarRunner = (*Runtime)(nil)

// RunNetnsSidecar runs spec.Argv in a throwaway container joined to the target's
// network namespace (--network container:<target>) with the requested
// capabilities, blocks until it exits, and removes it. A non-zero exit is an
// error carrying the captured logs, so a failed firewall install fails the
// caller's operation rather than leaving the agent unguarded.
func (r *Runtime) RunNetnsSidecar(ctx context.Context, spec runtime.NetnsSidecarSpec) error {
	image := spec.Image
	if image == "" {
		info, err := r.client.ContainerInspect(ctx, spec.Target)
		if err != nil {
			return fmt.Errorf("netns sidecar: inspect target %s: %w", spec.Target, err)
		}
		image = info.Config.Image
	}

	name := spec.Target + "-netns-sidecar"
	// Clear any stale sidecar left by a crashed prior run before reusing the name.
	_ = r.client.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}) //nolint:errcheck // best-effort

	containerConfig := &container.Config{
		Image:      image,
		Entrypoint: spec.Argv, // override the image ENTRYPOINT (entrypoint.sh)
		Env:        spec.Env,
	}
	hostConfig := &container.HostConfig{
		NetworkMode: container.NetworkMode("container:" + spec.Target),
		CapAdd:      spec.CapAdd,
	}

	if _, err := r.client.ContainerCreate(ctx, containerConfig, hostConfig, &network.NetworkingConfig{}, nil, name); err != nil {
		return fmt.Errorf("netns sidecar: create: %w", err)
	}
	defer func() {
		_ = r.client.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}) //nolint:errcheck // best-effort cleanup
	}()

	if err := r.client.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		return fmt.Errorf("netns sidecar: start: %w", err)
	}

	statusCh, errCh := r.client.ContainerWait(ctx, name, container.WaitConditionNotRunning)
	var exitCode int64
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("netns sidecar: wait: %w", err)
		}
	case status := <-statusCh:
		exitCode = status.StatusCode
	case <-ctx.Done():
		return ctx.Err()
	}

	if exitCode != 0 {
		return fmt.Errorf("netns sidecar exited %d: %s", exitCode, r.sidecarLogs(ctx, name))
	}
	return nil
}

// sidecarLogs returns the demultiplexed stdout+stderr of the named container for
// diagnostics. Best-effort: returns a short marker string on any failure.
func (r *Runtime) sidecarLogs(ctx context.Context, name string) string {
	rc, err := r.client.ContainerLogs(ctx, name, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return "(logs unavailable: " + err.Error() + ")"
	}
	defer rc.Close() //nolint:errcheck // best-effort
	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, rc); err != nil {
		return "(log read failed: " + err.Error() + ")"
	}
	return out.String()
}
