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

// migrateTestTop isolates HOME so the data dir resolves to $HOME/.yoloai and
// returns that TOP path, resetting the process-wide root layout around the test.
func migrateTestTop(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cliutil.SetRootLayout(config.Layout{})
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
