//go:build integration

package orchestrator_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator"
	"github.com/kstenerud/yoloai/internal/orchestrator/create"
	"github.com/kstenerud/yoloai/internal/orchestrator/lifecycle"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
	podmanrt "github.com/kstenerud/yoloai/runtime/podman"
	"github.com/stretchr/testify/require"
)

// createSandbox runs the create pipeline through the carved create.Run entry
// point, building the same Deps the Engine would (F5.2d dissolved
// Engine.Create). EnsureSetup is already performed by integrationSetup.
func createSandbox(ctx context.Context, mgr *orchestrator.Engine, opts orchestrator.CreateOptions) (string, error) {
	return create.Run(ctx, state.Deps{Runtime: mgr.Runtime(), Layout: mgr.Layout(), Input: strings.NewReader("")}, opts)
}

func stopSandbox(ctx context.Context, mgr *orchestrator.Engine, name string) error {
	return lifecycle.Stop(ctx, state.Deps{Runtime: mgr.Runtime(), Layout: mgr.Layout(), Input: strings.NewReader("")}, name)
}

func startSandbox(ctx context.Context, mgr *orchestrator.Engine, name string, opts orchestrator.StartOptions) (*orchestrator.StartResult, error) {
	return lifecycle.Start(ctx, state.Deps{Runtime: mgr.Runtime(), Layout: mgr.Layout(), Input: strings.NewReader("")}, name, opts)
}

func resetSandbox(ctx context.Context, mgr *orchestrator.Engine, opts orchestrator.ResetOptions) (*orchestrator.ResetResult, error) {
	return lifecycle.Reset(ctx, state.Deps{Runtime: mgr.Runtime(), Layout: mgr.Layout(), Input: strings.NewReader("")}, opts)
}

func destroySandbox(ctx context.Context, mgr *orchestrator.Engine, name string) (*orchestrator.DestroyResult, error) {
	return lifecycle.Destroy(ctx, state.Deps{Runtime: mgr.Runtime(), Layout: mgr.Layout(), Input: strings.NewReader("")}, name)
}

// integrationSetup sets HOME to a temp dir, connects to Docker,
// builds the base image, and returns a Engine. Uses t.Cleanup
// for automatic teardown.
func integrationSetup(t *testing.T) (*orchestrator.Engine, context.Context) {
	t.Helper()
	ctx := context.Background()

	home := testutil.IsolatedHome(t)
	layout := config.NewLayout(filepath.Join(home, ".yoloai"))

	// Pre-seed the build-inputs checksum in the per-test HOME. Same
	// rationale as the TestMain bootstrap (integration_main_test.go:41):
	// `make integration` runs `make base-image` before this test
	// binary starts; every per-test integrationSetup creates a fresh
	// HOME via testutil.IsolatedHome and so loses the pre-seed unless
	// we re-apply it here. Without this, EnsureSetup re-builds against
	// the existing daemon image and intermittently hits the
	// AlreadyExists race documented in backend-idiosyncrasies.md
	// "Docker daemon races on AlreadyExists when rebuilding an
	// existing tag with identical content".
	require.NoError(t, os.MkdirAll(layout.CacheDir(), 0750))
	dockerrt.RecordBuildChecksum(layout, "docker")

	rt, err := dockerrt.New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	require.NoError(t, err, "Docker must be running for integration tests")
	t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup

	mgr := orchestrator.NewEngineWithRuntime(rt, slog.Default(), strings.NewReader(""), orchestrator.WithLayout(layout))
	require.NoError(t, mgr.EnsureSetup(ctx, io.Discard))

	return mgr, ctx
}

// legacyDockerRuntime wraps the docker Runtime but reports AgentFreeLaunch=false,
// forcing the legacy (in-entrypoint) bring-up + /run/secrets file delivery while
// keeping docker's real Launch/InjectorReach. It lets the legacy brokering path be
// exercised on real Docker without needing gVisor or another backend's host setup.
type legacyDockerRuntime struct {
	*dockerrt.Runtime
}

func (l *legacyDockerRuntime) Descriptor() runtime.BackendDescriptor {
	d := l.Runtime.Descriptor()
	d.Capabilities.AgentFreeLaunch = false
	return d
}

// legacyDockerIntegrationSetup mirrors integrationSetup but wraps the runtime so
// the engine takes the legacy launch path (secrets via /run/secrets files).
func legacyDockerIntegrationSetup(t *testing.T) (*orchestrator.Engine, context.Context) {
	t.Helper()
	ctx := context.Background()

	home := testutil.IsolatedHome(t)
	layout := config.NewLayout(filepath.Join(home, ".yoloai"))
	require.NoError(t, os.MkdirAll(layout.CacheDir(), 0750))
	dockerrt.RecordBuildChecksum(layout, "docker")

	rt, err := dockerrt.New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	require.NoError(t, err, "Docker must be running for integration tests")
	t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup

	mgr := orchestrator.NewEngineWithRuntime(&legacyDockerRuntime{Runtime: rt}, slog.Default(), strings.NewReader(""), orchestrator.WithLayout(layout))
	require.NoError(t, mgr.EnsureSetup(ctx, io.Discard))

	return mgr, ctx
}

// podmanIntegrationSetup mirrors integrationSetup on the Podman backend, to
// validate brokering on rootless podman: it takes the legacy launch path + the
// decoupled broker + the slirp InjectorReach. Skips when Podman isn't available.
// The build-checksum pre-seed is keyed to the podman image store (DF56); it avoids
// a rebuild as long as `yoloai system build --backend podman` has put a current
// image in Podman.
func podmanIntegrationSetup(t *testing.T) (*orchestrator.Engine, context.Context) {
	t.Helper()
	ctx := context.Background()

	home := testutil.IsolatedHome(t)
	layout := config.NewLayout(filepath.Join(home, ".yoloai"))
	require.NoError(t, os.MkdirAll(layout.CacheDir(), 0750))
	dockerrt.RecordBuildChecksum(layout, "podman")

	rt, err := podmanrt.New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	if err != nil {
		t.Skipf("Podman unavailable, skipping: %v", err)
	}
	t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup

	mgr := orchestrator.NewEngineWithRuntime(rt, slog.Default(), strings.NewReader(""), orchestrator.WithLayout(layout))
	require.NoError(t, mgr.EnsureSetup(ctx, io.Discard))

	return mgr, ctx
}

// createProjectDir creates a temp directory with a minimal Go project
// (main.go) and an initial git commit.
func createProjectDir(t *testing.T) string {
	t.Helper()
	return testutil.GoProject(t)
}

// createAuxDir creates a temp directory with a simple file for aux dir testing.
func createAuxDir(t *testing.T, name string) string {
	t.Helper()
	return testutil.AuxDir(t, name)
}
