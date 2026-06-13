//go:build integration

// ABOUTME: Shared integration conformance suite for docker-API-compatible
// ABOUTME: backends (docker, podman). One table of behavioral assertions runs
// ABOUTME: against any backend that exposes the docker SDK client (W5).
package runtimetest

import (
	"context"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/docker"
)

// DockerCompatRuntime is the surface shared by every docker-API-compatible
// backend: the runtime.Backend contract plus the exported docker SDK client the
// conformance suite uses to create long-lived test containers and to verify
// host-config facts (resource limits, port bindings) the runtime.Backend
// interface does not expose.
type DockerCompatRuntime interface {
	runtime.Backend
	Client() *dockerclient.Client
}

// SetupFunc connects to a backend and returns a per-test runtime with cleanup
// (e.g. rt.Close via t.Cleanup) already registered.
type SetupFunc func(t *testing.T) (DockerCompatRuntime, context.Context)

// createContainer creates a container on the yoloai-base image with its
// entrypoint overridden to "sleep infinity" so it stays alive for exec tests.
// Goes through the docker SDK directly because InstanceConfig does not expose
// Entrypoint/Cmd (they are normally baked into the image). Returns the name and
// registers removal via t.Cleanup.
func createContainer(t *testing.T, rt DockerCompatRuntime, ctx context.Context, cfg runtime.InstanceConfig) string {
	t.Helper()

	if cfg.Name == "" {
		// Subtest names carry a "/" (e.g. "TestDockerConformance/ExecSimple"),
		// which is illegal in a container name — flatten it.
		cfg.Name = "yoloai-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	}
	if cfg.ImageRef == "" {
		cfg.ImageRef = "yoloai-base"
	}

	mounts := docker.ConvertMounts(cfg.Mounts)
	portBindings, exposedPorts := docker.ConvertPorts(cfg.Ports)

	containerConfig := &container.Config{
		Image:        cfg.ImageRef,
		Entrypoint:   []string{"sleep"},
		Cmd:          []string{"infinity"},
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
	if cfg.Resources != nil {
		if cfg.Resources.NanoCPUs > 0 {
			hostConfig.NanoCPUs = cfg.Resources.NanoCPUs
		}
		if cfg.Resources.Memory > 0 {
			hostConfig.Memory = cfg.Resources.Memory
		}
	}
	if cfg.UsernsMode != "" {
		hostConfig.UsernsMode = container.UsernsMode(cfg.UsernsMode)
	}

	// Pre-cleanup: evict any stale container left by a previous failed run.
	_ = rt.Stop(ctx, cfg.Name)
	_ = rt.Remove(ctx, cfg.Name)

	_, err := rt.Client().ContainerCreate(ctx, containerConfig, hostConfig, &network.NetworkingConfig{}, nil, cfg.Name)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = rt.Stop(ctx, cfg.Name)
		_ = rt.Remove(ctx, cfg.Name)
	})

	return cfg.Name
}

// RunConformance exercises the behavioral contract for docker-API backends. The
// universal runtime.Backend contract (lifecycle, exec, mounts, idempotency) is
// delegated to the shared, backend-agnostic RunInterfaceConformance suite via an
// SDK-backed Sleeper, so docker/podman verify the exact same table as the VM and
// host backends. This function adds only the assertions that require the docker
// SDK Client() to read host-config facts (resource limits, port bindings) the
// runtime.Backend interface does not expose.
//
// InteractiveExec and StdioExec drive the container over the SDK socket (the same
// control plane as Inspect/Exec), not a `docker exec` subprocess — otherwise a
// bare-CLI invocation can race the rootless-Podman store under load and report
// "no such container" for a container Inspect sees running. The shared suite
// exercises both.
func RunConformance(t *testing.T, setup SetupFunc) {
	// Universal contract: adapt the docker-compat setup into the interface
	// fixture, supplying an SDK-backed sleeper (entrypoint override → sleep).
	RunInterfaceConformance(t, func(t *testing.T) InterfaceBackend {
		rt, ctx := setup(t)
		return InterfaceBackend{
			Runtime: rt,
			Ctx:     ctx,
			NewSleeper: func(t *testing.T, cfg runtime.InstanceConfig) string {
				// docker/podman reap the `sleep infinity` PID 1 via tini.
				cfg.UseInit = true
				return createContainer(t, rt, ctx, cfg)
			},
		}
	})

	// --- Docker-SDK-only assertions (need Client() for host-config facts) ---

	t.Run("ResourceLimits", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{
			UseInit: true,
			Resources: &runtime.ResourceLimits{
				NanoCPUs: 1_000_000_000, // 1 CPU
				Memory:   256 * 1024 * 1024,
			},
		})

		info, err := rt.Client().ContainerInspect(ctx, name)
		require.NoError(t, err)
		assert.Equal(t, int64(1_000_000_000), info.HostConfig.NanoCPUs)
		assert.Equal(t, int64(256*1024*1024), info.HostConfig.Memory)
	})

	t.Run("PortBinding", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{
			UseInit: true,
			Ports:   []runtime.PortMapping{{HostPort: 0, ContainerPort: 8080, Protocol: "tcp"}},
		})

		info, err := rt.Client().ContainerInspect(ctx, name)
		require.NoError(t, err)
		assert.NotEmpty(t, info.HostConfig.PortBindings, "port bindings should be set")
	})

	t.Run("NetworkNone", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{
			UseInit:     true,
			NetworkMode: "none",
		})
		require.NoError(t, rt.Start(ctx, name))

		result, err := rt.Exec(ctx, name, []string{"sh", "-c", "cat /sys/class/net/eth0/operstate 2>/dev/null || echo nodev"}, "")
		// With network=none there is no eth0 device; either no device or an
		// error proves the network is isolated.
		if err == nil {
			assert.Contains(t, result.Stdout, "nodev")
		}
	})
}
