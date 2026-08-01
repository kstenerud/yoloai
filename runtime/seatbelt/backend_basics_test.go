// ABOUTME: Process-free seatbelt backend basics (constructor, descriptor,
// ABOUTME: create/inspect/remove scaffolding) against the real sandbox-exec
// ABOUTME: CLI. Untagged so every macOS `make check` runs them; they skip off
// ABOUTME: macOS and never spawn a sandboxed process.

package seatbelt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/runtime"
)

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
	sandboxPath := filepath.Join(rt.layout.SandboxesDir(), rt.sandboxName(cfg.Name))
	require.DirExists(t, sandboxPath, "sandbox directory should be created")
	require.DirExists(t, config.BackendPath(sandboxPath),
		"backend dir should be created")
	require.FileExists(t, filepath.Join(config.BackendPath(sandboxPath), profileFileName),
		"SBPL profile should be written")
	require.FileExists(t, filepath.Join(config.BackendPath(sandboxPath), seatbeltConfigFileName),
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

// TestResolveCopyMount_ResolvesIntoTheReadWriteTier pins where seatbelt's
// copy-mode work copy lives. Seatbelt is host-side: this path is what the agent
// edits and what `yoloai diff` reads, so it is the same directory seen from both
// ends, and it is read-write tier state.
//
// It needs pinning because the failure is quiet in the direction that matters —
// a work copy that is not where diff looks produces an empty diff, not an error,
// so an agent's work would look like no work at all. This was built from a
// "work" literal and went stale the moment the tiers moved; nothing tested it.
func TestResolveCopyMount_ResolvesIntoTheReadWriteTier(t *testing.T) {
	r := &Runtime{layout: config.Layout{DataDir: "/data"}}

	got := r.ResolveCopyMount("mybox", "/Users/karl/project")

	sandboxDir := filepath.Join(r.layout.SandboxesDir(), "mybox")
	assert.Equal(t, filepath.Join(config.WorkBasePath(sandboxDir), config.EncodePath("/Users/karl/project")), got)
	// Spelled literally too: the assertion above would follow a wrong builder.
	assert.Contains(t, got, filepath.Join("mybox", "rw", "work"),
		"the work copy is read-write tier state")
}

// TestCheckTmuxSocketPathFits_RefusesBeforeTheSocketIsBound pins the boundary
// and, more importantly, which side of it errors.
//
// The limit belongs to the whole path, so the same sandbox name is fine under a
// short data dir and impossible under a long one — which is why it cannot be a
// name-validation rule and has to be checked where the resolved path is known.
// Before this check the sandbox was created, reported success, and then failed
// deeper in `start` with tmux's "File name too long" against a path nobody chose
// the length of; the release gate's own generated names hit it (DF169).
func TestCheckTmuxSocketPathFits_RefusesBeforeTheSocketIsBound(t *testing.T) {
	// Work backwards from the cap so the case cannot drift with the layout: the
	// longest sandbox dir whose socket path still fits, and one byte more. The
	// suffix is measured, not spelled, so it stays right if the tier moves.
	suffix := len(config.TmuxSocketPath("x")) - len("x")
	fits := strings.Repeat("a", maxUnixSocketPath-1-suffix)
	require.NoError(t, checkTmuxSocketPathFits(fits),
		"a socket path of exactly the maximum length must be accepted")
	require.Len(t, config.TmuxSocketPath(fits), maxUnixSocketPath-1)

	err := checkTmuxSocketPathFits(fits + "a")
	require.Error(t, err, "one byte over the cap must be refused")
	// The message has to name the limit and the remedy: the kernel's does not,
	// and it surfaces from inside tmux where the sandbox name is not in scope.
	assert.Contains(t, err.Error(), "too long for this data directory")
	assert.Contains(t, err.Error(), "Shorten the sandbox name")
	assert.Contains(t, err.Error(), "--data-dir")
}

// TestTmuxSocketPath_SitsAtTheTierRoot pins the depth, which is the part that is
// load-bearing rather than cosmetic: every path component between the sandbox
// dir and the socket is spent from the same 104-byte budget, so moving the
// socket back under tmux/ would silently re-narrow the usable name length.
func TestTmuxSocketPath_SitsAtTheTierRoot(t *testing.T) {
	got := config.TmuxSocketPath("/data/sandboxes/mybox")

	assert.Equal(t, "/data/sandboxes/mybox/rw/tmux.sock", got)
	assert.NotContains(t, got, "/tmux/", "the socket must not sit inside tmux/ — that costs five bytes of sun_path")
}
