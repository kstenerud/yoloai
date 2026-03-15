//go:build integration

package podman

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	yoloairuntime "github.com/kstenerud/yoloai/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPodman_CreateStartStopRemove(t *testing.T) {
	rt, ctx := podmanSetup(t)

	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
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
	assert.ErrorIs(t, err, yoloairuntime.ErrNotFound)
}

func TestPodman_InspectRunning(t *testing.T) {
	rt, ctx := podmanSetup(t)

	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
		UseInit: true,
	})
	require.NoError(t, rt.Start(ctx, name))

	info, err := rt.Inspect(ctx, name)
	require.NoError(t, err)
	assert.True(t, info.Running)
}

func TestPodman_InspectStopped(t *testing.T) {
	rt, ctx := podmanSetup(t)

	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
		UseInit: true,
	})
	require.NoError(t, rt.Start(ctx, name))
	require.NoError(t, rt.Stop(ctx, name))

	info, err := rt.Inspect(ctx, name)
	require.NoError(t, err)
	assert.False(t, info.Running)
}

func TestPodman_InspectNotFound(t *testing.T) {
	rt, ctx := podmanSetup(t)

	_, err := rt.Inspect(ctx, "yoloai-nonexistent-container-xyz")
	assert.ErrorIs(t, err, yoloairuntime.ErrNotFound)
}

func TestPodman_ExecSimple(t *testing.T) {
	rt, ctx := podmanSetup(t)

	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
		UseInit: true,
	})
	require.NoError(t, rt.Start(ctx, name))

	result, err := rt.Exec(ctx, name, []string{"echo", "hello"}, "")
	require.NoError(t, err)
	assert.Equal(t, "hello", result.Stdout)
	assert.Equal(t, 0, result.ExitCode)
}

func TestPodman_ExecNonZeroExit(t *testing.T) {
	rt, ctx := podmanSetup(t)

	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
		UseInit: true,
	})
	require.NoError(t, rt.Start(ctx, name))

	result, err := rt.Exec(ctx, name, []string{"false"}, "")
	assert.Error(t, err)
	assert.Equal(t, 1, result.ExitCode)
}

func TestPodman_ExecNotRunning(t *testing.T) {
	rt, ctx := podmanSetup(t)

	// Create but don't start
	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
		UseInit: true,
	})

	_, err := rt.Exec(ctx, name, []string{"echo", "hello"}, "")
	assert.Error(t, err)
}

func TestPodman_BindMountReadWrite(t *testing.T) {
	rt, ctx := podmanSetup(t)

	hostDir := t.TempDir()

	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
		UseInit: true,
		Mounts: []yoloairuntime.MountSpec{
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

func TestPodman_BindMountReadOnly(t *testing.T) {
	rt, ctx := podmanSetup(t)

	hostDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "readonly.txt"), []byte("original"), 0600))

	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
		UseInit: true,
		Mounts: []yoloairuntime.MountSpec{
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

func TestPodman_ResourceLimits(t *testing.T) {
	rt, ctx := podmanSetup(t)

	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
		UseInit: true,
		Resources: &yoloairuntime.ResourceLimits{
			NanoCPUs: 1_000_000_000, // 1 CPU
			Memory:   256 * 1024 * 1024,
		},
	})

	// Verify by inspecting the container directly via Docker SDK
	info, err := rt.Runtime.Client().ContainerInspect(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, int64(1_000_000_000), info.HostConfig.NanoCPUs)
	assert.Equal(t, int64(256*1024*1024), info.HostConfig.Memory)
}

func TestPodman_PortBinding(t *testing.T) {
	rt, ctx := podmanSetup(t)

	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
		UseInit: true,
		Ports: []yoloairuntime.PortMapping{
			{HostPort: "0", InstancePort: "8080", Protocol: "tcp"},
		},
	})

	info, err := rt.Runtime.Client().ContainerInspect(ctx, name)
	require.NoError(t, err)

	// Verify port binding exists in container config
	bindings := info.HostConfig.PortBindings
	assert.NotEmpty(t, bindings, "port bindings should be set")
}

func TestPodman_NetworkNone(t *testing.T) {
	rt, ctx := podmanSetup(t)

	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
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

func TestPodman_StopIdempotent(t *testing.T) {
	rt, ctx := podmanSetup(t)

	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
		UseInit: true,
	})
	require.NoError(t, rt.Start(ctx, name))
	require.NoError(t, rt.Stop(ctx, name))

	// Stop again — should return nil
	assert.NoError(t, rt.Stop(ctx, name))
}

func TestPodman_RemoveIdempotent(t *testing.T) {
	rt, ctx := podmanSetup(t)

	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
		UseInit: true,
	})
	require.NoError(t, rt.Remove(ctx, name))

	// Remove again — should return nil
	assert.NoError(t, rt.Remove(ctx, name))
}

func TestPodman_ImageExists(t *testing.T) {
	rt, ctx := podmanSetup(t)

	exists, err := rt.ImageExists(ctx, "yoloai-base")
	require.NoError(t, err)
	assert.True(t, exists)

	exists, err = rt.ImageExists(ctx, "yoloai-nonexistent-image-xyz")
	require.NoError(t, err)
	assert.False(t, exists)
}

// Podman-specific tests

func TestPodman_RootlessUsernsKeepID(t *testing.T) {
	rt, ctx := podmanSetup(t)

	// Skip on macOS — Podman Machine uses a VM with different UID mapping behavior
	// This test is specific to Linux rootless Podman
	if runtime.GOOS == "darwin" {
		t.Skip("Skipping on macOS — test requires native Linux rootless Podman")
	}

	// Skip on rootful Podman (running as root)
	if !isRootless() {
		t.Skip("Skipping rootless test — running as root")
	}

	hostDir := t.TempDir()
	testFile := filepath.Join(hostDir, "test.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("test"), 0644))

	// Create container — should automatically get --userns=keep-id
	name := createTestContainer(t, rt, ctx, yoloairuntime.InstanceConfig{
		UseInit: true,
		Mounts: []yoloairuntime.MountSpec{
			{Source: hostDir, Target: "/mnt/test", ReadOnly: false},
		},
	})
	require.NoError(t, rt.Start(ctx, name))

	// Check file ownership inside container
	result, err := rt.Exec(ctx, name, []string{"stat", "-c", "%u:%g", "/mnt/test/test.txt"}, "")
	require.NoError(t, err)

	// With --userns=keep-id, files should not appear as root-owned (0:0)
	// The exact UID/GID mapping may vary by Podman configuration,
	// but it should preserve the host user's ownership
	assert.NotEqual(t, "0:0", result.Stdout, "file should not appear as root-owned with keep-id")
}

func TestPodman_Name(t *testing.T) {
	rt, ctx := podmanSetup(t)
	_ = ctx // unused but keep for consistency

	assert.Equal(t, "podman", rt.Name())
}
