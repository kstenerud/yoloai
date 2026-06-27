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
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
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

	// The entrypoint runs as root and writes .substrate-ready (and its logs) into
	// the bind-mounted logs dir, so under rootful Docker (container root = host
	// root) those files are root-owned and the non-root test process can't unlink
	// them during t.TempDir cleanup. Remove them from inside the container, where
	// root can. Registered after Start so it runs (cleanups are LIFO) before the
	// rt.Remove and TempDir cleanups — while the container is still alive.
	t.Cleanup(func() {
		_, _ = rt.Exec(ctx, name, []string{"find", "/yoloai/logs", "-mindepth", "1", "-delete"}, "root")
	})

	// Wait for the container to be running (sleep infinity should hold it up).
	testutil.WaitForActive(ctx, t, rt, name, 15*time.Second)

	info, err := rt.Inspect(ctx, name)
	require.NoError(t, err)
	assert.True(t, info.Running, "container should be running under keepalive_only")

	// Wait until the neutral keeper (sleep) is the running holder. The entrypoint
	// writes .substrate-ready and then execs gosu→sleep, so poll for the process
	// rather than sleeping a fixed interval, which races a slow or loaded host
	// (rt.Exec returns an error when pgrep finds nothing and exits non-zero).
	require.Eventually(t, func() bool {
		r, _ := rt.Exec(ctx, name, []string{"pgrep", "-x", "sleep"}, "root")
		return r.ExitCode == 0
	}, 15*time.Second, 200*time.Millisecond, "sleep keeper (neutral holder) never started under keepalive_only")

	// Assert agent-free: sandbox-setup.py must NOT be running.
	noAgent, _ := rt.Exec(ctx, name, []string{"pgrep", "-f", "sandbox-setup.py"}, "root")
	assert.NotEqual(t, 0, noAgent.ExitCode,
		"sandbox-setup.py must not be running under keepalive_only; pgrep should exit non-zero")
}
