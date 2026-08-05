package orchestrator

// ABOUTME: Tests for the v5->v6 tier migrator — the entry classification, the
// ABOUTME: preconditions it refuses on, and that a failure leaves the live tree intact.

import (
	"context"
	"errors"
	"github.com/kstenerud/yoloai/runtime"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/migrate"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/store"
)

// A representative flat v5 sandbox: one entry from each tier, plus a directory,
// so a transform that handled only files or only the records would be visible.
// Spelled with literals — this is the pre-tier layout, and building the fixture
// from the live builders would move it along with them (DF164).
func seedFlatSandbox(t *testing.T, layout config.Layout, name string) string {
	t.Helper()
	dir := layout.SandboxDir(name)
	seedSandboxRecord(t, dir, &store.Environment{
		Version:     3,
		Name:        name,
		Principal:   config.CLIPrincipal,
		BackendType: "mock",
		Dirs:        []store.DirEnvironment{{HostPath: "/proj", MountPath: "/proj", Mode: store.DirModeCopy}},
	})
	testutil.WriteSandboxRecord(t, filepath.Join(dir, "sandbox-state.json"), []byte(`{"version":1}`))
	testutil.WriteSandboxRecord(t, filepath.Join(dir, "agent.json"), []byte(`{"version":1,"agent":"test"}`))
	testutil.WriteSandboxRecord(t, filepath.Join(dir, "netpolicy.json"), []byte(`{"version":1}`))
	testutil.WriteSandboxRecord(t, filepath.Join(dir, "runtime-config.json"), []byte(`{}`))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", "entrypoint.sh"), []byte("#!/bin/sh\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "work", "^proj"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "work", "^proj", "README.md"), []byte("hi"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "logs"), 0o750))
	return dir
}

// seedRealm stamps the library realm at v5 — the schema this migrator's input is
// written in.
func seedRealm(t *testing.T, layout config.Layout) {
	t.Helper()
	require.NoError(t, os.MkdirAll(layout.SandboxesDir(), 0o750))
	testutil.WriteSandboxRecord(t, layout.SchemaVersionPath(), []byte("5"))
}

func newTierLayoutFor(layout config.Layout) *TierLayout {
	return NewTierLayout(layout, layout.DataDir, layout.SandboxesDir(),
		func(context.Context, runtime.BackendType) (runtime.Backend, error) {
			// No backend of type "mock" exists here, which is the same answer a
			// Linux host gives for a tart sandbox: nothing of that kind is running.
			return nil, errors.New("no such backend on this host")
		})
}

func TestTierLayout_Apply_TiersAFlatSandbox(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedRealm(t, layout)
	dir := seedFlatSandbox(t, layout, "box")

	rep, err := newTierLayoutFor(layout).Apply(context.Background(), migrate.Decision{Yes: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"box"}, rep.Migrated)

	// The root is exactly the three tiers — no flat survivor beside them, which
	// is the property that makes "which directory is it in" a real answer.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{"host", "ro", "rw"}, names)

	// Each entry landed in its own tier, asserted through the live builders:
	// after this migration the staged layout IS the current one, so what the
	// current binary looks for is exactly the claim.
	assert.FileExists(t, config.EnvironmentPath(dir))
	assert.FileExists(t, config.AgentConfigPath(dir))
	assert.FileExists(t, config.NetpolicyPath(dir))
	assert.FileExists(t, config.SandboxStatePath(dir))
	assert.FileExists(t, filepath.Join(dir, "ro", "runtime-config.json"))
	assert.FileExists(t, filepath.Join(dir, "ro", "bin", "entrypoint.sh"))
	assert.FileExists(t, filepath.Join(dir, "rw", "work", "^proj", "README.md"))
	assert.DirExists(t, filepath.Join(dir, "rw", "logs"))

	// The records still load, which a file-existence check cannot tell from a
	// truncated copy.
	env, err := store.LoadEnvironment(dir)
	require.NoError(t, err)
	assert.Equal(t, "box", env.Name)

	// The realm advances, and the tree carries its own marker so a crash between
	// the promotion and the stamp is recoverable.
	v, _, err := config.ReadSchemaVersion(layout.SchemaVersionPath())
	require.NoError(t, err)
	assert.Equal(t, config.SchemaTiered, v)
	assert.FileExists(t, filepath.Join(layout.SandboxesDir(), ".tier-version"))
}

// The unit promoted is the whole tree, so anything in it that is not a sandbox
// has to ride across. A lock file dropped here would not be noticed until two
// processes raced.
func TestTierLayout_Apply_CarriesNonSandboxSiblings(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedRealm(t, layout)
	seedFlatSandbox(t, layout, "box")
	lock := filepath.Join(layout.SandboxesDir(), "box.lock")
	require.NoError(t, os.WriteFile(lock, nil, 0o600))
	stray := filepath.Join(layout.SandboxesDir(), "not-a-sandbox")
	require.NoError(t, os.MkdirAll(stray, 0o750))

	_, err := newTierLayoutFor(layout).Apply(context.Background(), migrate.Decision{Yes: true})
	require.NoError(t, err)

	assert.FileExists(t, lock, "a sibling lock file must survive the tree swap")
	assert.DirExists(t, stray, "a directory that is not a sandbox is carried, not tiered")
	assert.NoDirExists(t, filepath.Join(stray, "host"), "a non-sandbox dir must not be tiered")
}

// An entry nobody classified goes to host/ — the fail-safe direction — and is
// named in the plan. It must NOT ask for confirmation: one stray .DS_Store would
// otherwise make every macOS upgrade interactive and abort every headless one.
func TestTierLayout_UnclassifiedEntryGoesToHostAndIsNamedNotPrompted(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedRealm(t, layout)
	dir := seedFlatSandbox(t, layout, "box")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("x"), 0o600))

	plan, err := newTierLayoutFor(layout).Plan(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, plan.Ops)
	assert.Contains(t, plan.Ops[0].Description, ".DS_Store", "an unclassified entry must be named")
	assert.Equal(t, migrate.AuthNone, plan.Ops[0].Auth, "it is a warning, not a decision to make")

	_, err = newTierLayoutFor(layout).Apply(context.Background(), migrate.Decision{})
	require.NoError(t, err, "an unclassified entry must not need --yes")
	assert.FileExists(t, filepath.Join(dir, "host", ".DS_Store"),
		"the unknown default must be host/, where it can only lose guest access")
}

// A sandbox that already has a tier directory was created by an unreleased build
// of this branch, not by a release. Migrating it would merge two layouts; it is
// refused with an instruction instead.
func TestTierLayout_Plan_AlreadyTieredSandboxIsBlocked(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedRealm(t, layout)
	dir := seedFlatSandbox(t, layout, "box")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "host"), 0o750))

	plan, err := newTierLayoutFor(layout).Plan(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, plan.Ops)
	assert.Equal(t, migrate.AuthBlocked, plan.Ops[0].Auth)
	assert.Contains(t, plan.Ops[0].Description, "destroyed and recreated")
}

// The design's central guarantee: everything that can go wrong goes wrong while
// the live tree is still read-only, so a failure leaves a realm some released
// binary can still read rather than one stranded between two schemas.
//
// Verification is what makes that true for the dangerous half — a record that
// will not load from its new tier path. The staged tree is discarded and the
// live one must be byte-for-byte what it was, still flat, still stamped v5.
func TestTierLayout_Apply_VerificationFailureLeavesTheLiveTreeUntouched(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedRealm(t, layout)
	dir := seedFlatSandbox(t, layout, "box")
	// A record this binary cannot load. It is still a sandbox by shape, so the
	// migration takes it on and only the post-build verification can catch it.
	testutil.WriteSandboxRecord(t, filepath.Join(dir, "environment.json"),
		[]byte(`{"version":99,"name":"box"}`))

	_, err := newTierLayoutFor(layout).Apply(context.Background(), migrate.Decision{Yes: true})
	require.Error(t, err, "an unreadable record must fail the migration")
	assert.Contains(t, err.Error(), "box")

	assert.FileExists(t, filepath.Join(dir, "environment.json"), "the live record must stay where it was")
	assert.FileExists(t, filepath.Join(dir, "work", "^proj", "README.md"), "live work must be untouched")
	assert.NoDirExists(t, filepath.Join(dir, "host"), "no tier may exist after a refused migration")
	v, _, err := config.ReadSchemaVersion(layout.SchemaVersionPath())
	require.NoError(t, err)
	assert.Equal(t, 5, v, "the realm must not certify a migration that did not happen")
}

// A re-run after the stamp is a no-op, and a re-run of a tree already marked
// tiered must not tier it twice (which would nest host/ inside host/).
func TestTierLayout_Apply_IsIdempotent(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedRealm(t, layout)
	dir := seedFlatSandbox(t, layout, "box")

	m := newTierLayoutFor(layout)
	_, err := m.Apply(context.Background(), migrate.Decision{Yes: true})
	require.NoError(t, err)
	_, err = m.Apply(context.Background(), migrate.Decision{Yes: true})
	require.NoError(t, err, "a second run must be a no-op")

	assert.NoDirExists(t, filepath.Join(dir, "host", "host"), "the tier move must not nest")
	assert.FileExists(t, config.EnvironmentPath(dir))
	plan, err := m.Plan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, plan.Ops, "an already-tiered realm has nothing to plan")
}

// Build must produce a COMPLETE tree, and this asserts it directly rather than
// through Apply, because Apply cannot see the difference: the promotion's
// repopulate step copies anything Build omitted back out of the displaced
// original, so a Build that drops entries still yields a correct final tree.
//
// That safety net is exactly why the invariant needs its own test. The design
// depends on completeness for a different reason than correctness — a complete
// Build makes repopulate's filter (entries(orig) \ entries(newer)) empty by
// construction, which is what lets this migration ride the existing Promotion
// with no change to it. Lose completeness silently and the tree being swapped in
// is no longer the tree that was verified.
func TestTierLayout_BuildProducesACompleteTree(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedRealm(t, layout)
	seedFlatSandbox(t, layout, "box")
	require.NoError(t, os.WriteFile(filepath.Join(layout.SandboxesDir(), "box.lock"), nil, 0o600))

	staged := filepath.Join(t.TempDir(), "staged")
	require.NoError(t, os.MkdirAll(staged, 0o750))
	require.NoError(t, newTierLayoutFor(layout).buildTieredTree(staged))

	live, err := os.ReadDir(layout.SandboxesDir())
	require.NoError(t, err)
	for _, e := range live {
		assert.True(t, existsInStaged(staged, e.Name()),
			"%s is missing from the staged tree — Build must produce every entry, not rely on repopulate", e.Name())
	}
	// And the sandbox among them is tiered rather than merely copied.
	assert.FileExists(t, config.EnvironmentPath(filepath.Join(staged, "box")))
}

func existsInStaged(staged, name string) bool {
	_, err := os.Lstat(filepath.Join(staged, name))
	return err == nil
}

// A running sandbox must be refused, and this is the one precondition the disk
// cannot answer. The promotion renames sandboxes/ out from under a live
// container whose bind mounts keep pointing at the displaced inodes: the agent
// goes on writing into trash/ and loses everything the moment it is cleared —
// silently, because every host-side check still passes.
func TestTierLayout_Plan_RunningSandboxIsBlocked(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedRealm(t, layout)
	seedFlatSandbox(t, layout, "box")

	m := NewTierLayout(layout, layout.DataDir, layout.SandboxesDir(),
		func(context.Context, runtime.BackendType) (runtime.Backend, error) {
			return &fakeBackend{keepAlive: runtime.KeepAliveGuestOSInit, running: true}, nil
		})
	plan, err := m.Plan(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, plan.Ops)
	assert.Equal(t, migrate.AuthBlocked, plan.Ops[0].Auth)
	assert.Contains(t, plan.Ops[0].Description, "stop it and re-run migrate")

	// The refusal has to name a route that exists. `yoloai stop` is the obvious
	// move and it is refused by the same out-of-date-directory gate that sent the
	// operator here, so a message saying only "stop it" is a closed loop: migrate
	// says stop, stop says migrate. Observed end to end on a v0.10.0 install.
	assert.Contains(t, plan.Ops[0].Description, "`yoloai stop` will not do it from this build",
		"the refusal must say the obvious remedy is blocked")
	assert.Contains(t, plan.Ops[0].Description, "docker stop",
		"and must name a route out that does not need this binary")

	// Clearing it needs no older release — that route is for work this build
	// cannot reach at all, and offering it here would cost a rebuild to fix a
	// sandbox that only needs stopping.
	assert.False(t, plan.Ops[0].NeedsOlderRelease,
		"a running sandbox is cleared in place, so the downgrade route must not be offered")
}

// A backend that cannot be constructed on this host reports nothing running,
// which is sound rather than lenient: a tart VM cannot be live on a machine that
// cannot run tart. It is also what keeps a Linux host able to migrate the
// macOS-only sandboxes it is holding — get this wrong and those become
// permanently unmigratable on the host they live on.
func TestTierLayout_Plan_AbsentBackendIsNotRunning(t *testing.T) {
	layout := config.NewLayout(t.TempDir()).WithPrincipal(config.CLIPrincipal)
	seedRealm(t, layout)
	seedFlatSandbox(t, layout, "box")

	m := NewTierLayout(layout, layout.DataDir, layout.SandboxesDir(),
		func(context.Context, runtime.BackendType) (runtime.Backend, error) {
			return nil, errors.New("tart is not installed on this host")
		})
	plan, err := m.Plan(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, plan.Ops)
	assert.Equal(t, migrate.AuthNone, plan.Ops[0].Auth,
		"a sandbox whose backend is absent here cannot be running here")
}
