//go:build integration

// ABOUTME: Seatbelt backend integration tests: the shared conformance suite
// ABOUTME: against real macOS sandbox-exec processes. The process-free basics
// ABOUTME: live untagged in backend_basics_test.go.

package seatbelt

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
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
// TestSeatbelt_HostTierIsUnwritableFromInside is the DF136 reproduction turned
// into a guard: a real sandbox-exec'd process must not be able to rewrite a
// record in the sandbox's host-only tier.
//
// It asserts enforcement by the kernel rather than the presence of a rule,
// because the two come apart in both directions here — the profile grants write
// over the whole sandbox dir *and* over the temp tree the sandbox dir sits in, so
// a deny that is present but mispositioned or misspelled reads correct and does
// nothing (DF161 is the same failure on the mount path). The sibling assertion is
// the load-bearing half: the deny must be scoped to the tier, so a write
// elsewhere in the sandbox dir has to keep working. A deny that broke everything
// would pass the first assertion alone.
func TestSeatbelt_HostTierIsUnwritableFromInside(t *testing.T) {
	rt, ctx := seatbeltSetup(t)

	name := "yoloai-test-host-tier-deny"
	_ = rt.Remove(ctx, name) // evict any stale leftover from a failed run
	require.NoError(t, rt.Create(ctx, runtime.InstanceConfig{Name: name}))
	t.Cleanup(func() { _ = rt.Remove(context.Background(), name) })
	require.NoError(t, rt.Start(ctx, name))

	sandboxPath := filepath.Join(rt.layout.SandboxesDir(), rt.sandboxName(name))
	hostTier := config.HostTierDir(sandboxPath)
	require.NoError(t, config.EnsureHostTier(sandboxPath))
	record := filepath.Join(hostTier, "environment.json")
	require.NoError(t, os.WriteFile(record, []byte(`{"HostPath":"/real"}`), 0o600))

	_, err := rt.Exec(ctx, name, []string{"sh", "-c", "echo tampered > " + record}, "")
	assert.Error(t, err, "a sandboxed process must not be able to rewrite a host-tier record (DF136)")

	onDisk, readErr := os.ReadFile(record) //nolint:gosec // G304: path built from the test's own sandbox dir
	require.NoError(t, readErr)
	assert.NotContains(t, string(onDisk), "tampered", "the record must be byte-identical after the denied write")

	// Scoping: the deny covers the tier, not the sandbox dir it sits in.
	canary := filepath.Join(sandboxPath, "canary.txt")
	_, err = rt.Exec(ctx, name, []string{"sh", "-c", "echo ok > " + canary}, "")
	assert.NoError(t, err, "the deny must not extend past the host tier — the rest of the sandbox dir stays writable")
}

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
