//go:build integration

// ABOUTME: Shared integration conformance suite for docker-API-compatible
// ABOUTME: backends (docker, podman). One table of behavioral assertions runs
// ABOUTME: against any backend that exposes the docker SDK client (W5).
package runtimetest

import (
	"context"
	"os"
	"path/filepath"
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
// backend: the runtime.Runtime contract plus the exported docker SDK client the
// conformance suite uses to create long-lived test containers and to verify
// host-config facts (resource limits, port bindings) the runtime.Runtime
// interface does not expose.
type DockerCompatRuntime interface {
	runtime.Runtime
	Client() *dockerclient.Client
}

// SetupFunc connects to a backend and returns a per-test runtime with cleanup
// (e.g. rt.Close via t.Cleanup) already registered.
type SetupFunc func(t *testing.T) (DockerCompatRuntime, context.Context)

// EnvFromOS snapshots the process environment as a map. Integration tests are
// the test-side boundary (equivalent to the CLI's licensed os.Environ read), so
// they thread the real host env into New just as the CLI would (§12).
func EnvFromOS() map[string]string {
	m := make(map[string]string)
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}
	return m
}

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

// RunConformance exercises the behavioral contract shared by docker-API
// backends. Each subtest connects through setup so every case gets a fresh
// runtime with its own cleanup, matching the per-test isolation the
// backend-specific suites had before they were unified.
func RunConformance(t *testing.T, setup SetupFunc) {
	t.Run("CreateStartStopRemove", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{UseInit: true})

		require.NoError(t, rt.Start(ctx, name))
		info, err := rt.Inspect(ctx, name)
		require.NoError(t, err)
		assert.True(t, info.Running)

		require.NoError(t, rt.Stop(ctx, name))
		info, err = rt.Inspect(ctx, name)
		require.NoError(t, err)
		assert.False(t, info.Running)

		require.NoError(t, rt.Remove(ctx, name))
		_, err = rt.Inspect(ctx, name)
		assert.ErrorIs(t, err, runtime.ErrNotFound)
	})

	t.Run("InspectRunning", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{UseInit: true})
		require.NoError(t, rt.Start(ctx, name))

		info, err := rt.Inspect(ctx, name)
		require.NoError(t, err)
		assert.True(t, info.Running)
	})

	t.Run("InspectStopped", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{UseInit: true})
		require.NoError(t, rt.Start(ctx, name))
		require.NoError(t, rt.Stop(ctx, name))

		info, err := rt.Inspect(ctx, name)
		require.NoError(t, err)
		assert.False(t, info.Running)
	})

	t.Run("InspectNotFound", func(t *testing.T) {
		rt, ctx := setup(t)
		_, err := rt.Inspect(ctx, "yoloai-nonexistent-container-xyz")
		assert.ErrorIs(t, err, runtime.ErrNotFound)
	})

	t.Run("ExecSimple", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{UseInit: true})
		require.NoError(t, rt.Start(ctx, name))

		result, err := rt.Exec(ctx, name, []string{"echo", "hello"}, "")
		require.NoError(t, err)
		assert.Equal(t, "hello", result.Stdout)
		assert.Equal(t, 0, result.ExitCode)
	})

	t.Run("ExecNonZeroExit", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{UseInit: true})
		require.NoError(t, rt.Start(ctx, name))

		result, err := rt.Exec(ctx, name, []string{"false"}, "")
		assert.Error(t, err)
		assert.Equal(t, 1, result.ExitCode)
	})

	t.Run("ExecNotRunning", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{UseInit: true})

		_, err := rt.Exec(ctx, name, []string{"echo", "hello"}, "")
		assert.Error(t, err)
	})

	// InteractiveExec and StdioExec must drive the container over the SDK socket
	// (the same control plane as Inspect/Exec), not a `docker exec` subprocess —
	// otherwise a bare-CLI invocation can race the rootless-Podman store under
	// load and report "no such container" for a container Inspect sees running.
	t.Run("StdioExecPipesOutput", func(t *testing.T) {
		rt, ctx := setup(t)
		execer, ok := rt.(runtime.StdioExecer)
		require.True(t, ok, "docker-compat backend must implement StdioExecer")
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{UseInit: true})
		require.NoError(t, rt.Start(ctx, name))

		var stdout, stderr strings.Builder
		err := execer.StdioExec(ctx, name, []string{"echo", "hello"}, nil, &stdout, &stderr)
		require.NoError(t, err)
		assert.Equal(t, "hello", strings.TrimSpace(stdout.String()))
	})

	t.Run("StdioExecNonZeroExit", func(t *testing.T) {
		rt, ctx := setup(t)
		execer, ok := rt.(runtime.StdioExecer)
		require.True(t, ok, "docker-compat backend must implement StdioExecer")
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{UseInit: true})
		require.NoError(t, rt.Start(ctx, name))

		err := execer.StdioExec(ctx, name, []string{"sh", "-c", "exit 7"}, nil, nil, nil)
		var execErr *runtime.ExecError
		require.ErrorAs(t, err, &execErr, "non-zero exit must surface as *runtime.ExecError")
		assert.Equal(t, 7, execErr.ExitCode)
	})

	t.Run("InteractiveExecNonZeroExit", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{UseInit: true})
		require.NoError(t, rt.Start(ctx, name))

		err := rt.InteractiveExec(ctx, name, []string{"sh", "-c", "exit 9"}, "", "", runtime.IOStreams{TTY: true})
		var execErr *runtime.ExecError
		require.ErrorAs(t, err, &execErr, "TTY exec non-zero exit must surface as *runtime.ExecError")
		assert.Equal(t, 9, execErr.ExitCode)
	})

	t.Run("InteractiveExecZeroExit", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{UseInit: true})
		require.NoError(t, rt.Start(ctx, name))

		var out strings.Builder
		err := rt.InteractiveExec(ctx, name, []string{"true"}, "", "", runtime.IOStreams{Out: &out, TTY: true})
		assert.NoError(t, err, "exit 0 stays nil")
	})

	t.Run("BindMountReadWrite", func(t *testing.T) {
		rt, ctx := setup(t)
		hostDir := t.TempDir()
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{
			UseInit: true,
			Mounts:  []runtime.MountSpec{{HostPath: hostDir, ContainerPath: "/mnt/test", ReadOnly: false}},
		})
		require.NoError(t, rt.Start(ctx, name))

		_, err := rt.Exec(ctx, name, []string{"sh", "-c", "echo hello > /mnt/test/output.txt"}, "")
		require.NoError(t, err)

		content, err := os.ReadFile(filepath.Join(hostDir, "output.txt"))
		require.NoError(t, err)
		assert.Contains(t, string(content), "hello")
	})

	t.Run("BindMountReadOnly", func(t *testing.T) {
		rt, ctx := setup(t)
		hostDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(hostDir, "readonly.txt"), []byte("original"), 0600))
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{
			UseInit: true,
			Mounts:  []runtime.MountSpec{{HostPath: hostDir, ContainerPath: "/mnt/test", ReadOnly: true}},
		})
		require.NoError(t, rt.Start(ctx, name))

		result, err := rt.Exec(ctx, name, []string{"cat", "/mnt/test/readonly.txt"}, "")
		require.NoError(t, err)
		assert.Equal(t, "original", result.Stdout)

		_, err = rt.Exec(ctx, name, []string{"sh", "-c", "echo modified > /mnt/test/readonly.txt"}, "")
		assert.Error(t, err, "write to RO mount should fail")
	})

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

	t.Run("StopIdempotent", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{UseInit: true})
		require.NoError(t, rt.Start(ctx, name))
		require.NoError(t, rt.Stop(ctx, name))
		assert.NoError(t, rt.Stop(ctx, name), "second Stop should be a no-op")
	})

	t.Run("RemoveIdempotent", func(t *testing.T) {
		rt, ctx := setup(t)
		name := createContainer(t, rt, ctx, runtime.InstanceConfig{UseInit: true})
		require.NoError(t, rt.Remove(ctx, name))
		assert.NoError(t, rt.Remove(ctx, name), "second Remove should be a no-op")
	})

	t.Run("IsReady", func(t *testing.T) {
		rt, ctx := setup(t)
		exists, err := rt.IsReady(ctx)
		require.NoError(t, err)
		assert.True(t, exists)
	})
}
