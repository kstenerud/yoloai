//go:build integration

package sandbox

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/testutil"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
	"github.com/stretchr/testify/require"
)

// integrationSetup sets HOME to a temp dir, connects to Docker,
// builds the base image, and returns a Manager. Uses t.Cleanup
// for automatic teardown.
func integrationSetup(t *testing.T) (*Manager, context.Context) {
	t.Helper()
	ctx := context.Background()

	testutil.IsolatedHome(t)

	rt, err := dockerrt.New(ctx)
	require.NoError(t, err, "Docker must be running for integration tests")
	t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup

	mgr := NewManager(rt, slog.Default(), strings.NewReader(""), io.Discard)
	require.NoError(t, mgr.EnsureSetup(ctx))

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
