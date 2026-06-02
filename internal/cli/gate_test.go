package cli

// ABOUTME: Unit tests for the startup migration gate's decision tree —
// ABOUTME: fresh-create, migration-required, inconsistent, too-new, proceed.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateTestTop isolates HOME so TopDir() resolves to a fresh $HOME/.yoloai and
// returns that TOP path. It resets the process-wide root layout around the test.
func gateTestTop(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cliutil.SetRootLayout(config.Layout{})
	t.Cleanup(func() { cliutil.SetRootLayout(config.Layout{}) })
	return filepath.Join(home, ".yoloai")
}

// libStamp writes a plain-int version stamp into TOP/library, creating it.
func libStamp(t *testing.T, top string, version int) {
	t.Helper()
	dir := filepath.Join(top, "library")
	require.NoError(t, os.MkdirAll(dir, 0750))
	require.NoError(t, config.WriteSchemaVersion(config.SchemaVersionPathFor(dir), version))
}

// cliStamp writes a plain-int version stamp into TOP/cli, creating it.
func cliStamp(t *testing.T, top string, version int) {
	t.Helper()
	dir := filepath.Join(top, "cli")
	require.NoError(t, os.MkdirAll(dir, 0750))
	require.NoError(t, config.WriteSchemaVersion(config.SchemaVersionPathFor(dir), version))
}

func dummyCmd() *cobra.Command { return &cobra.Command{Use: "dummy"} }

func TestGate_EmptyTop_CreatesFresh(t *testing.T) {
	top := gateTestTop(t)

	require.NoError(t, runMigrationGate(dummyCmd()))

	cliV, ok, err := config.ReadSchemaVersion(config.SchemaVersionPathFor(filepath.Join(top, "cli")))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, cliutil.CLISchemaVersion, cliV)

	libV, ok, err := config.ReadSchemaVersion(config.SchemaVersionPathFor(filepath.Join(top, "library")))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, config.LibrarySchemaVersion, libV)
}

func TestGate_FlatV0_BothFresh_RequiresMigration(t *testing.T) {
	top := gateTestTop(t)
	// A pre-namespace flat install: TOP non-empty, but neither realm dir exists.
	require.NoError(t, os.MkdirAll(top, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(top, "config.yaml"), []byte("agent: claude\n"), 0600))

	err := runMigrationGate(dummyCmd())
	_, ok := errors.AsType[*yoerrors.MigrationRequiredError](err)
	require.True(t, ok, "expected MigrationRequiredError, got %v", err)
	assert.Contains(t, err.Error(), "yoloai system migrate")

	// The gate must not have relocated or created anything.
	assert.NoDirExists(t, filepath.Join(top, "library"))
	assert.NoDirExists(t, filepath.Join(top, "cli"))
}

func TestGate_BothPopulatedOld_RequiresMigration(t *testing.T) {
	top := gateTestTop(t)
	libStamp(t, top, config.LibrarySchemaVersion-1) // 0
	cliStamp(t, top, cliutil.CLISchemaVersion-1)    // 0 — needs >0 current to be Migrate

	err := runMigrationGate(dummyCmd())
	_, ok := errors.AsType[*yoerrors.MigrationRequiredError](err)
	require.True(t, ok, "expected MigrationRequiredError, got %v", err)
}

func TestGate_OneFreshOnePopulated_Inconsistent(t *testing.T) {
	top := gateTestTop(t)
	// library present at current; cli absent → exactly one realm fresh.
	libStamp(t, top, config.LibrarySchemaVersion)

	err := runMigrationGate(dummyCmd())
	_, ok := errors.AsType[*yoerrors.InconsistentDataDirError](err)
	require.True(t, ok, "expected InconsistentDataDirError, got %v", err)
	assert.NotContains(t, err.Error(), "yoloai system migrate", "inconsistent dir must not point at migrate")
}

func TestGate_BothCurrent_Proceeds(t *testing.T) {
	top := gateTestTop(t)
	libStamp(t, top, config.LibrarySchemaVersion)
	cliStamp(t, top, cliutil.CLISchemaVersion)

	require.NoError(t, runMigrationGate(dummyCmd()))
}

func TestGate_TooNew_Errors(t *testing.T) {
	top := gateTestTop(t)
	libStamp(t, top, config.LibrarySchemaVersion+1)
	cliStamp(t, top, cliutil.CLISchemaVersion)

	err := runMigrationGate(dummyCmd())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upgrade yoloai")
}

func TestGate_ExemptCommand_Skipped(t *testing.T) {
	top := gateTestTop(t)
	// A flat-v0 install that would otherwise fail the gate.
	require.NoError(t, os.MkdirAll(top, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(top, "config.yaml"), []byte("x\n"), 0600))

	exempt := &cobra.Command{Use: "version", Annotations: cliutil.SkipMigrationGateAnnotations}
	require.NoError(t, runMigrationGate(exempt), "exempt command must bypass the gate")
	assert.NoDirExists(t, filepath.Join(top, "library"))
}
