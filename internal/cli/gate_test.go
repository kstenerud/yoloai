package cli

// ABOUTME: Unit tests for the startup migration gate's decision tree —
// ABOUTME: fresh-create, migration-required, inconsistent, too-new, proceed.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateTestTop isolates HOME and sets the process-wide root layout to a fresh
// $HOME/.yoloai (these tests call runMigrationGate directly, bypassing the root
// command's PersistentPreRunE that would otherwise establish it). Returns that
// TOP path and clears the layout on cleanup.
func gateTestTop(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cliutil.SetRootLayout(cliutil.LayoutForDataDir(filepath.Join(home, ".yoloai")))
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

// sentinel writes TOP/.initializing directly, without going through
// initFreshDataDir — constructing on disk exactly the state a crashed
// initFreshDataDir would have left behind (DF128).
func sentinel(t *testing.T, top string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(top, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(top, ".initializing"), nil, 0600))
}

func dummyCmd() *cobra.Command { return &cobra.Command{Use: "dummy"} }

// gateExemptAllowlist is the COMPLETE set of yoloai commands that may bypass the
// startup migration gate, by full command path. Nothing else may ever be added.
//
//   - `help` and `version` must answer on any data dir, and read nothing.
//   - `system migrate` performs the migration, so gating it would deadlock.
//
// Cobra's completion machinery is exempted separately by name in gateExempt
// (`__complete`, `__completeNoDesc`, `completion`) — TestGateExemptionsAreClosed
// pins that too.
var gateExemptAllowlist = map[string]bool{
	"yoloai help":           true,
	"yoloai version":        true,
	"yoloai system migrate": true,
}

// TestGateExemptionsAreClosed is a mechanical guard on a safety invariant, not a
// unit test of behaviour: EVERY command in the tree is gated unless it is on the
// allowlist above. A new command is therefore gated by default, and adding an
// exemption fails here rather than shipping.
//
// Why this is worth a gate of its own. The gate is what guarantees no command
// touches a data dir below the current schema — and correctness elsewhere is
// built on that. `system prune`'s sweep claims the CLI's pre-D126 instances
// (DF125), which is safe ONLY because the realm is already at v5 by the time it
// can run: the v4->v5 migration stamps v5 only after every sandbox is migrated,
// so a `yoloai-<name>` instance at prune time is provably debris. Exempt prune
// and it runs below v5, where every LIVE sandbox still carries the legacy name
// and no longer matches `known` — and the sweep deletes all of them. The blast
// radius of a one-line annotation is somebody's running work.
func TestGateExemptionsAreClosed(t *testing.T) {
	root := NewRootCmd("test", "test", "test")

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		path := c.CommandPath()
		// Cobra's own completion tree is exempt by name, not by annotation, and is
		// asserted separately below; skip it here.
		if !isCompletionCmd(c) {
			if got, want := gateExempt(c), gateExemptAllowlist[path]; got != want {
				if got {
					t.Errorf("command %q bypasses the migration gate but is not on the allowlist.\n"+
						"Nothing may be added to it: the gate is what proves the data dir is current before a\n"+
						"command touches it, and `system prune`'s legacy sweep (DF125) is only safe because of\n"+
						"that. If you truly need an exemption, justify it in the allowlist doc and in review.", path)
				} else {
					t.Errorf("command %q is on the gate allowlist but does NOT bypass the gate — "+
						"the allowlist and the annotations have drifted", path)
				}
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	// The allowlist must not name a command that no longer exists, or it silently
	// stops protecting anything.
	seen := map[string]bool{}
	var collect func(c *cobra.Command)
	collect = func(c *cobra.Command) {
		seen[c.CommandPath()] = true
		for _, sub := range c.Commands() {
			collect(sub)
		}
	}
	collect(root)
	for path := range gateExemptAllowlist {
		assert.True(t, seen[path], "allowlist names %q, which is not a command in the tree", path)
	}
}

// TestGateExemptsDataTouchingCommandsNever spot-checks the commands whose blast
// radius makes the invariant matter, so a regression names the victim rather than
// just a count.
func TestGateExemptsDataTouchingCommandsNever(t *testing.T) {
	root := NewRootCmd("test", "test", "test")
	for _, path := range []string{"system prune", "new", "ls", "destroy", "start", "stop", "diff", "apply"} {
		cmd, _, err := root.Find(splitPath(path))
		require.NoError(t, err, "command %q not found", path)
		require.Equal(t, path, trimRoot(cmd.CommandPath()), "resolved the wrong command for %q", path)
		assert.False(t, gateExempt(cmd), "%q must never bypass the migration gate — it touches the data dir", path)
	}
}

// isCompletionCmd reports whether c is part of Cobra's completion machinery,
// which gateExempt exempts by name.
func isCompletionCmd(c *cobra.Command) bool {
	for p := c; p != nil; p = p.Parent() {
		switch p.Name() {
		case cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd, "completion":
			return true
		}
	}
	return false
}

func splitPath(path string) []string { return strings.Fields(path) }

func trimRoot(path string) string { return strings.TrimPrefix(path, "yoloai ") }

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

// TestGate_CLIOnlyNoSentinel_StillLoud is the DF128 anomaly the sentinel must
// NOT silence: a populated cli/ with no library/ and no sentinel is exactly
// as alarming as before — a realm may have gone missing from an otherwise
// live install, and the gate has no fact on disk saying otherwise.
func TestGate_CLIOnlyNoSentinel_StillLoud(t *testing.T) {
	top := gateTestTop(t)
	cliStamp(t, top, cliutil.CLISchemaVersion)

	err := runMigrationGate(dummyCmd())
	_, ok := errors.AsType[*yoerrors.InconsistentDataDirError](err)
	require.True(t, ok, "expected InconsistentDataDirError, got %v", err)
	assert.NotContains(t, err.Error(), "yoloai system migrate", "inconsistent dir must not point at migrate")
}

// TestGate_SentinelOnly_Initializes covers the wedge the sentinel exists to
// avoid: a TOP whose only content is TOP/.initializing must initialize
// rather than being routed to MigrationRequired (where system migrate would
// find no case it recognizes — the trap the plan pins).
func TestGate_SentinelOnly_Initializes(t *testing.T) {
	top := gateTestTop(t)
	sentinel(t, top)

	require.NoError(t, runMigrationGate(dummyCmd()))

	assert.NoFileExists(t, filepath.Join(top, ".initializing"))
	cliV, ok, err := config.ReadSchemaVersion(config.SchemaVersionPathFor(filepath.Join(top, "cli")))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, cliutil.CLISchemaVersion, cliV)
	libV, ok, err := config.ReadSchemaVersion(config.SchemaVersionPathFor(filepath.Join(top, "library")))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, config.LibrarySchemaVersion, libV)
}

// TestGate_SentinelPlusCLI_Initializes covers a crash after the CLI realm
// finished but before the library realm started: the gate must finish the
// build (create the missing library realm) rather than refuse.
func TestGate_SentinelPlusCLI_Initializes(t *testing.T) {
	top := gateTestTop(t)
	sentinel(t, top)
	cliStamp(t, top, cliutil.CLISchemaVersion)

	require.NoError(t, runMigrationGate(dummyCmd()))

	assert.NoFileExists(t, filepath.Join(top, ".initializing"))
	libV, ok, err := config.ReadSchemaVersion(config.SchemaVersionPathFor(filepath.Join(top, "library")))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, config.LibrarySchemaVersion, libV)
}

// TestGate_SentinelPlusBothRealms_ClearsAndProceeds covers a crash after both
// realms finished but before the final sentinel removal: the gate must clear
// the stray sentinel and proceed, not treat it as still mid-build forever.
func TestGate_SentinelPlusBothRealms_ClearsAndProceeds(t *testing.T) {
	top := gateTestTop(t)
	sentinel(t, top)
	libStamp(t, top, config.LibrarySchemaVersion)
	cliStamp(t, top, cliutil.CLISchemaVersion)

	require.NoError(t, runMigrationGate(dummyCmd()))

	assert.NoFileExists(t, filepath.Join(top, ".initializing"))
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
