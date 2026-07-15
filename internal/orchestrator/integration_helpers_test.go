//go:build integration

// ABOUTME: Shared integration-test helpers: create/stop/start/reset/destroy
// ABOUTME: wrappers over the state.Deps pipeline, per-backend (docker,
// ABOUTME: legacy-docker, podman) setup fixtures, and project/aux-dir fixtures.
package orchestrator_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
// dockerWarm guards the once-per-run docker bootstrap. It used to live in
// TestMain, which ran it unconditionally and so made docker a prerequisite for
// every test in this multi-backend package — including seatbelt, apple and tart
// tests that never touch a daemon (DF99). Owning it here means the first docker
// test pays for it and a run with no docker tests never connects at all.
var (
	dockerWarmOnce sync.Once
	dockerWarmErr  error
)

// warmDockerBase builds the docker base image once per test binary, so the
// per-test EnsureSetup calls below hit the cache and return in milliseconds
// instead of each rebuilding. Absence is reported to the caller rather than
// exiting: `make integration` already fails loudly via `make base-image` if the
// daemon is down (D112), so the second copy of that check here bought nothing
// and cost every non-docker test a docker dependency.
func warmDockerBase(ctx context.Context) error {
	dockerWarmOnce.Do(func() {
		step := testutil.TestMainBreadcrumb("sandbox")
		var rt *dockerrt.Runtime
		step("connecting to docker", func() {
			rt, dockerWarmErr = dockerrt.New(ctx, config.Layout{}.WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars)))
		})
		if dockerWarmErr != nil {
			return
		}
		defer rt.Close() //nolint:errcheck // best-effort close

		// Pre-seed the build-inputs checksum in this bootstrap HOME. `make
		// integration` builds the base image (via `make base-image`) immediately
		// before this binary runs, so the daemon already has yoloai-base:latest
		// matching the current embedded build inputs. Without the seed,
		// EnsureSetup finds no checksum and triggers a redundant rebuild, which
		// races the daemon's delete-then-create on the tag and surfaces as
		// "AlreadyExists after deleting the existing one". See
		// backend-idiosyncrasies.md "Docker daemon races on AlreadyExists when
		// rebuilding an existing tag with identical content".
		home, err := os.MkdirTemp("", "yoloai-warm-*")
		if err != nil {
			dockerWarmErr = fmt.Errorf("warm docker: temp home: %w", err)
			return
		}
		defer os.RemoveAll(home) //nolint:errcheck // best-effort cleanup
		layout := config.NewLayout(filepath.Join(home, ".yoloai"))
		if err := os.MkdirAll(layout.CacheDir(), 0750); err != nil { //nolint:forbidigo // test-edge dir create
			dockerWarmErr = fmt.Errorf("warm docker: cache dir: %w", err)
			return
		}
		dockerrt.RecordBuildChecksum(layout, "docker")

		// Capture the build output rather than discarding it, and attach it to the
		// error. There is no *testing.T here to log through, and io.Discard is what
		// reduced a real failure to a bare "docker build exited with code 1" with
		// the cause thrown away (DF97). On success the buffer is dropped.
		mgr := orchestrator.NewEngineWithRuntime(rt, slog.Default(), strings.NewReader(""), orchestrator.WithLayout(layout))
		var out bytes.Buffer
		step("ensuring base image is ready", func() {
			if err := mgr.EnsureSetup(ctx, &out); err != nil {
				dockerWarmErr = fmt.Errorf("warm docker base image: %w\n--- build output ---\n%s", err, out.String())
			}
		})
	})
	return dockerWarmErr
}

func integrationSetup(t *testing.T) (*orchestrator.Engine, context.Context) {
	t.Helper()
	ctx := context.Background()

	// Docker is this helper's backend, so this helper warms it (DF99).
	require.NoError(t, warmDockerBase(ctx), "docker must be running for docker integration tests")

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
	require.NoError(t, mgr.EnsureSetup(ctx, testutil.LogWriter(t)))

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
	require.NoError(t, mgr.EnsureSetup(ctx, testutil.LogWriter(t)))

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
	require.NoError(t, mgr.EnsureSetup(ctx, testutil.LogWriter(t)))

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
