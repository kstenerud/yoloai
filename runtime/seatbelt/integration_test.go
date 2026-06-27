//go:build integration

// ABOUTME: Seatbelt backend integration tests. Exercise Create/Inspect/Remove on
// ABOUTME: real macOS sandbox-exec; skip cleanly on non-macOS platforms.

package seatbelt

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/runtimetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSeatbeltConformance runs the shared backend-agnostic conformance suite
// against the real macOS seatbelt backend. Seatbelt has no image/VM — each
// instance is a sandbox-exec'd process under an SBPL profile. The suite works
// because Start now does P1 only (a bare keep-alive under the profile) when no
// sandbox runtime-config.json is present, skipping the sandbox-setup.py monitor
// — the same P1/P2 split as tart. Stdio auto-skips (no StdioExecer). Mounts, if
// they run, exercise real SBPL enforcement: the profile grants RW/RO on the host
// mount path, so a write to a read-only mount is denied by the kernel.
func TestSeatbeltConformance(t *testing.T) {
	rt, ctx := seatbeltSetup(t)
	runtimetest.RunInterfaceConformance(t, func(t *testing.T) runtimetest.InterfaceBackend {
		return runtimetest.InterfaceBackend{
			Runtime: rt,
			Ctx:     ctx,
			// The conformance mounts at /mnt/test, but seatbelt runs on the host
			// where /mnt isn't writable without root — so the container→host
			// symlink can't be created and /mnt/test doesn't exist. This is the
			// conformance's container-path assumption, not a seatbelt mount-
			// capability gap: the SBPL RW/RO grant generation is unit-tested
			// (TestGenerateProfile_{ReadOnly,ReadWrite}Mount), and real mounts at
			// writable paths run in the smoke matrix.
			SkipMounts: "conformance mounts at /mnt/test; seatbelt is host-side and /mnt isn't writable without root (grants are unit-tested via GenerateProfile_*Mount)",
			NewSleeper: func(t *testing.T, cfg runtime.InstanceConfig) string {
				_ = rt.Remove(ctx, cfg.Name) // evict any stale leftover from a failed run
				require.NoError(t, rt.Create(ctx, cfg))
				t.Cleanup(func() { _ = rt.Remove(context.Background(), cfg.Name) })
				return cfg.Name
			},
		}
	})
}

// minimalInstanceConfig returns a runtime.InstanceConfig that is just enough
// to call Create without crashing. It does NOT make the sandbox actually
// runnable — a real Start would need an agent installed, an API key, etc.
// This is a scaffold for verifying the create/inspect/remove plumbing.
func minimalInstanceConfig(t *testing.T, name string) runtime.InstanceConfig {
	t.Helper()
	workdir := t.TempDir()
	return runtime.InstanceConfig{
		Name:       name,
		WorkingDir: workdir,
		Mounts: []runtime.MountSpec{
			{HostPath: workdir, ContainerPath: workdir, ReadOnly: false},
		},
	}
}

func TestSeatbelt_New_ReturnsRuntime(t *testing.T) {
	rt, _ := seatbeltSetup(t)
	require.NotNil(t, rt)
	assert.NotEmpty(t, rt.sandboxExecBin, "should have located sandbox-exec")
	assert.NotEmpty(t, rt.layout.SandboxesDir(), "should have set sandbox base dir")
}

func TestSeatbelt_Descriptor_AdvertisesHostFilesystem(t *testing.T) {
	rt, _ := seatbeltSetup(t)
	desc := rt.Descriptor()
	assert.Equal(t, runtime.BackendSeatbelt, desc.Type)
	assert.True(t, desc.Capabilities.HostFilesystem,
		"seatbelt should declare HostFilesystem (process runs against host paths)")
	assert.False(t, desc.Capabilities.ContainerAttach,
		"seatbelt has no container surface; VS Code Attach should be false")
}

func TestSeatbelt_CreateInspectRemove(t *testing.T) {
	rt, ctx := seatbeltSetup(t)

	cfg := minimalInstanceConfig(t, "scaffold-create")
	require.NoError(t, rt.Create(ctx, cfg))

	// The sandbox directory layout should now exist.
	sandboxPath := filepath.Join(rt.layout.SandboxesDir(), sandboxName(cfg.Name))
	require.DirExists(t, sandboxPath, "sandbox directory should be created")
	require.DirExists(t, filepath.Join(sandboxPath, backendDir),
		"backend dir should be created")
	require.FileExists(t, filepath.Join(sandboxPath, backendDir, profileFileName),
		"SBPL profile should be written")
	require.FileExists(t, filepath.Join(sandboxPath, backendDir, seatbeltConfigFileName),
		"instance config should be persisted")

	// Inspect before Start — process is not running, but Inspect must succeed.
	info, err := rt.Inspect(ctx, cfg.Name)
	require.NoError(t, err)
	assert.False(t, info.Running, "sandbox should not be running before Start")

	// Remove tears down the sandbox cleanly.
	require.NoError(t, rt.Remove(ctx, cfg.Name))
	_, statErr := os.Stat(sandboxPath)
	assert.True(t, os.IsNotExist(statErr),
		"sandbox directory should be gone after Remove")
}

func TestSeatbelt_InspectNotFound(t *testing.T) {
	rt, ctx := seatbeltSetup(t)
	_, err := rt.Inspect(ctx, "does-not-exist")
	require.Error(t, err)
}

func TestSeatbelt_RemoveIdempotent(t *testing.T) {
	rt, ctx := seatbeltSetup(t)

	cfg := minimalInstanceConfig(t, "scaffold-remove-twice")
	require.NoError(t, rt.Create(ctx, cfg))
	require.NoError(t, rt.Remove(ctx, cfg.Name))
	// A second Remove on a missing sandbox should not error.
	assert.NoError(t, rt.Remove(ctx, cfg.Name),
		"Remove on already-removed sandbox should be idempotent")
}

func TestSeatbelt_StopNotRunningIsNoOp(t *testing.T) {
	rt, ctx := seatbeltSetup(t)

	cfg := minimalInstanceConfig(t, "scaffold-stop-noop")
	require.NoError(t, rt.Create(ctx, cfg))
	t.Cleanup(func() { _ = rt.Remove(ctx, cfg.Name) })

	// Stop on a never-started sandbox should not error — there is nothing
	// to kill but the contract is "best-effort idempotent."
	assert.NoError(t, rt.Stop(ctx, cfg.Name))
}
