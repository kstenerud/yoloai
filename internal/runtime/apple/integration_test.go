//go:build integration

// ABOUTME: Apple container backend integration tests against the live `container`
// ABOUTME: CLI. Opt-in via YOLOAI_TEST_APPLE=1 — builds a tiny image and runs real
// ABOUTME: per-container VMs, so it is not part of the default suite.

package apple

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const itestImage = "yoloai-apple-itest:latest"

func appleSetup(t *testing.T) (*Runtime, context.Context) {
	t.Helper()
	if os.Getenv("YOLOAI_TEST_APPLE") == "" {
		t.Skip("set YOLOAI_TEST_APPLE=1 to run apple container integration tests")
	}
	home := testutil.IsolatedHome(t)
	ctx := context.Background()
	rt, err := New(ctx, config.NewLayout(filepath.Join(home, ".yoloai")))
	require.NoError(t, err, "apple backend must be available (macOS 26 + Apple Silicon + container CLI)")
	_, _ = rt.runContainer(ctx, "system", "start") // idempotent
	return rt, ctx
}

// buildSleepImage builds a tiny long-running image. The context path is absolute
// (t.TempDir), which exercises AC1 (a relative `.` context is silently dropped)
// and AC3 (the builder VM must be started first).
func buildSleepImage(t *testing.T, rt *Runtime, ctx context.Context) {
	t.Helper()
	dir := t.TempDir()
	dockerfile := "FROM alpine:3.22\nENTRYPOINT [\"sleep\", \"3600\"]\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644))

	_, _ = rt.runContainer(ctx, "builder", "start") // idempotent (AC3)
	_, err := rt.runContainer(ctx, "build", "-t", itestImage, dir)
	require.NoError(t, err, "container build with an absolute context must succeed (AC1/AC3)")
	t.Cleanup(func() { _, _ = rt.runContainer(context.Background(), "image", "delete", itestImage) })
}

// TestApple_Lifecycle drives the full create→start→exec→stop→remove path plus a
// live :rw virtiofs mount, against the real CLI.
func TestApple_Lifecycle(t *testing.T) {
	rt, ctx := appleSetup(t)
	buildSleepImage(t, rt, ctx)

	host := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(host, "from-host.txt"), []byte("hi-from-host"), 0o644))

	const name = "yoloai-apple-itest"
	cfg := runtime.InstanceConfig{
		Name:     name,
		ImageRef: itestImage,
		Labels:   map[string]string{"com.yoloai.test": "1"},
		Mounts:   []runtime.MountSpec{{HostPath: host, ContainerPath: "/mnt/work"}},
	}
	require.NoError(t, rt.Create(ctx, cfg))
	t.Cleanup(func() { _ = rt.Remove(context.Background(), name) })

	require.NoError(t, rt.Start(ctx, name))

	info, err := rt.Inspect(ctx, name)
	require.NoError(t, err)
	assert.True(t, info.Running, "running after Start")
	assert.False(t, info.Suspended)

	// :rw mount, host → guest.
	res, err := rt.Exec(ctx, name, []string{"cat", "/mnt/work/from-host.txt"}, "")
	require.NoError(t, err)
	assert.Equal(t, "hi-from-host", res.Stdout)

	// :rw mount, guest → host (live propagation).
	_, err = rt.Exec(ctx, name, []string{"sh", "-c", "echo from-guest > /mnt/work/g.txt"}, "")
	require.NoError(t, err)
	data, rerr := os.ReadFile(filepath.Join(host, "g.txt"))
	require.NoError(t, rerr)
	assert.Equal(t, "from-guest", strings.TrimSpace(string(data)))

	// Exec exit-code propagation.
	res, _ = rt.Exec(ctx, name, []string{"sh", "-c", "exit 42"}, "")
	assert.Equal(t, 42, res.ExitCode)

	require.NoError(t, rt.Start(ctx, name), "Start is idempotent when already running")

	require.NoError(t, rt.Stop(ctx, name))
	info, err = rt.Inspect(ctx, name)
	require.NoError(t, err)
	assert.False(t, info.Running, "stopped after Stop")

	require.NoError(t, rt.Remove(ctx, name))
	_, err = rt.Inspect(ctx, name)
	assert.ErrorIs(t, err, runtime.ErrNotFound, "gone after Remove")
}

// TestApple_SetupBuildsBase exercises the real Setup path: start the apiserver +
// builder and build the actual yoloai-base image from our Dockerfile via
// `container build` (the first real test of our Dockerfile under Apple's
// builder). Then IsReady is true and a second Setup skips. This build is slow
// (full base image), so it lives behind the same YOLOAI_TEST_APPLE gate.
func TestApple_SetupBuildsBase(t *testing.T) {
	rt, ctx := appleSetup(t)

	// A real CacheDir so the staleness marker persists (production has one;
	// os.WriteFile won't mkdir).
	layout := config.NewLayout(t.TempDir())
	require.NoError(t, os.MkdirAll(layout.CacheDir(), 0o755))

	var buf bytes.Buffer
	require.NoError(t, rt.Setup(ctx, layout, "", &buf, slog.Default(), false),
		"Setup must build yoloai-base from our Dockerfile under Apple's builder")

	ready, err := rt.IsReady(ctx)
	require.NoError(t, err)
	assert.True(t, ready, "yoloai-base present after Setup")

	// Second Setup: image present + marker current → skip (no rebuild).
	var buf2 bytes.Buffer
	require.NoError(t, rt.Setup(ctx, layout, "", &buf2, slog.Default(), false))
	assert.NotContains(t, buf2.String(), "Building base image", "re-run must skip")
	assert.NotContains(t, buf2.String(), "rebuilding", "re-run must not hit NeedsBuild")
}
