//go:build integration

// ABOUTME: Shared integration-test helpers: create/stop/start/reset/destroy
// ABOUTME: wrappers over the state.Deps pipeline, per-backend (docker,
// ABOUTME: legacy-docker, podman) setup fixtures, and project/aux-dir fixtures.
package orchestrator_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/orchestrator"
	"github.com/kstenerud/yoloai/internal/orchestrator/create"
	"github.com/kstenerud/yoloai/internal/orchestrator/lifecycle"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
	podmanrt "github.com/kstenerud/yoloai/runtime/podman"
	"github.com/kstenerud/yoloai/store"
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

// startAndWaitActive starts a freshly-created sandbox and blocks until its
// container is active. Copy-mode diff/apply runs git inside the sandbox (audit
// C1), so tests that diff/apply must bring the box up first — create only
// provisions, it does not launch the container.
func startAndWaitActive(ctx context.Context, t *testing.T, mgr *orchestrator.Engine, name string) {
	t.Helper()
	_, err := startSandbox(ctx, mgr, name, orchestrator.StartOptions{})
	require.NoError(t, err)
	testutil.WaitForActive(ctx, t, mgr.Runtime(), store.InstanceName(mgr.Layout().Principal, name), 30*time.Second)
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
	// Thread the curated host env into the Engine's layout so host-side git (the
	// copy-mode baseline) sees SUDO_UID. Production does this via cliutil's
	// processEnv; without it, `sudo make integration` runs git as root against a
	// SUDO_UID-owned work copy with no SUDO_UID in git's env, and git rejects it
	// with "dubious ownership" (mirrors sysexec.GitEnv). A no-op off-sudo.
	layout := config.NewLayout(filepath.Join(home, ".yoloai")).WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars))

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
	// Thread the curated host env into the Engine's layout so host-side git (the
	// copy-mode baseline) sees SUDO_UID. Production does this via cliutil's
	// processEnv; without it, `sudo make integration` runs git as root against a
	// SUDO_UID-owned work copy with no SUDO_UID in git's env, and git rejects it
	// with "dubious ownership" (mirrors sysexec.GitEnv). A no-op off-sudo.
	layout := config.NewLayout(filepath.Join(home, ".yoloai")).WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars))
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
// decoupled broker + the slirp InjectorReach.
//
// These tests belong to the podman-owning suite (`make integration-podman`),
// which provisions rootless podman (builds the image, starts the user socket)
// and signals ownership with YOLOAI_TEST_BACKEND=podman. The docker-owning
// `make integration` job has podman *installed* on the runner but not
// *provisioned* (no rootless socket/slirp host-loopback for brokering, no
// keep-id UID mapping — container-written files land root-owned and defeat
// t.TempDir cleanup), so running there is a false failure. The env check below
// is job-scoping, NOT an opportunistic "skip if podman is missing": once podman
// IS the backend under test, it is REQUIRED — a missing/unprovisioned podman is
// a hard failure we want surfaced, not silently skipped.
//
// The build-checksum pre-seed is keyed to the podman image store (DF56); it avoids
// a rebuild as long as `yoloai system build --backend podman` has put a current
// image in Podman.
func podmanIntegrationSetup(t *testing.T) (*orchestrator.Engine, context.Context) {
	t.Helper()
	if os.Getenv("YOLOAI_TEST_BACKEND") != "podman" {
		t.Skip("podman orchestrator tests run under the podman suite (make integration-podman, YOLOAI_TEST_BACKEND=podman)")
	}
	ctx := context.Background()

	home := testutil.IsolatedHome(t)
	// Thread the curated host env into the Engine's layout so host-side git (the
	// copy-mode baseline) sees SUDO_UID. Production does this via cliutil's
	// processEnv; without it, `sudo make integration` runs git as root against a
	// SUDO_UID-owned work copy with no SUDO_UID in git's env, and git rejects it
	// with "dubious ownership" (mirrors sysexec.GitEnv). A no-op off-sudo.
	layout := config.NewLayout(filepath.Join(home, ".yoloai")).WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars))
	require.NoError(t, os.MkdirAll(layout.CacheDir(), 0750))
	dockerrt.RecordBuildChecksum(layout, "podman")

	rt, err := podmanrt.New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
	require.NoError(t, err, "podman must be available and provisioned when YOLOAI_TEST_BACKEND=podman")
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
