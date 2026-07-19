//go:build integration

// ABOUTME: Tart backend integration tests: the full VM-lifecycle tests, opt-in
// ABOUTME: via YOLOAI_TEST_TART_VM=1 because each clones a multi-GB base VM.
// ABOUTME: The VM-free basics live untagged in backend_basics_test.go.

package tart

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/runtimetest"
)

// TestTart_Start_SetsGuestHostname verifies the DF142 tart half end to end: a
// Create+Start with InstanceConfig.Hostname set lands that name as the guest's
// OS hostname (runSetupScript -> setVMHostname -> sudo scutil). Without the fix
// the guest keeps the base image's generic "Manageds-Virtual-Machine".
//
// Gated behind YOLOAI_TEST_TART_VM=1 like the conformance suite: it clones and
// boots a multi-GB base VM. The VM is named "yoloai-test-*" and removed via
// t.Cleanup.
func TestTart_Start_SetsGuestHostname(t *testing.T) {
	if os.Getenv("YOLOAI_TEST_TART_VM") != "1" {
		t.Skip("set YOLOAI_TEST_TART_VM=1 to run the Tart hostname test (clones + boots a multi-GB base VM)")
	}
	ctx := context.Background()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	layout := config.NewLayoutFor(filepath.Join(t.TempDir(), ".yoloai"), home).
		WithPrincipal(config.CLIPrincipal).
		WithEnv(testutil.GetCuratedHostEnv(testutil.IntegrationHostEnvVars))
	rt, err := New(ctx, layout)
	require.NoError(t, err, "tart backend must be available on this platform")
	ready, err := rt.IsReady(ctx)
	require.NoError(t, err)
	if !ready {
		t.Skip("yoloai-base VM not present in ~/.tart; run 'yoloai system build --backend tart' first")
	}

	name := "yoloai-test-hostname"
	wantHostname := "my-sandbox-hostname"
	_ = rt.Remove(ctx, name) // evict any stale leftover from a failed run
	t.Cleanup(func() { _ = rt.Remove(context.Background(), name) })

	require.NoError(t, rt.Create(ctx, runtime.InstanceConfig{
		Name:     name,
		Hostname: wantHostname,
		ImageRef: "yoloai-base",
	}))
	require.NoError(t, rt.Start(ctx, name))

	res, err := rt.Exec(ctx, name, []string{"hostname"}, "")
	require.NoError(t, err)
	assert.Equal(t, wantHostname, res.Stdout,
		"the guest hostname should be the sandbox name set via scutil, not the base image default")
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
//	    -run TestTartConformance ./runtime/tart/
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
		WithPrincipal(config.CLIPrincipal).
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
			// A tart boot is a multi-GB clone (~90-118s); amortize the read-only
			// subtests onto one shared instance (speedup plan, lever 1).
			SharesReadOnlyInstance: true,
			// Conformance mounts at /mnt/test — a container-centric path. On the
			// macOS guest the root volume is read-only (the SIP-sealed system
			// volume), so /mnt does not exist and cannot be created even as root:
			// `sudo mkdir -p /mnt/test` fails with "Read-only file system". (Note
			// the guest DOES have passwordless sudo — `sudo -n true` succeeds as
			// the admin user; the setup symlinks rely on it — so sudo is not the
			// obstacle here, the read-only rootfs is.) The failing `ln` then emits
			// "No such file or directory" (since DF30 surfaced verbatim; it used
			// to be mislabeled "instance not found" by mapTartError). Real mount
			// wiring is exercised by the sandbox-level lifecycle tests.
			SkipMounts: "conformance /mnt/test can't be created in the macOS guest: the root volume is read-only (SIP-sealed), so /mnt is absent and uncreatable even as root; same container-path assumption seatbelt skips",
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
