package cli

// ABOUTME: Guards the Execute() entry point against regressing the bootstrap
// ABOUTME: wiring (--data-dir + the startup migration gate) that NewRootCmd installs.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedFlatV0 lays down a pre-namespace (v0) flat install directly under top: a
// flat config.yaml plus a library-owned dir and a CLI-owned extensions file.
// Under the gate, both realms read as Fresh on a non-empty TOP — the
// migration-required signal.
func seedFlatV0(t *testing.T, top string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(top, "sandboxes", "box1"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(top, "extensions"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(top, "config.yaml"), []byte("agent: claude\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(top, "sandboxes", "box1", "marker"), []byte("x"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(top, "extensions", "demo.yaml"), []byte("action: echo hi\n"), 0600))
}

func withArgs(t *testing.T, args ...string) {
	t.Helper()
	saved := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = saved })
}

// TestExecute_RunsGate is the only test that drives the real Execute() entry
// point rather than NewRootCmd().ExecuteContext() directly. Execute installs
// its own PersistentPreRunE (logger + bug-report setup); this asserts it still
// chains to the bootstrap PersistentPreRunE that NewRootCmd installs (which
// applies --data-dir and runs the migration gate). A naive reassignment of
// PersistentPreRunE in Execute silently disables both, and no other test
// catches it because they all bypass Execute.
func TestExecute_RunsGate(t *testing.T) {
	t.Run("non-exempt command fails fast on a v0 dir", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		cliutil.SetRootLayout(config.Layout{})
		t.Cleanup(func() { cliutil.SetRootLayout(config.Layout{}) })

		top := filepath.Join(home, ".yoloai")
		seedFlatV0(t, top)
		withArgs(t, "yoloai", "system", "disk")

		exitCode := Execute(context.Background(), "test", "test", "test")
		assert.Equal(t, yoerrors.ExitMigrationRequired, exitCode,
			"a v0 dir must fail fast with the migration-required exit code")

		// The gate is read-only: nothing relocated, nothing created.
		assert.FileExists(t, filepath.Join(top, "config.yaml"), "gate must not relocate flat config.yaml")
		assert.NoDirExists(t, filepath.Join(top, "library"))
		assert.NoDirExists(t, filepath.Join(top, "cli"))
	})

	t.Run("exempt version command bypasses the gate", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		cliutil.SetRootLayout(config.Layout{})
		t.Cleanup(func() { cliutil.SetRootLayout(config.Layout{}) })

		top := filepath.Join(home, ".yoloai")
		seedFlatV0(t, top)
		withArgs(t, "yoloai", "version")

		exitCode := Execute(context.Background(), "test", "test", "test")
		assert.Equal(t, 0, exitCode, "version is gate-exempt and must succeed on a v0 dir")

		// Exempt commands never trigger create-fresh or relocation.
		assert.FileExists(t, filepath.Join(top, "config.yaml"))
		assert.NoDirExists(t, filepath.Join(top, "library"))
		assert.NoDirExists(t, filepath.Join(top, "cli"))
	})

	t.Run("fresh dir is created for a non-exempt command", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		cliutil.SetRootLayout(config.Layout{})
		t.Cleanup(func() { cliutil.SetRootLayout(config.Layout{}) })

		top := filepath.Join(home, ".yoloai") // absent → fresh
		withArgs(t, "yoloai", "system", "disk")

		_ = Execute(context.Background(), "test", "test", "test")

		// Regardless of the command's own outcome, the gate must have
		// create-freshed both realms (stamps present at the current version).
		cliV, ok, err := config.ReadSchemaVersion(config.SchemaVersionPathFor(filepath.Join(top, "cli")))
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, cliutil.CLISchemaVersion, cliV)

		libV, ok, err := config.ReadSchemaVersion(config.SchemaVersionPathFor(filepath.Join(top, "library")))
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, config.LibrarySchemaVersion, libV)
	})
}
