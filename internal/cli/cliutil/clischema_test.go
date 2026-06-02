package cliutil_test

// ABOUTME: Tests for the CLI flat->namespaced bootstrap migration and the
// ABOUTME: TOP/cli/state.yaml round-trip + first-run-tip suppression.

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

// isolatedTop sets HOME to a fresh temp dir, resets the process-wide root
// Layout so TopDir() falls back to $HOME/.yoloai, and returns that TOP path.
func isolatedTop(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cliutil.SetRootLayout(config.Layout{})
	return filepath.Join(home, ".yoloai")
}

// seedFlatInstall lays down a pre-namespace (v0) install directly under top:
// a flat config.yaml plus a few library-owned dirs, an extensions dir, and an
// optional legacy state.yaml.
func seedFlatInstall(t *testing.T, top string, setupComplete bool) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(top, "sandboxes", "box1"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(top, "profiles", "base"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(top, "cache"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(top, "extensions"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(top, "config.yaml"), []byte("agent: claude\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(top, "sandboxes", "box1", "marker"), []byte("x"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(top, "extensions", "demo.yaml"), []byte("action: echo hi\n"), 0600))
	if setupComplete {
		require.NoError(t, os.WriteFile(filepath.Join(top, "state.yaml"), []byte("setup_complete: true\n"), 0600))
	}
}

func TestMigrateCLI_RelocatesFlatInstall(t *testing.T) {
	top := isolatedTop(t)
	seedFlatInstall(t, top, true)

	require.NoError(t, cliutil.MigrateCLI())

	// Library-owned dirs moved under TOP/library, content preserved.
	assert.NoFileExists(t, filepath.Join(top, "config.yaml"))
	assert.NoDirExists(t, filepath.Join(top, "sandboxes"))
	assert.FileExists(t, filepath.Join(top, "library", "config.yaml"))
	assert.FileExists(t, filepath.Join(top, "library", "sandboxes", "box1", "marker"))
	assert.DirExists(t, filepath.Join(top, "library", "profiles", "base"))
	assert.DirExists(t, filepath.Join(top, "library", "cache"))

	// Extensions moved under TOP/cli.
	assert.NoDirExists(t, filepath.Join(top, "extensions"))
	assert.FileExists(t, filepath.Join(top, "cli", "extensions", "demo.yaml"))

	// Legacy flat state removed; setup_complete carried forward as tip-shown.
	assert.NoFileExists(t, filepath.Join(top, "state.yaml"))
	st, err := cliutil.LoadCLIState()
	require.NoError(t, err)
	assert.True(t, st.FirstRunTipShown)

	// Stamped at the current version.
	version, exists, err := config.ReadSchemaVersion(cliutil.CLISchemaVersionPath())
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, cliutil.CLISchemaVersion, version)
}

func TestMigrateCLI_SetupIncomplete_TipNotSuppressed(t *testing.T) {
	top := isolatedTop(t)
	seedFlatInstall(t, top, false) // no legacy state.yaml

	require.NoError(t, cliutil.MigrateCLI())

	st, err := cliutil.LoadCLIState()
	require.NoError(t, err)
	assert.False(t, st.FirstRunTipShown, "no prior setup_complete → onboarding tip must still fire")
}

func TestMigrateCLI_FreshInstall_DefersStamp(t *testing.T) {
	top := isolatedTop(t)

	require.NoError(t, cliutil.MigrateCLI())

	// Nothing on disk yet → no directories materialized, stamp deferred.
	assert.NoDirExists(t, top)
	_, exists, err := config.ReadSchemaVersion(cliutil.CLISchemaVersionPath())
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestMigrateCLI_NamespacedNoStamp_Stamps(t *testing.T) {
	top := isolatedTop(t)
	// An already-namespaced layout from an interim build, but no stamp yet.
	require.NoError(t, os.MkdirAll(filepath.Join(top, "library", "sandboxes"), 0750))

	require.NoError(t, cliutil.MigrateCLI())

	version, exists, err := config.ReadSchemaVersion(cliutil.CLISchemaVersionPath())
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, cliutil.CLISchemaVersion, version)
}

func TestMigrateCLI_AlreadyStamped_NoRelocation(t *testing.T) {
	top := isolatedTop(t)
	// A flat-looking layout but already stamped → stamp wins, no relocation.
	seedFlatInstall(t, top, false)
	require.NoError(t, os.MkdirAll(cliutil.CLIDir(), 0750))
	require.NoError(t, config.WriteSchemaVersion(cliutil.CLISchemaVersionPath(), cliutil.CLISchemaVersion))

	require.NoError(t, cliutil.MigrateCLI())

	assert.FileExists(t, filepath.Join(top, "config.yaml"), "stamped layout must not be relocated")
	assert.NoDirExists(t, filepath.Join(top, "library"))
}

func TestMigrateCLI_Idempotent(t *testing.T) {
	top := isolatedTop(t)
	seedFlatInstall(t, top, true)

	require.NoError(t, cliutil.MigrateCLI())
	require.NoError(t, cliutil.MigrateCLI(), "second run is a stamped no-op")

	assert.FileExists(t, filepath.Join(top, "library", "config.yaml"))
}

func TestMigrateCLI_NewerStamp_Errors(t *testing.T) {
	_ = isolatedTop(t)
	require.NoError(t, os.MkdirAll(cliutil.CLIDir(), 0750))
	require.NoError(t, config.WriteSchemaVersion(cliutil.CLISchemaVersionPath(), cliutil.CLISchemaVersion+1))

	err := cliutil.MigrateCLI()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "newer than this build supports")
}

func TestCLIState_RoundTrip(t *testing.T) {
	_ = isolatedTop(t)

	// Missing file → zero value, no error.
	st, err := cliutil.LoadCLIState()
	require.NoError(t, err)
	assert.False(t, st.FirstRunTipShown)

	require.NoError(t, cliutil.SaveCLIState(&cliutil.CLIState{FirstRunTipShown: true}))
	st, err = cliutil.LoadCLIState()
	require.NoError(t, err)
	assert.True(t, st.FirstRunTipShown)
}

func TestMaybeShowFirstRunTip_ShowsOnce(t *testing.T) {
	_ = isolatedTop(t)

	var buf bytes.Buffer
	cliutil.MaybeShowFirstRunTip(&buf)
	assert.Contains(t, buf.String(), "shell completions")

	buf.Reset()
	cliutil.MaybeShowFirstRunTip(&buf)
	assert.Empty(t, buf.String(), "tip fires exactly once")
}
