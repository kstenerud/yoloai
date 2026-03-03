//go:build integration

package docker

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/require"
)

// dockerSetup connects to Docker, ensures the base image exists,
// and returns a *Runtime. Uses t.Cleanup for Close().
func dockerSetup(t *testing.T) (*Runtime, context.Context) {
	t.Helper()
	ctx := context.Background()

	rt, err := New(ctx)
	require.NoError(t, err, "Docker must be running for integration tests")
	t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup

	exists, err := rt.ImageExists(ctx, "yoloai-base")
	require.NoError(t, err)
	require.True(t, exists, "yoloai-base image must exist — run 'make build && ./yoloai setup' first")

	return rt, ctx
}

// createTestContainer creates a container using the yoloai-base image with
// entrypoint overridden to "sleep infinity" so it stays alive for exec tests.
// Uses the Docker SDK directly to set Entrypoint/Cmd since InstanceConfig
// doesn't expose those fields (they're normally baked into the image).
// Returns the container name. Uses t.Cleanup for removal.
func createTestContainer(t *testing.T, rt *Runtime, ctx context.Context, cfg runtime.InstanceConfig) string {
	t.Helper()

	if cfg.Name == "" {
		cfg.Name = "yoloai-test-" + t.Name()
	}
	if cfg.ImageRef == "" {
		cfg.ImageRef = "yoloai-base"
	}

	mounts := convertMounts(cfg.Mounts)
	portBindings, exposedPorts := convertPorts(cfg.Ports)

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

	_, err := rt.client.ContainerCreate(ctx, containerConfig, hostConfig, &network.NetworkingConfig{}, nil, cfg.Name)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = rt.Stop(ctx, cfg.Name)
		_ = rt.Remove(ctx, cfg.Name)
	})

	return cfg.Name
}
