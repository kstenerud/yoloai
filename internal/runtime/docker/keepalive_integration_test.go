// ABOUTME: Integration test for the keepalive_only entrypoint branch (S2 carve):
// ABOUTME: verifies a container started with keepalive_only=true runs agent-free.

//go:build integration

package docker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/testutil"
)

// TestKeepaliveOnly_AgentFreeStartup verifies the S2 carve's neutral keep-alive
// bring-up mode. When runtime-config.json has keepalive_only=true, the
// container must start and remain running on `sleep infinity` with no
// sandbox-setup.py launched.
func TestKeepaliveOnly_AgentFreeStartup(t *testing.T) {
	ctx := context.Background()
	rt, err := New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	const name = "yoloai-keepalive-it"
	_ = rt.Remove(ctx, name) // clear any leftover from a prior failed run

	// Build a minimal sandbox dir that entrypoint.py can use.
	sandboxDir := t.TempDir()
	logsDir := filepath.Join(sandboxDir, "logs")
	require.NoError(t, os.MkdirAll(logsDir, 0750))

	// Write runtime-config.json with keepalive_only: true.
	rcPath := filepath.Join(sandboxDir, "runtime-config.json")
	rc := map[string]any{
		"schema_version": 1,
		"keepalive_only": true,
		// Minimal required fields to avoid remap_uid panics: omit host_uid/
		// host_gid so usermod is skipped (empty-string comparison != any real
		// UID). network_isolated omitted → no iptables rules attempted.
	}
	rcBytes, err := json.Marshal(rc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(rcPath, rcBytes, 0640))

	cfg := runtime.InstanceConfig{
		Name:       name,
		ImageRef:   "yoloai-base",
		WorkingDir: "/",
		UseInit:    true,
		Mounts: []runtime.MountSpec{
			{
				HostPath:      rcPath,
				ContainerPath: "/yoloai/runtime-config.json",
				ReadOnly:      true,
			},
			{
				HostPath:      logsDir,
				ContainerPath: "/yoloai/logs",
				ReadOnly:      false,
			},
		},
	}

	require.NoError(t, rt.Create(ctx, cfg))
	t.Cleanup(func() { _ = rt.Remove(ctx, name) })
	require.NoError(t, rt.Start(ctx, name))

	// Wait for the container to be running (sleep infinity should hold it up).
	testutil.WaitForActive(ctx, t, rt, name, 15*time.Second)

	info, err := rt.Inspect(ctx, name)
	require.NoError(t, err)
	assert.True(t, info.Running, "container should be running under keepalive_only")

	// Give the entrypoint a moment to exec into sleep.
	time.Sleep(500 * time.Millisecond)

	// Assert agent-free: sandbox-setup.py must NOT be running.
	noAgent, _ := rt.Exec(ctx, name, []string{"pgrep", "-f", "sandbox-setup.py"}, "root")
	assert.NotEqual(t, 0, noAgent.ExitCode,
		"sandbox-setup.py must not be running under keepalive_only; pgrep should exit non-zero")

	// Assert the neutral holder is running.
	sleepProc, err := rt.Exec(ctx, name, []string{"pgrep", "-x", "sleep"}, "root")
	require.NoError(t, err)
	assert.Equal(t, 0, sleepProc.ExitCode,
		"sleep process (the neutral keeper) should be running under keepalive_only")
}
