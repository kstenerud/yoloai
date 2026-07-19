//go:build integration

// ABOUTME: Seatbelt backend integration tests: the shared conformance suite
// ABOUTME: against real macOS sandbox-exec processes. The process-free basics
// ABOUTME: live untagged in backend_basics_test.go.

package seatbelt

import (
	"context"
	"testing"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/runtimetest"
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
