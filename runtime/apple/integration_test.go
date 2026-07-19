//go:build integration

// ABOUTME: Apple container backend integration tests against the live `container`
// ABOUTME: CLI. Availability is gated in TestMain (macOS 26 + Apple Silicon +
// ABOUTME: container CLI); on any other host the suite skips cleanly. Builds a
// ABOUTME: tiny image and runs real per-container VMs.

package apple

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/runtimetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const itestImage = "yoloai-apple-itest:latest"

func appleSetup(t *testing.T) (*Runtime, context.Context) {
	t.Helper()
	home := testutil.IsolatedHome(t)
	ctx := context.Background()
	rt, err := New(ctx, config.NewLayout(filepath.Join(home, ".yoloai")).WithPrincipal(config.CLIPrincipal))
	require.NoError(t, err, "apple backend must be available (macOS 26 + Apple Silicon + container CLI)")
	_, _ = rt.runContainer(ctx, "system", "start") // idempotent
	return rt, ctx
}

var (
	sleepImageOnce sync.Once
	sleepImageErr  error
)

// ensureSleepImage builds the tiny long-running test image once for the whole
// package. The context path is absolute (a temp dir), which also exercises AC1
// (a relative `.` context is silently dropped) and AC3 (the builder VM must be
// started first). The image persists for the run — it is a tiny alpine reused
// across every subtest, and skipping per-test deletion avoids a teardown race
// when several conformance subtests share it.
func ensureSleepImage(rt *Runtime, ctx context.Context) error {
	sleepImageOnce.Do(func() {
		dir, err := os.MkdirTemp("", "apple-sleep-*")
		if err != nil {
			sleepImageErr = err
			return
		}
		defer os.RemoveAll(dir) //nolint:errcheck // best-effort cleanup
		dockerfile := "FROM alpine:3.22\nENTRYPOINT [\"sleep\", \"3600\"]\n"
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil { //nolint:gosec // G306: test file, no secret content
			sleepImageErr = err
			return
		}
		_, _ = rt.runContainer(ctx, "builder", "start") // idempotent (AC3)
		if _, err := rt.runContainer(ctx, "build", "-t", itestImage, dir); err != nil {
			sleepImageErr = err
		}
	})
	return sleepImageErr
}

// buildSleepImage is the t-scoped wrapper used by the bespoke lifecycle test.
func buildSleepImage(t *testing.T, rt *Runtime, ctx context.Context) {
	t.Helper()
	require.NoError(t, ensureSleepImage(rt, ctx), "container build with an absolute context must succeed (AC1/AC3)")
}

// TestAppleConformance runs the shared, backend-agnostic behavioral conformance
// suite against the live `container` CLI, so apple verifies the same lifecycle /
// exec / mount contract as docker, podman, and containerd. The sleeper is a tiny
// alpine "sleep" image; the stdio section auto-skips (apple does not implement
// runtime.StdioExecer).
func TestAppleConformance(t *testing.T) {
	runtimetest.RunInterfaceConformance(t, func(t *testing.T) runtimetest.InterfaceBackend {
		rt, ctx := appleSetup(t)
		require.NoError(t, ensureSleepImage(rt, ctx))
		return runtimetest.InterfaceBackend{
			Runtime: rt,
			Ctx:     ctx,
			// Each apple container is its own lightweight VM; share one across the
			// read-only subtests to skip its per-subtest boots (speedup plan, lever 1).
			SharesReadOnlyInstance: true,
			NewSleeper: func(t *testing.T, cfg runtime.InstanceConfig) string {
				if cfg.ImageRef == "" {
					cfg.ImageRef = itestImage
				}
				_ = rt.Remove(ctx, cfg.Name) // evict any stale leftover from a failed run
				require.NoError(t, rt.Create(ctx, cfg))
				t.Cleanup(func() { _ = rt.Remove(context.Background(), cfg.Name) })
				return cfg.Name
			},
		}
	})
}

// TestApple_Lifecycle drives the full create→start→exec→stop→remove path plus a
// live :rw virtiofs mount, against the real CLI.
func TestApple_Lifecycle(t *testing.T) {
	rt, ctx := appleSetup(t)
	buildSleepImage(t, rt, ctx)

	host := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(host, "from-host.txt"), []byte("hi-from-host"), 0o644)) //nolint:gosec // G306: test file, no secret content

	// A single-FILE mount — yoloai injects seed/credential files this way
	// (e.g. ~/.claude.json). `--mount type=virtiofs` rejects a file source, so
	// Create must use -v; this guards that regression.
	seedFile := filepath.Join(t.TempDir(), "seed.json")
	require.NoError(t, os.WriteFile(seedFile, []byte("seed-data"), 0o644)) //nolint:gosec // G306: test file, no secret content

	const name = "yoloai-apple-itest"
	cfg := runtime.InstanceConfig{
		Name:     name,
		ImageRef: itestImage,
		Labels:   map[string]string{"com.yoloai.test": "1"},
		Mounts: []runtime.MountSpec{
			{HostPath: host, ContainerPath: "/mnt/work"},
			{HostPath: seedFile, ContainerPath: "/home/seed.json", ReadOnly: true},
		},
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
	data, rerr := os.ReadFile(filepath.Join(host, "g.txt")) //nolint:gosec // G304: path under t.TempDir()
	require.NoError(t, rerr)
	assert.Equal(t, "from-guest", strings.TrimSpace(string(data)))

	// Single-file (read-only) mount is readable in the guest.
	res, err = rt.Exec(ctx, name, []string{"cat", "/home/seed.json"}, "")
	require.NoError(t, err)
	assert.Equal(t, "seed-data", res.Stdout)

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
	if os.Getenv("YOLOAI_TEST_APPLE_BASE") != "1" {
		t.Skip("set YOLOAI_TEST_APPLE_BASE=1 to build the full yoloai-base under Apple's builder (slow)")
	}
	rt, ctx := appleSetup(t)

	// A real CacheDir so the staleness marker persists (production has one;
	// os.WriteFile won't mkdir).
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	require.NoError(t, os.MkdirAll(layout.CacheDir(), 0o755)) //nolint:gosec // G301: test dir under t.TempDir(), no sudo chown concern

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

// newPrincipalRuntime builds a Runtime scoped to its own unique test principal
// and data dir — the shape a separate embedder has (D59/D62), so a sweep run by
// one can be observed against instances owned by the other.
func newPrincipalRuntime(t *testing.T, ctx context.Context) (*Runtime, config.PrincipalSegment) {
	t.Helper()
	principal := testutil.UniqueTestPrincipal(t)
	home := testutil.IsolatedHome(t)
	rt, err := New(ctx, config.NewLayout(filepath.Join(home, ".yoloai")).WithPrincipal(principal))
	require.NoError(t, err)
	return rt, principal
}

// TestApple_PruneSparesForeignPrincipal plants an instance under a FOREIGN
// principal and asserts this principal's sweep spares it, against the live CLI.
//
// It has to build that situation on purpose. Nothing in a normal run — or in any
// other test here — creates a cross-principal container, so the hazard is
// invisible to a green suite; only a test that constructs it can fail (DF115).
// What it guards is a regression from label equality back to name-prefix
// matching, which would delete another embedder's running VMs.
//
// Both principals are UniqueTestPrincipals: the `container` daemon is global (an
// isolated HOME does not partition it), so a sweep run under the real CLI
// principal here would reap the developer's own yoloai-cli-* containers (DF19).
func TestApple_PruneSparesForeignPrincipal(t *testing.T) {
	rt, ctx := appleSetup(t)
	buildSleepImage(t, rt, ctx)

	ownRT, ownPrincipal := newPrincipalRuntime(t, ctx)
	foreignRT, foreignPrincipal := newPrincipalRuntime(t, ctx)
	require.NotEqual(t, ownPrincipal, foreignPrincipal)

	create := func(r *Runtime, principal config.PrincipalSegment, sandbox string) string {
		name := config.InstancePrefix(principal) + sandbox
		require.NoError(t, r.Create(ctx, runtime.InstanceConfig{
			Name:     name,
			ImageRef: itestImage,
			Labels: map[string]string{
				runtime.LabelSandbox:   sandbox,
				runtime.LabelPrincipal: string(principal),
			},
		}))
		t.Cleanup(func() { _ = r.Remove(context.Background(), name) })
		return name
	}

	foreignName := create(foreignRT, foreignPrincipal, "probe")
	ownName := create(ownRT, ownPrincipal, "orphan")

	// A container the USER ran by hand that merely happens to sit in this
	// principal's namespace: no com.yoloai.* labels, because yoloai never created
	// it. This is the case that actually discriminates the two predicates on live
	// hardware — see the note above the foreign-principal assertion below.
	handRunName := config.InstancePrefix(ownPrincipal) + "handrun"
	_, err := ownRT.runContainer(ctx, "create", "--name", handRunName, itestImage)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = ownRT.runContainer(context.Background(), "delete", "--force", handRunName) })

	// known is empty, so ownName is an orphan by construction; foreignName is not
	// ours to reap at all.
	var out bytes.Buffer
	res, err := ownRT.Prune(ctx, nil, false, &out)
	require.NoError(t, err)

	var pruned []string
	for _, it := range res.Items {
		pruned = append(pruned, it.Name)
	}
	assert.Contains(t, pruned, ownName, "this principal's unknown instance is an orphan and must be swept")

	// THE discriminating assertion. Prefix matching reaps this (it is in our
	// namespace by name); label equality spares it (yoloai never created it, so it
	// carries no com.yoloai.sandbox). Every other assertion here also holds under
	// the old prefix predicate — verified by mutation, not assumed — because
	// principals are alphanumeric (config.principalSegmentRe) and InstancePrefix
	// appends a delimiting "-", which makes distinct namespaces structurally
	// non-prefixing. So this is the assertion that fails on a regression.
	assert.NotContains(t, pruned, handRunName, "a container yoloai did not create must never be swept")
	_, err = ownRT.Inspect(ctx, handRunName)
	assert.NoError(t, err, "the hand-run container must still exist after the sweep")

	// Kept as a belt even though the prefix predicate satisfies it too: it pins
	// cross-principal sparing to the predicate rather than to the name-shape
	// accident that currently also delivers it.
	assert.NotContains(t, pruned, foreignName, "another principal's instance must never be reported as swept")

	// The report is a claim; the daemon is the fact. Assert the foreign instance
	// actually survived rather than trusting the sweep's own accounting.
	_, err = foreignRT.Inspect(ctx, foreignName)
	assert.NoError(t, err, "another principal's instance must still exist after the sweep")

	_, err = ownRT.Inspect(ctx, ownName)
	assert.ErrorIs(t, err, runtime.ErrNotFound, "this principal's orphan must actually be gone")
}
