package system

// ABOUTME: Tests for `yoloai system migrate` — the v0 flat -> namespaced
// ABOUTME: relocation, idempotent re-run, and refusal to mangle a garbage dir.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// migrateTestTop isolates HOME and points the process-wide root layout at
// $HOME/.yoloai (runMigrate executes the migrate subcommand standalone,
// bypassing the root command's PersistentPreRunE that would otherwise establish
// it). Returns that TOP path and clears the layout on cleanup.
func migrateTestTop(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cliutil.SetRootLayout(cliutil.LayoutForDataDir(filepath.Join(home, ".yoloai")))
	t.Cleanup(func() { cliutil.SetRootLayout(config.Layout{}) })
	return filepath.Join(home, ".yoloai")
}

// seedFlatV0 lays down a pre-namespace (v0) flat install: a flat config.yaml,
// a library-owned sandboxes tree, a CLI-owned extensions file, and a legacy
// state.yaml recording that first-run setup already completed.
func seedFlatV0(t *testing.T, top string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(top, "sandboxes", "box1"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(top, "extensions"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(top, "config.yaml"), []byte("agent: claude\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(top, "sandboxes", "box1", "marker"), []byte("x"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(top, "extensions", "demo.yaml"), []byte("action: echo hi\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(top, "state.yaml"), []byte("setup_complete: true\n"), 0600))
}

func runMigrate(t *testing.T) (string, error) {
	t.Helper()
	cmd := newSystemMigrateCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	return buf.String(), err
}

func TestMigrate_FlatV0_RelocatesAndStamps(t *testing.T) {
	top := migrateTestTop(t)
	seedFlatV0(t, top)

	out, err := runMigrate(t)
	require.NoError(t, err)
	assert.Contains(t, out, "migrated successfully")

	// Library-owned content relocated under TOP/library.
	assert.FileExists(t, filepath.Join(top, "library", "config.yaml"))
	assert.FileExists(t, filepath.Join(top, "library", "sandboxes", "box1", "marker"))
	// CLI-owned content relocated under TOP/cli.
	assert.FileExists(t, filepath.Join(top, "cli", "extensions", "demo.yaml"))
	// The flat originals are gone.
	assert.NoFileExists(t, filepath.Join(top, "config.yaml"))
	assert.NoDirExists(t, filepath.Join(top, "sandboxes"))

	// Both realms stamped at the current version.
	libV, ok, err := config.ReadSchemaVersion(config.SchemaVersionPathFor(filepath.Join(top, "library")))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, config.LibrarySchemaVersion, libV)
	cliV, ok, err := config.ReadSchemaVersion(config.SchemaVersionPathFor(filepath.Join(top, "cli")))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, cliutil.CLISchemaVersion, cliV)

	// Legacy setup_complete carried forward as first-run-tip suppression; the
	// old flat state file is dropped.
	assert.NoFileExists(t, filepath.Join(top, "state.yaml"))
	st, err := cliutil.LoadCLIState()
	require.NoError(t, err)
	assert.True(t, st.FirstRunTipShown, "legacy setup_complete must suppress the first-run tip")
}

// seedPreV3Record adds a sandbox record at the schema the flat-v0 era wrote —
// below the current record version, so every reader correctly refuses it until
// the sealed ladder relocates it. That refusal is DF168's trigger: the framework
// migrators' pre-flight plan used to run first and hit exactly this.
//
// The principal is pre-set only to keep the test off a backend: PrincipalRename
// skips an already-scoped record, and with no overlay dirs OverlayFlatten skips
// it too, so the post-ladder plan needs no runtime. Nothing about the ordering
// under test depends on it.
func seedPreV3Record(t *testing.T, top string) {
	t.Helper()
	rec := `{"version":1,"name":"box1","backend":"docker","principal":"cli",` +
		`"workdir":{"host_path":"/proj","mount_path":"/proj","mode":"copy"}}`
	require.NoError(t, os.WriteFile(
		filepath.Join(top, "sandboxes", "box1", "environment.json"), []byte(rec), 0600))
}

// A pre-v3 install must migrate. Until DF168 was fixed this aborted before
// mutating anything — the pre-flight refusal planned the framework migrators
// against a record only the sealed ladder could raise, so `system migrate`
// refused on the grounds that it had not run yet, and every release since v0.6.0
// refused identically. There was no stepwise path out.
func TestMigrate_PreV3Record_LadderRunsBeforeThePreflightGuard(t *testing.T) {
	top := migrateTestTop(t)
	seedFlatV0(t, top)
	seedPreV3Record(t, top)

	out, err := runMigrate(t)
	require.NoError(t, err, "a realm below the frozen ceiling must still migrate")
	assert.Contains(t, out, "migrated successfully")

	// The ladder ran end to end: the v2->v3 relocation produced the sibling
	// records, and the v5->v6 tier move then carried them into host/. Asserted
	// through the live builders because that is the claim — the current binary
	// must find these where it now looks for them.
	box := filepath.Join(top, "library", "sandboxes", "box1")
	assert.FileExists(t, config.AgentConfigPath(box))
	assert.FileExists(t, config.NetpolicyPath(box))
	assert.FileExists(t, config.EnvironmentPath(box))
	assert.NoFileExists(t, filepath.Join(box, "environment.json"),
		"the flat record must not survive beside its tiered copy")
	libV, ok, err := config.ReadSchemaVersion(config.SchemaVersionPathFor(filepath.Join(top, "library")))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, config.LibrarySchemaVersion, libV, "the realm must reach the current schema")
}

// --check took the same path and failed the same way, which is worse than the
// apply failing: the audit is what a cautious user runs first, and it told the
// oldest installs their data dir was unreadable rather than that it was old.
func TestMigrate_PreV3Record_CheckReportsTheDeferredPlan(t *testing.T) {
	top := migrateTestTop(t)
	seedFlatV0(t, top)
	seedPreV3Record(t, top)

	cmd := newSystemMigrateCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--check"})
	require.NoError(t, cmd.Execute(), "--check must audit a pre-v3 realm, not refuse it")

	out := buf.String()
	assert.Contains(t, out, "not yet derivable", "the absent plan must say why it is absent")
	assert.NotContains(t, out, "needs migration before use", "the record-floor error must not surface")

	// Read-only: --check must not have run the ladder it described.
	assert.NoFileExists(t, filepath.Join(top, "library", "sandboxes", "box1", "agent.json"))
	assert.FileExists(t, filepath.Join(top, "sandboxes", "box1", "environment.json"))
}

// An out-of-date realm with nothing to restructure must say so. The two meanings
// of an empty plan are opposite — "nothing to do" against "the version stamp is
// the whole job" — and the unqualified wording printed the first one directly
// under "Library realm: needs migration", which reads as a contradiction in the
// one command whose entire purpose is to tell an operator what is about to
// happen. Observed on a real install carrying no sandboxes at all.
func TestMigrate_OutOfDateRealmWithNoOps_SaysTheStampIsTheWork(t *testing.T) {
	top := migrateTestTop(t)
	seedFlatV0(t, top)
	require.NoError(t, os.RemoveAll(filepath.Join(top, "sandboxes")))

	_, err := runMigrate(t)
	require.NoError(t, err)

	// Roll the library stamp back one rung, above the frozen ceiling so the
	// framework plan is derivable — and derives empty, there being no sandbox.
	libDir := filepath.Join(top, "library")
	require.NoError(t, config.WriteSchemaVersion(
		config.SchemaVersionPathFor(libDir), config.LibrarySchemaVersion-1))

	cmd := newSystemMigrateCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--check"})
	require.NoError(t, cmd.Execute())

	out := buf.String()
	require.Contains(t, out, "needs migration", "precondition: the realm must be out of date")
	assert.Contains(t, out, "version stamp alone",
		"an empty plan under an out-of-date realm must say the stamp is the work")
	assert.NotContains(t, out, "No pending framework migrations",
		"the unqualified wording contradicts the realm line above it")
}

func TestMigrate_Idempotent(t *testing.T) {
	top := migrateTestTop(t)
	seedFlatV0(t, top)

	_, err := runMigrate(t)
	require.NoError(t, err)

	out, err := runMigrate(t)
	require.NoError(t, err)
	assert.Contains(t, out, "already up to date")
}

func TestMigrate_GarbageTop_Errors(t *testing.T) {
	top := migrateTestTop(t)
	// A non-empty TOP that is neither a flat v0 install (no config.yaml) nor a
	// namespaced layout (no library/cli dirs).
	require.NoError(t, os.MkdirAll(top, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(top, "junk"), []byte("?"), 0600))

	out, err := runMigrate(t)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a recognized yoloai data directory")
	assert.NotContains(t, out, "migrated successfully")

	// Nothing was relocated or created.
	assert.NoDirExists(t, filepath.Join(top, "library"))
	assert.NoDirExists(t, filepath.Join(top, "cli"))
}
