//go:build integration && linux

// ABOUTME: Full lifecycle (create/start/exec/stop/restart/remove) and shared
// ABOUTME: conformance-suite coverage against a real containerd + Kata daemon.
package containerdrt

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
	"github.com/kstenerud/yoloai/runtime/runtimetest"
)

const containerdSocket = "/run/containerd/containerd.sock"

// requireAvailable fails the test when containerd is not actually usable on this
// host. containerd is a platform-possible backend on Linux, so its absence is a
// misconfiguration to fix, not a reason to silently skip (D112) — testutil.
// RequireBackend FAILs unless "containerd" is carved out via
// YOLOAI_TEST_UNCONTROLLED_BACKENDS (the CI path), in which case it skips.
//
// The probe is three-staged because each layer can be absent independently: the
// socket file can exist yet be unconnectable — the daemon is down, or the test
// user lacks dial permission (the socket is commonly root-owned srw-rw----), so a
// bare os.Stat would pass and every test would then fail at first use. And a
// reachable daemon still isn't sufficient: every test brings up CNI networking,
// which creates a named network namespace needing CAP_SYS_ADMIN + CAP_DAC_OVERRIDE
// (typically root); on an unprivileged host the daemon dials fine but Create fails
// at "create netns: operation not permitted". Each stage yields a specific reason.
func requireAvailable(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(containerdSocket); err != nil {
		testutil.RequireBackend(t, "containerd", fmt.Sprintf("%s not found", containerdSocket))
	}
	conn, err := net.DialTimeout("unix", containerdSocket, 2*time.Second)
	if err != nil {
		testutil.RequireBackend(t, "containerd", fmt.Sprintf("%s exists but is not connectable: %v", containerdSocket, err))
	} else {
		_ = conn.Close()
	}
	if err := canCreateNetNSFunc(); err != nil {
		testutil.RequireBackend(t, "containerd", fmt.Sprintf("reachable but this host cannot create network namespaces (needs CAP_SYS_ADMIN/root): %v", err))
	}
}

// testLayout constructs a Layout rooted at an isolated home for an
// integration test — Q-W: every runtime.New caller must pass a Layout.
func testLayout(t *testing.T) config.Layout {
	t.Helper()
	home := testutil.IsolatedHome(t)
	return config.NewLayout(filepath.Join(home, ".yoloai")).WithPrincipal(config.CLIPrincipal)
}

// testSandboxDir creates a temporary directory that acts as a sandbox dir.
func testSandboxDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

// TestIntegration_New verifies that New() connects to containerd successfully.
func TestIntegration_New(t *testing.T) {
	requireAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx, testLayout(t))
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	assert.Equal(t, runtime.BackendContainerd, rt.Descriptor().Type)
}

// TestIntegration_IsReady_False verifies IsReady returns false when yoloai-base is not imported.
func TestIntegration_IsReady_False(t *testing.T) {
	requireAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx, testLayout(t))
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
	requireAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx, testLayout(t))
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
	requireAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx, testLayout(t))
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
	requireAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx, testLayout(t))
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	// Check if the backend is ready before attempting lifecycle test.
	exists, err := rt.IsReady(ctx)
	require.NoError(t, err)
	if !exists {
		t.Skip("yoloai-base image not found in containerd yoloai namespace; run 'yoloai system setup' first")
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

	// InteractiveExec must propagate a non-zero inner exit as *runtime.ExecError
	// carrying the code (regression: it used to discard the exit status and
	// always return nil, so `yoloai exec -- false` exited 0 on this backend).
	ierr := rt.InteractiveExec(ctx, name, []string{"false"}, "", "",
		runtime.IOStreams{In: nil, Out: io.Discard, Err: io.Discard})
	var execErr *runtime.ExecError
	require.ErrorAs(t, ierr, &execErr, "non-zero interactive exit must surface as *runtime.ExecError")
	assert.Equal(t, 1, execErr.ExitCode)

	// InteractiveExec with a zero exit returns nil.
	require.NoError(t, rt.InteractiveExec(ctx, name, []string{"true"}, "", "",
		runtime.IOStreams{In: nil, Out: io.Discard, Err: io.Discard}))

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

// TestIntegration_DefaultRuntimeCreate covers the plain `--backend containerd`
// path: default container isolation, where InstanceConfig.ContainerRuntime is ""
// (the backend-default sentinel from runtime.IsolationContainerRuntime). That
// path was dead on arrival — containerd rejects an empty Runtime.Name at Create
// with "container.Runtime.Name must be set" — and nothing here caught it because
// every other case forces a Kata runtime: TestIntegration_ContainerLifecycle
// hardcodes it and the conformance sleeper rewrites "" → kata. This creates with
// the empty default, asserts the container was stamped with the runc shim (the
// resolver's job), and runs it to prove the runtime is functional.
func TestIntegration_DefaultRuntimeCreate(t *testing.T) {
	requireAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx, testLayout(t))
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	exists, err := rt.IsReady(ctx)
	require.NoError(t, err)
	if !exists {
		t.Skip("yoloai-base image not found in containerd yoloai namespace; run 'yoloai system setup' first")
	}

	name := "yoloai-integration-runc"
	cfg := runtime.InstanceConfig{
		Name:       name,
		ImageRef:   "yoloai-base",
		WorkingDir: "/",
		// ContainerRuntime deliberately left "" — the default container isolation,
		// the case the regression broke.
	}

	_ = rt.Remove(ctx, name) // clear any leftover from a previous run
	require.NoError(t, rt.Create(ctx, cfg), "Create must succeed for the default (empty) runtime")
	defer func() {
		_ = rt.Stop(ctx, name)
		_ = rt.Remove(ctx, name)
	}()

	// The empty sentinel must have been resolved to the runc shim at the
	// containerd boundary — inspect the container's stamped runtime directly.
	nsCtx := rt.withNamespace(ctx)
	c, err := rt.client.LoadContainer(nsCtx, name)
	require.NoError(t, err)
	info, err := c.Info(nsCtx)
	require.NoError(t, err)
	assert.Equal(t, defaultRuntime, info.Runtime.Name, "empty runtime must resolve to the runc shim")

	// And it actually boots and runs under runc (no Kata required for this path).
	require.NoError(t, rt.Start(ctx, name), "Start should succeed")
	result, err := rt.Exec(ctx, name, []string{"echo", "hello"}, "")
	require.NoError(t, err)
	assert.Contains(t, result.Stdout, "hello")
}

// TestContainerdConformance runs the shared, backend-agnostic behavioral
// conformance suite against a live containerd + Kata daemon, so containerd
// verifies the same lifecycle / exec / mount contract as docker, podman, and
// apple. The sleeper is the yoloai-base image (its entrypoint idles, so the
// container stays alive for exec) created under the Kata shim. The stdio section
// auto-skips (containerd does not implement runtime.StdioExecer). The bespoke
// TestIntegration_ContainerLifecycle is retained for the containerd-unique
// restart-after-stop / stopped-task-cleanup guard the shared suite does not cover.
func TestContainerdConformance(t *testing.T) {
	requireAvailable(t)

	runtimetest.RunInterfaceConformance(t, func(t *testing.T) runtimetest.InterfaceBackend {
		ctx := context.Background()
		rt, err := New(ctx, testLayout(t))
		require.NoError(t, err)
		t.Cleanup(func() { _ = rt.Close() }) //nolint:errcheck // best-effort close

		ready, err := rt.IsReady(ctx)
		require.NoError(t, err)
		if !ready {
			t.Skip("yoloai-base not imported into containerd yoloai namespace; run 'yoloai system setup' first")
		}

		return runtimetest.InterfaceBackend{
			Runtime: rt,
			Ctx:     ctx,
			NewSleeper: func(t *testing.T, cfg runtime.InstanceConfig) string {
				if cfg.ImageRef == "" {
					cfg.ImageRef = "yoloai-base"
				}
				if cfg.ContainerRuntime == "" {
					cfg.ContainerRuntime = "io.containerd.kata.v2"
				}
				if cfg.WorkingDir == "" {
					cfg.WorkingDir = "/"
				}
				_ = rt.Remove(ctx, cfg.Name) // evict any stale leftover from a failed run
				require.NoError(t, rt.Create(ctx, cfg))
				t.Cleanup(func() { _ = rt.Remove(context.Background(), cfg.Name) })
				return cfg.Name
			},
		}
	})
}

// TestIntegration_Prune verifies that Prune removes orphaned containers.
func TestIntegration_Prune(t *testing.T) {
	requireAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx, testLayout(t))
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	// Prune with an empty known list — should not error.
	result, err := rt.Prune(ctx, []string{}, true /* dryRun */, os.Stdout)
	require.NoError(t, err)
	t.Logf("Prune found %d orphaned items", len(result.Items))
}

// TestIntegration_Logs verifies Logs returns a string without error.
func TestIntegration_Logs(t *testing.T) {
	requireAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx, testLayout(t))
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	// Logs for a non-existent sandbox returns empty string (no panic).
	logs := rt.Logs(ctx, "yoloai-nonexistent", 50)
	assert.Equal(t, "", logs)
}

// TestIntegration_DiagHint verifies DiagHint returns a non-empty string.
func TestIntegration_DiagHint(t *testing.T) {
	requireAvailable(t)

	ctx := context.Background()
	rt, err := New(ctx, testLayout(t))
	require.NoError(t, err)
	defer rt.Close() //nolint:errcheck // best-effort close

	hint := rt.DiagHint("yoloai-mybox")
	assert.NotEmpty(t, hint)
	assert.Contains(t, hint, "ctr")
}
