//go:build integration

package docker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocker_CreateStartStopRemove(t *testing.T) {
	rt, ctx := dockerSetup(t)

	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit: true,
	})

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
}

func TestDocker_InspectRunning(t *testing.T) {
	rt, ctx := dockerSetup(t)

	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit: true,
	})
	require.NoError(t, rt.Start(ctx, name))

	info, err := rt.Inspect(ctx, name)
	require.NoError(t, err)
	assert.True(t, info.Running)
}

func TestDocker_InspectStopped(t *testing.T) {
	rt, ctx := dockerSetup(t)

	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit: true,
	})
	require.NoError(t, rt.Start(ctx, name))
	require.NoError(t, rt.Stop(ctx, name))

	info, err := rt.Inspect(ctx, name)
	require.NoError(t, err)
	assert.False(t, info.Running)
}

func TestDocker_InspectNotFound(t *testing.T) {
	rt, ctx := dockerSetup(t)

	_, err := rt.Inspect(ctx, "yoloai-nonexistent-container-xyz")
	assert.ErrorIs(t, err, runtime.ErrNotFound)
}

func TestDocker_ExecSimple(t *testing.T) {
	rt, ctx := dockerSetup(t)

	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit: true,
	})
	require.NoError(t, rt.Start(ctx, name))

	result, err := rt.Exec(ctx, name, []string{"echo", "hello"}, "")
	require.NoError(t, err)
	assert.Equal(t, "hello", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)
}

func TestDocker_ExecNonZeroExit(t *testing.T) {
	rt, ctx := dockerSetup(t)

	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit: true,
	})
	require.NoError(t, rt.Start(ctx, name))

	result, err := rt.Exec(ctx, name, []string{"false"}, "")
	assert.Error(t, err)
	assert.Equal(t, 1, result.ExitCode)
}

func TestDocker_ExecNotRunning(t *testing.T) {
	rt, ctx := dockerSetup(t)

	// Create but don't start
	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit: true,
	})

	_, err := rt.Exec(ctx, name, []string{"echo", "hello"}, "")
	assert.Error(t, err)
}

func TestDocker_BindMountReadWrite(t *testing.T) {
	rt, ctx := dockerSetup(t)

	hostDir := t.TempDir()

	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit: true,
		Mounts: []runtime.MountSpec{
			{Source: hostDir, Target: "/mnt/test", ReadOnly: false},
		},
	})
	require.NoError(t, rt.Start(ctx, name))

	_, err := rt.Exec(ctx, name, []string{"sh", "-c", "echo hello > /mnt/test/output.txt"}, "")
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(hostDir, "output.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "hello")
}

func TestDocker_BindMountReadOnly(t *testing.T) {
	rt, ctx := dockerSetup(t)

	hostDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "readonly.txt"), []byte("original"), 0600))

	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit: true,
		Mounts: []runtime.MountSpec{
			{Source: hostDir, Target: "/mnt/test", ReadOnly: true},
		},
	})
	require.NoError(t, rt.Start(ctx, name))

	// Read should succeed
	result, err := rt.Exec(ctx, name, []string{"cat", "/mnt/test/readonly.txt"}, "")
	require.NoError(t, err)
	assert.Equal(t, "original", result.Stdout)

	// Write should fail
	_, err = rt.Exec(ctx, name, []string{"sh", "-c", "echo modified > /mnt/test/readonly.txt"}, "")
	assert.Error(t, err, "write to RO mount should fail")
}

func TestDocker_ResourceLimits(t *testing.T) {
	rt, ctx := dockerSetup(t)

	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit: true,
		Resources: &runtime.ResourceLimits{
			NanoCPUs: 1_000_000_000, // 1 CPU
			Memory:   256 * 1024 * 1024,
		},
	})

	// Verify by inspecting the container directly via Docker SDK
	info, err := rt.client.ContainerInspect(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, int64(1_000_000_000), info.HostConfig.NanoCPUs)
	assert.Equal(t, int64(256*1024*1024), info.HostConfig.Memory)
}

func TestDocker_PortBinding(t *testing.T) {
	rt, ctx := dockerSetup(t)

	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit: true,
		Ports: []runtime.PortMapping{
			{HostPort: "0", InstancePort: "8080", Protocol: "tcp"},
		},
	})

	info, err := rt.client.ContainerInspect(ctx, name)
	require.NoError(t, err)

	// Verify port binding exists in container config
	bindings := info.HostConfig.PortBindings
	assert.NotEmpty(t, bindings, "port bindings should be set")
}

func TestDocker_NetworkNone(t *testing.T) {
	rt, ctx := dockerSetup(t)

	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit:     true,
		NetworkMode: "none",
	})
	require.NoError(t, rt.Start(ctx, name))

	// Network should be disabled — ping should fail
	result, err := rt.Exec(ctx, name, []string{"sh", "-c", "cat /sys/class/net/eth0/operstate 2>/dev/null || echo nodev"}, "")
	// With network=none, there's no eth0 device
	if err == nil {
		assert.Contains(t, result.Stdout, "nodev")
	}
	// The point is that network is isolated; either no device or error is fine
}

func TestDocker_StopIdempotent(t *testing.T) {
	rt, ctx := dockerSetup(t)

	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit: true,
	})
	require.NoError(t, rt.Start(ctx, name))
	require.NoError(t, rt.Stop(ctx, name))

	// Stop again — should return nil
	assert.NoError(t, rt.Stop(ctx, name))
}

func TestDocker_RemoveIdempotent(t *testing.T) {
	rt, ctx := dockerSetup(t)

	name := createTestContainer(t, rt, ctx, runtime.InstanceConfig{
		UseInit: true,
	})
	require.NoError(t, rt.Remove(ctx, name))

	// Remove again — should return nil
	assert.NoError(t, rt.Remove(ctx, name))
}

func TestDocker_ImageExists(t *testing.T) {
	rt, ctx := dockerSetup(t)

	exists, err := rt.ImageExists(ctx, "yoloai-base")
	require.NoError(t, err)
	assert.True(t, exists)

	exists, err = rt.ImageExists(ctx, "yoloai-nonexistent-image-xyz")
	require.NoError(t, err)
	assert.False(t, exists)
}
