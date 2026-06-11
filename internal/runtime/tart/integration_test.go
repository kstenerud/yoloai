//go:build integration

// ABOUTME: Tart backend integration tests. Cheap tests run on every Apple
// ABOUTME: Silicon machine with tart installed; full VM-lifecycle tests are
// ABOUTME: opt-in via YOLOAI_TEST_TART_VM=1 because they clone a multi-GB base.

package tart

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/runtime/runtimetest"
	"github.com/kstenerud/yoloai/internal/testutil"
)

func TestTart_New_ReturnsRuntime(t *testing.T) {
	rt, _ := tartSetup(t)
	require.NotNil(t, rt)
	assert.NotEmpty(t, rt.tartBin, "should have located tart CLI")
	assert.NotEmpty(t, rt.layout.SandboxesDir(), "should have set sandbox base dir")
}

func TestTart_Descriptor_AdvertisesVMCapabilities(t *testing.T) {
	rt, _ := tartSetup(t)
	desc := rt.Descriptor()
	assert.Equal(t, "tart", string(desc.Type))
	assert.False(t, desc.Capabilities.HostFilesystem,
		"tart runs the agent inside a VM, not on the host filesystem")
	assert.False(t, desc.Capabilities.ContainerAttach,
		"tart has no docker-compatible container surface; VS Code Attach should be false")
	assert.False(t, desc.Capabilities.OverlayDirs,
		"tart does not support :overlay (no overlayfs in macOS VMs)")
}

func TestTart_InspectNotFound(t *testing.T) {
	rt, ctx := tartSetup(t)
	info, err := rt.Inspect(ctx, "does-not-exist-"+t.Name())
	// Contract: Inspect on a nonexistent VM should either return an error
	// or an InstanceInfo with Running=false. Either is acceptable; the
	// caller checks Running, not whether the inspect succeeded.
	if err == nil {
		assert.False(t, info.Running,
			"a nonexistent VM cannot be running")
	}
}

func TestTart_RemoveIdempotent_NonexistentVM(t *testing.T) {
	rt, ctx := tartSetup(t)
	// Remove on a never-created VM should not error. Tart's "delete" is
	// idempotent enough that the runtime should not surface a failure
	// when the target wasn't there to begin with.
	err := rt.Remove(ctx, "never-existed-"+t.Name())
	assert.NoError(t, err, "Remove on nonexistent VM should be idempotent")
}

// TestTartConformance runs the shared backend-agnostic conformance suite against
// a real Tart VM, so Tart verifies the same lifecycle / exec / mount contract as
// docker, podman, containerd, and apple. The sleeper is a booted yoloai-base
// clone — at the runtime level a started VM stays alive on its own, so no idle
// command is needed (the idle agent's keep-alive is a sandbox-level concern).
//
// Gated behind YOLOAI_TEST_TART_VM=1: every subtest clones a multi-GB base VM
// and boots a full macOS guest, so the suite is slow and opt-in. VMs are named
// "yoloai-test-*" (never production yoloai-base/yoloai-<sandbox>) and removed via
// t.Cleanup. The stdio section auto-skips (tart implements no runtime.StdioExecer).
//
// On an Apple Silicon host with the base image present, run with:
//
//	YOLOAI_TEST_TART_VM=1 go test -tags=integration -timeout=40m \
//	    -run TestTartConformance ./internal/runtime/tart/
func TestTartConformance(t *testing.T) {
	if os.Getenv("YOLOAI_TEST_TART_VM") != "1" {
		t.Skip("set YOLOAI_TEST_TART_VM=1 to run the Tart conformance suite (clones a multi-GB base VM per subtest)")
	}
	// Isolate yoloai's .yoloai state (config/sandboxes) in a temp dir, but resolve
	// the layout's HomeDir to the REAL home. tart reads its store via
	// NSHomeDirectory/TART_HOME (not $HOME), and TART_HOME defaults to
	// <HomeDir>/.tart — so a real HomeDir points tart at the shared ~/.tart where
	// yoloai-base actually lives, reusing that expensive image instead of pointing
	// at an empty isolated store (which would make IsReady false → whole suite
	// skips). Curated real env so the tart subprocess has PATH etc.
	ctx := context.Background()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	layout := config.NewLayoutFor(filepath.Join(t.TempDir(), ".yoloai"), home).
		WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars))
	rt, err := New(ctx, layout)
	require.NoError(t, err, "tart backend must be available on this platform")
	ready, err := rt.IsReady(ctx)
	require.NoError(t, err)
	if !ready {
		t.Skip("yoloai-base VM not present in ~/.tart; run 'yoloai system build --backend tart' first")
	}

	runtimetest.RunInterfaceConformance(t, func(t *testing.T) runtimetest.InterfaceBackend {
		return runtimetest.InterfaceBackend{
			Runtime: rt,
			Ctx:     ctx,
			// Conformance mounts at /mnt/test — a container-centric path. The
			// macOS guest's /mnt is root-owned and the guest has no passwordless
			// sudo, so the symlink command can't create it (the same
			// /mnt-not-writable reason seatbelt skips). The failing `ln` emits
			// "No such file or directory", which runTart's mapTartError
			// misclassifies as runtime.ErrNotFound → "instance not found" (DF30).
			// Real mount wiring is exercised by the sandbox-level lifecycle tests.
			SkipMounts: "conformance /mnt/test is not host-writable in the macOS guest (no passwordless sudo); same container-path assumption seatbelt skips",
			NewSleeper: func(t *testing.T, cfg runtime.InstanceConfig) string {
				if cfg.ImageRef == "" {
					cfg.ImageRef = "yoloai-base"
				}
				_ = rt.Remove(ctx, cfg.Name) // evict any stale leftover from a failed run
				require.NoError(t, rt.Create(ctx, cfg))
				t.Cleanup(func() { _ = rt.Remove(context.Background(), cfg.Name) })
				return cfg.Name
			},
		}
	})
}
