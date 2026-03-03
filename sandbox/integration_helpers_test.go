//go:build integration

package sandbox

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
	"github.com/stretchr/testify/require"
)

// integrationSetup sets HOME to a temp dir, connects to Docker,
// builds the base image, and returns a Manager. Uses t.Cleanup
// for automatic teardown.
func integrationSetup(t *testing.T) (*Manager, context.Context) {
	t.Helper()
	ctx := context.Background()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	rt, err := dockerrt.New(ctx)
	require.NoError(t, err, "Docker must be running for integration tests")
	t.Cleanup(func() { rt.Close() }) //nolint:errcheck // test cleanup

	mgr := NewManager(rt, "docker", slog.Default(), strings.NewReader(""), io.Discard)
	require.NoError(t, mgr.EnsureSetup(ctx))

	return mgr, ctx
}

// createProjectDir creates a temp directory with a minimal Go project
// (main.go) for use as a workdir.
func createProjectDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "project")
	require.NoError(t, os.MkdirAll(dir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"),
		0600,
	))
	return dir
}

// createAuxDir creates a temp directory with a simple file for aux dir testing.
func createAuxDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(dir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "data.txt"),
		[]byte("aux data for "+name+"\n"),
		0600,
	))
	return dir
}
