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

	// Scoping: the deny covers the tier, not the sandbox dir it sits in. The
	// canary is in the read-write tier, which is where everything the guest
	// writes now lives — the sandbox root itself is granted nothing.
	canary := filepath.Join(config.ReadWriteTierDir(sandboxPath), "canary.txt")
	_, err = rt.Exec(ctx, name, []string{"sh", "-c", "echo ok > " + canary}, "")
	assert.NoError(t, err, "the deny must not extend past the host tier — the read-write tier stays writable")
}

// TestSeatbelt_ReadOnlyTierIsUnwritableFromInside is DF148's guard, and it has
// to be a kernel test for the reason DF161 established: on seatbelt a read-only
// region is the *absence* of a write grant, so it holds only while nothing
// broader grants write — and in these tests the sandbox dir sits inside the
// per-user temp tree, which the profile grants read+write wholesale. A profile
// that merely omits the write grant reads correct and enforces nothing.
//
// The write through the *view* is the load-bearing case. The guest reaches
// runtime-config.json at <view>/runtime-config.json, a symlink into the
// read-only tier, and macOS checks access after symlink resolution — so the
// question this answers is whether the tier's deny applies to the path the guest
// actually uses, rather than only to the one the host does.
func TestSeatbelt_ReadOnlyTierIsUnwritableFromInside(t *testing.T) {
	rt, ctx := seatbeltSetup(t)

	name := "yoloai-test-ro-tier-deny"
	_ = rt.Remove(ctx, name) // evict any stale leftover from a failed run
	require.NoError(t, rt.Create(ctx, runtime.InstanceConfig{Name: name}))
	t.Cleanup(func() { _ = rt.Remove(context.Background(), name) })
	require.NoError(t, rt.Start(ctx, name))

	sandboxPath := filepath.Join(rt.layout.SandboxesDir(), rt.sandboxName(name))
	prompt := config.PromptPath(sandboxPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(prompt), 0o750))
	require.NoError(t, os.WriteFile(prompt, []byte("original"), 0o600))
	// Surface it in the view the way a launch does.
	require.NoError(t, config.AssembleGuestView(sandboxPath))
	viaView := filepath.Join(config.GuestViewDir(sandboxPath), config.PromptFileName)

	// Readable — a deny that also blocked reads would pass every write
	// assertion below while making the tier useless.
	out, err := rt.Exec(ctx, name, []string{"sh", "-c", "cat " + viaView}, "")
	require.NoError(t, err, "the guest must be able to read the read-only tier through the view")
	assert.Contains(t, out.Stdout, "original")

	for _, path := range []string{prompt, viaView} {
		_, err := rt.Exec(ctx, name, []string{"sh", "-c", "echo tampered > " + path}, "")
		assert.Error(t, err, "a sandboxed process must not be able to write the read-only tier via %s (DF148)", path)
	}

	onDisk, readErr := os.ReadFile(prompt) //nolint:gosec // G304: path built from the test's own sandbox dir
	require.NoError(t, readErr)
	assert.Equal(t, "original", string(onDisk), "the read-only tier's contents must be unchanged")

	// Scoping, same as the host tier: the neighbouring tier stays writable.
	canary := filepath.Join(config.ReadWriteTierDir(sandboxPath), "canary.txt")
	_, err = rt.Exec(ctx, name, []string{"sh", "-c", "echo ok > " + canary}, "")
	assert.NoError(t, err, "the deny must not extend past the read-only tier")
}

// OpenCode fails with an "Unexpected server error. Check server logs for details." error
// if it is unable to write plugin-related files, beginning with a simple `.gitignore`,
// next to its seeded config".
func TestSeatbelt_HomeSeedIsWritableFromInside(t *testing.T) {
	rt, ctx := seatbeltSetup(t)

	name := "yoloai-test-home-seed-rw"
	_ = rt.Remove(ctx, name) // evict any stale leftover from a failed run
	require.NoError(t, rt.Create(ctx, runtime.InstanceConfig{Name: name}))
	t.Cleanup(func() { _ = rt.Remove(context.Background(), name) })
	require.NoError(t, rt.Start(ctx, name))

	sandboxPath := filepath.Join(rt.layout.SandboxesDir(), rt.sandboxName(name))
	seededConfig := filepath.Join(config.HomeSeedPath(sandboxPath), ".config", "opencode", "opencode.jsonc")
	require.NoError(t, os.MkdirAll(filepath.Dir(seededConfig), 0o750))
	require.NoError(t, os.WriteFile(seededConfig, []byte(`{"$schema": "seeded"}`), 0o600))

	gitIgnore := filepath.Join(config.GuestViewDir(sandboxPath), config.HomeSeedDirName, ".config", "opencode", ".gitignore")
	_, err := rt.Exec(ctx, name, []string{"sh", "-c", "echo 'node_modules' > " + gitIgnore}, "")
	assert.NoError(t, err, "the guest must be able to create files next to its seeded config")

	onDisk, readErr := os.ReadFile(gitIgnore) //nolint:gosec // G304: path built from the test's own sandbox dir
	require.NoError(t, readErr)
	assert.Equal(t, "node_modules\n", string(onDisk), "the .gitignore write must reach home-seed on disk")
}

// TestSeatbelt_ReadOnlyMountHoldsUnderABroaderGrant drives the user-facing
// `--dir <path>:ro` path (buildSingleAuxDirMount → MountSpec{ReadOnly:true} →
// GenerateProfile) with a host path that sits inside one of the profile's own
// broad write grants — the per-user temp tree, which tempPaths() grants
// read+write wholesale.
//
// Seatbelt expresses a read-only mount as the *absence* of a write grant, and an
// allow-read never revokes a write allowed by a broader rule, so this is the case
// where ":ro" silently is not. Every other backend enforces read-only with a real
// read-only mount and is unconditional (DF161).
func TestSeatbelt_ReadOnlyMountHoldsUnderABroaderGrant(t *testing.T) {
	rt, ctx := seatbeltSetup(t)

	// t.TempDir() is under /private/var/folders — inside tempPaths()' grant.
	hostDir := t.TempDir()
	victim := filepath.Join(hostDir, "readonly.txt")
	require.NoError(t, os.WriteFile(victim, []byte("original"), 0o600))

	name := "yoloai-test-ro-mount-grant"
	_ = rt.Remove(ctx, name) // evict any stale leftover from a failed run
	require.NoError(t, rt.Create(ctx, runtime.InstanceConfig{
		Name:   name,
		Mounts: []runtime.MountSpec{{HostPath: hostDir, ContainerPath: hostDir, ReadOnly: true}},
	}))
	t.Cleanup(func() { _ = rt.Remove(context.Background(), name) })
	require.NoError(t, rt.Start(ctx, name))

	_, err := rt.Exec(ctx, name, []string{"sh", "-c", "echo tampered > " + victim}, "")
	assert.Error(t, err, "a write to a :ro mount must fail even when a broader rule grants write over the same path")

	after, readErr := os.ReadFile(victim) //nolint:gosec // G304: the test's own fixture path
	require.NoError(t, readErr)
	assert.Equal(t, "original", string(after), "the read-only mount's contents must be unchanged")
}

// TestSeatbelt_NestedMountsEnforceMostSpecific checks the ordering rule against
// the kernel rather than against the profile text. The unit test pins where the
// rules land in the file; this pins that SBPL resolves overlapping subpath rules
// the way that ordering assumes — most specific wins — in both nesting
// directions. They are separate failures: the text could be ordered correctly and
// the resolution still not be positional.
func TestSeatbelt_NestedMountsEnforceMostSpecific(t *testing.T) {
	rt, ctx := seatbeltSetup(t)

	root := t.TempDir()
	roOuter := filepath.Join(root, "outer")
	rwInner := filepath.Join(roOuter, "inner")
	require.NoError(t, os.MkdirAll(rwInner, 0o750))

	name := "yoloai-test-nested-mounts"
	_ = rt.Remove(ctx, name) // evict any stale leftover from a failed run
	require.NoError(t, rt.Create(ctx, runtime.InstanceConfig{
		Name: name,
		Mounts: []runtime.MountSpec{
			{HostPath: roOuter, ContainerPath: roOuter, ReadOnly: true},
			{HostPath: rwInner, ContainerPath: rwInner},
		},
	}))
	t.Cleanup(func() { _ = rt.Remove(context.Background(), name) })
	require.NoError(t, rt.Start(ctx, name))

	_, err := rt.Exec(ctx, name, []string{"sh", "-c", "echo blocked > " + filepath.Join(roOuter, "x.txt")}, "")
	assert.Error(t, err, "the read-only outer dir must stay read-only")

	_, err = rt.Exec(ctx, name, []string{"sh", "-c", "echo allowed > " + filepath.Join(rwInner, "x.txt")}, "")
	assert.NoError(t, err, "the read-write dir nested inside it must stay writable")
}

func TestSeatbeltConformance(t *testing.T) {
	rt, ctx := seatbeltSetup(t)
	runtimetest.RunInterfaceConformance(t, func(t *testing.T) runtimetest.InterfaceBackend {
		return runtimetest.InterfaceBackend{
			Runtime: rt,
			Ctx:     ctx,
			// Mounts runs. It was skipped until 2026-07-30 on the conformance's
			// /mnt/test assumption — seatbelt is host-side and /mnt is not writable
			// without root, so the container→host symlink was never created. The
			// suite now mounts under /tmp, which the host can create (DF161).
			//
			// The tier section runs: seatbelt is one of the two backends that
			// exposes the sandbox directory itself, here as SBPL grants rather
			// than a mount, so the tiers are reachable and the invariant is real.
			// The guest is a host process, so its view path is the host's.
			SandboxTiers: func(name string) (string, string) {
				sandboxDir := filepath.Join(rt.layout.SandboxesDir(), rt.sandboxName(name))
				return sandboxDir, config.GuestViewDir(sandboxDir)
			},
			NewSleeper: func(t *testing.T, cfg runtime.InstanceConfig) string {
				_ = rt.Remove(ctx, cfg.Name) // evict any stale leftover from a failed run
				require.NoError(t, rt.Create(ctx, cfg))
				t.Cleanup(func() { _ = rt.Remove(context.Background(), cfg.Name) })
				return cfg.Name
			},
		}
	})
}
