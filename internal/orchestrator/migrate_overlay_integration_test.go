//go:build integration

// ABOUTME: Docker integration test for the v3->v4 overlay flatten — validates the
// ABOUTME: in-container merged-tree capture + promotion to :copy on a real overlay sandbox.

package orchestrator_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/migrate"
	"github.com/kstenerud/yoloai/internal/orchestrator"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_OverlayFlatten drives the v3->v4 migrator against a real running
// :overlay sandbox: it seeds an uncommitted change into the merged overlay view,
// flattens, and asserts the sandbox is now :copy with the change preserved
// (proving the raw in-container merged-tree capture is correct).
func TestIntegration_OverlayFlatten(t *testing.T) {
	mgr, ctx := integrationSetup(t)

	// No-git project (see TestIntegration_Overlay: entrypoint chown makes the
	// overlay opaque to .git; the flatten needs no git anyway — it is a raw copy).
	projectDir := testutil.GoProjectNoGit(t)

	_, err := createSandbox(ctx, mgr, orchestrator.CreateOptions{
		Name:    "flatten-integ",
		Workdir: orchestrator.DirSpec{Path: projectDir, Mode: orchestrator.DirModeOverlay},
		Agent:   "test",
		Version: "test",
	})
	require.NoError(t, err)
	enc := store.EncodePath(projectDir)
	t.Cleanup(func() {
		// ovlwork/ holds root-owned kernel files; remove via exec before destroy.
		mgr.Runtime().Exec(ctx, store.InstanceName("", "flatten-integ"), //nolint:errcheck // best-effort
			[]string{"rm", "-rf", "/yoloai/overlay/" + enc + "/ovlwork"}, "root")
		destroySandbox(ctx, mgr, "flatten-integ") //nolint:errcheck // test cleanup
	})

	if _, startErr := startSandbox(ctx, mgr, "flatten-integ", orchestrator.StartOptions{}); startErr != nil {
		if strings.Contains(startErr.Error(), "overlay") || strings.Contains(startErr.Error(), "mount") ||
			strings.Contains(startErr.Error(), "CAP_SYS_ADMIN") || strings.Contains(startErr.Error(), "permission") {
			t.Skip("overlay not supported: " + startErr.Error())
		}
		require.NoError(t, startErr)
	}
	instance := store.InstanceName("", "flatten-integ")
	testutil.WaitForActive(ctx, t, mgr.Runtime(), instance, 15*time.Second)

	// Seed an uncommitted change directly into the merged overlay view (the agent's
	// workdir), including a would-be-gitignored file, to prove the raw capture keeps
	// exactly what the agent sees — not a diff-against-baseline.
	sandboxDir := mgr.Layout().SandboxDir("flatten-integ")
	meta, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	merged := meta.Workdir().MountPath // /yoloai/overlay/<enc>/merged
	require.Equal(t, "/yoloai/overlay/"+enc+"/merged", merged)

	// Poll: the overlay mount is done by the entrypoint, so exec into merged may
	// not be ready the instant WaitForActive returns (see TestIntegration_Overlay).
	seed := fmt.Sprintf("printf overlay-change > %s/flatten-marker.txt; printf ignored > %s/build.log", merged, merged)
	seeded := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		res, execErr := mgr.Runtime().Exec(ctx, instance, []string{"sh", "-c", seed}, "yoloai")
		if execErr == nil && res.ExitCode == 0 {
			seeded = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.True(t, seeded, "could not seed the overlay merged view within 15s")

	// Flatten via the real migrator against the running sandbox.
	m := orchestrator.NewOverlayFlatten(
		mgr.Layout(), mgr.Layout().DataDir, mgr.Layout().SandboxesDir(), "linux",
		func(context.Context) (runtime.Backend, error) { return mgr.Runtime(), nil },
	)
	report, err := m.Apply(ctx, migrate.Decision{})
	require.NoError(t, err, "flatten Apply")
	assert.Contains(t, report.Migrated, "flatten-integ")

	// The sandbox is now :copy.
	flat, err := store.LoadEnvironment(sandboxDir)
	require.NoError(t, err)
	assert.Equal(t, store.DirModeCopy, flat.Workdir().Mode, "workdir must be copy mode after flatten")
	// MountPath must be reset off the overlay merged path to the mirrored host
	// path, so a copy-mode restart mounts the work copy where the agent expects.
	assert.Equal(t, projectDir, flat.Workdir().MountPath, "MountPath must mirror the host path after flatten")

	// The merged view landed as the copy work dir: the seeded change AND the
	// would-be-ignored file survive (raw capture, not a baseline diff), directly
	// under work/<enc> (copy layout — no overlay subdirs).
	workDir := store.WorkDir(sandboxDir, projectDir)
	assertFile(t, filepath.Join(workDir, "flatten-marker.txt"), "overlay-change")
	assertFile(t, filepath.Join(workDir, "build.log"), "ignored")
	assert.NoDirExists(t, filepath.Join(workDir, "merged"), "overlay subdirs must be gone")
	assert.NoDirExists(t, filepath.Join(workDir, "upper"), "overlay subdirs must be gone")

	// The realm is stamped current (v4), flipped last.
	v, _, err := config.ReadSchemaVersion(mgr.Layout().SchemaVersionPath())
	require.NoError(t, err)
	assert.Equal(t, config.LibrarySchemaVersion, v)
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path) //nolint:gosec // test path
	require.NoError(t, err, "read %s", path)
	assert.Equal(t, want, string(got), "content of %s", filepath.Base(path))
}
