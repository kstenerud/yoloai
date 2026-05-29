//go:build integration

package sandbox_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	dockerrt "github.com/kstenerud/yoloai/internal/runtime/docker"
	sandbox "github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/create"
	"github.com/kstenerud/yoloai/internal/sandbox/lifecycle"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/stretchr/testify/require"
)

// createSandbox runs the create pipeline through the carved create.Run entry
// point, building the same Deps the Engine would (F5.2d dissolved
// Engine.Create). EnsureSetup is already performed by integrationSetup.
func createSandbox(ctx context.Context, mgr *sandbox.Engine, opts sandbox.CreateOptions) (string, error) {
	return create.Run(ctx, state.Deps{Runtime: mgr.Runtime(), Layout: mgr.Layout(), Input: strings.NewReader("")}, opts)
}

func stopSandbox(ctx context.Context, mgr *sandbox.Engine, name string) error {
	return lifecycle.Stop(ctx, state.Deps{Runtime: mgr.Runtime(), Layout: mgr.Layout(), Input: strings.NewReader("")}, name)
}

func startSandbox(ctx context.Context, mgr *sandbox.Engine, name string, opts sandbox.StartOptions) (*sandbox.StartResult, error) {
	return lifecycle.Start(ctx, state.Deps{Runtime: mgr.Runtime(), Layout: mgr.Layout(), Input: strings.NewReader("")}, name, opts)
}

func resetSandbox(ctx context.Context, mgr *sandbox.Engine, opts sandbox.ResetOptions) (*sandbox.ResetResult, error) {
	return lifecycle.Reset(ctx, state.Deps{Runtime: mgr.Runtime(), Layout: mgr.Layout(), Input: strings.NewReader("")}, opts)
}

func destroySandbox(ctx context.Context, mgr *sandbox.Engine, name string) (*sandbox.DestroyResult, error) {
	return lifecycle.Destroy(ctx, state.Deps{Runtime: mgr.Runtime(), Layout: mgr.Layout(), Input: strings.NewReader("")}, name)
}

// integrationSetup sets HOME to a temp dir, connects to Docker,
// builds the base image, and returns a Engine. Uses t.Cleanup
// for automatic teardown.
func integrationSetup(t *testing.T) (*sandbox.Engine, context.Context) {
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
	dockerrt.RecordBuildChecksum(layout, "")

	rt, err := dockerrt.New(ctx)
	require.NoError(t, err, "Docker must be running for integration tests")
	t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup

	mgr := sandbox.NewEngine(rt, slog.Default(), strings.NewReader(""), sandbox.WithLayout(layout))
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
