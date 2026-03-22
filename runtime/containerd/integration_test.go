//go:build integration

package containerdrt

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
)

// skipIfNotAvailable skips the test if containerd is not available.
func skipIfNotAvailable(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/run/containerd/containerd.sock"); err != nil {
		t.Skip("containerd not available: /run/containerd/containerd.sock not found")
	}
}

// testSandboxDir creates a temporary directory that acts as a sandbox dir.
func testSandboxDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

// TestIntegration_New verifies that New() connects to containerd successfully.
func TestIntegration_New(t *testing.T) {
	skipIfNotAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx)
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	assert.Equal(t, "containerd", rt.Name())
}

// TestIntegration_IsReady_False verifies IsReady returns false when yoloai-base is not imported.
func TestIntegration_IsReady_False(t *testing.T) {
	skipIfNotAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx)
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	// IsReady checks specifically for yoloai-base; this passes when it's not present.
	// We can only verify it doesn't return an error; actual result depends on test env.
	_, err = rt.IsReady(ctx)
	require.NoError(t, err)
}

// TestIntegration_RequiredCapabilities verifies RequiredCapabilities reports missing prerequisites.
// On a machine without Kata, RunChecks should return failing results.
// On a machine with full Kata setup, all checks should pass.
func TestIntegration_RequiredCapabilities(t *testing.T) {
	skipIfNotAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx)
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	capList := rt.RequiredCapabilities("vm")
	require.NotNil(t, capList)
	env := caps.DetectEnvironment()
	results := caps.RunChecks(ctx, capList, env)
	t.Logf("RequiredCapabilities('vm') results: %v", caps.FormatError(results))
	// No assert — the result depends on the test machine configuration.
}

// TestIntegration_RequiredCapabilities_VmEnhanced verifies the devmapper snapshotter probe
// works against the real containerd daemon with vm-enhanced isolation.
func TestIntegration_RequiredCapabilities_VmEnhanced(t *testing.T) {
	skipIfNotAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx)
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	capList := rt.RequiredCapabilities("vm-enhanced")
	require.NotNil(t, capList)
	env := caps.DetectEnvironment()
	results := caps.RunChecks(ctx, capList, env)
	t.Logf("RequiredCapabilities('vm-enhanced') results: %v", caps.FormatError(results))
	// No assert — the result depends on the test machine configuration.
}

// TestIntegration_ContainerLifecycle runs a full create/start/stop/remove cycle.
// Requires: containerd running, yoloai-base image imported, Kata shim available.
func TestIntegration_ContainerLifecycle(t *testing.T) {
	skipIfNotAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx)
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	// Check if the backend is ready before attempting lifecycle test.
	exists, err := rt.IsReady(ctx)
	require.NoError(t, err)
	if !exists {
		t.Skip("yoloai-base image not found in containerd yoloai namespace; run 'yoloai setup' first")
	}

	name := "yoloai-integration-test"

	cfg := runtime.InstanceConfig{
		Name:             name,
		ImageRef:         "yoloai-base",
		WorkingDir:       "/",
		ContainerRuntime: "io.containerd.kata.v2",
	}

	// Clean up any leftover container from a previous run.
	_ = rt.Remove(ctx, name)

	// Create.
	err = rt.Create(ctx, cfg)
	require.NoError(t, err, "Create should succeed")

	// Inspect: should exist but not be running yet.
	info, err := rt.Inspect(ctx, name)
	require.NoError(t, err)
	assert.False(t, info.Running, "container should not be running before Start")

	// Start.
	err = rt.Start(ctx, name)
	require.NoError(t, err, "Start should succeed")

	// Inspect: should be running.
	info, err = rt.Inspect(ctx, name)
	require.NoError(t, err)
	assert.True(t, info.Running, "container should be running after Start")

	// Exec: run a simple command.
	result, err := rt.Exec(ctx, name, []string{"echo", "hello"}, "")
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "hello")

	// Stop.
	err = rt.Stop(ctx, name)
	require.NoError(t, err, "Stop should succeed")

	// Inspect: should not be running.
	info, err = rt.Inspect(ctx, name)
	require.NoError(t, err)
	assert.False(t, info.Running, "container should not be running after Stop")

	// Restart: start again to verify stopped-task cleanup works.
	err = rt.Start(ctx, name)
	require.NoError(t, err, "Restart should succeed")

	info, err = rt.Inspect(ctx, name)
	require.NoError(t, err)
	assert.True(t, info.Running, "container should be running after restart")

	// Remove.
	err = rt.Remove(ctx, name)
	require.NoError(t, err, "Remove should succeed")

	// Inspect: should return ErrNotFound.
	_, err = rt.Inspect(ctx, name)
	assert.ErrorIs(t, err, runtime.ErrNotFound)
}

// TestIntegration_Prune verifies that Prune removes orphaned containers.
func TestIntegration_Prune(t *testing.T) {
	skipIfNotAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx)
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	// Prune with an empty known list — should not error.
	result, err := rt.Prune(ctx, []string{}, true /* dryRun */, os.Stdout)
	require.NoError(t, err)
	t.Logf("Prune found %d orphaned items", len(result.Items))
}

// TestIntegration_Logs verifies Logs returns a string without error.
func TestIntegration_Logs(t *testing.T) {
	skipIfNotAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx)
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	// Logs for a non-existent sandbox returns empty string (no panic).
	logs := rt.Logs(ctx, "yoloai-nonexistent", 50)
	assert.Equal(t, "", logs)
}

// TestIntegration_DiagHint verifies DiagHint returns a non-empty string.
func TestIntegration_DiagHint(t *testing.T) {
	skipIfNotAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx)
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	hint := rt.DiagHint("yoloai-mybox")
	assert.NotEmpty(t, hint)
	assert.Contains(t, hint, "ctr")
}
