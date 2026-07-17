// ABOUTME: The startup migration gate — a read-only check that decides, per
// ABOUTME: command, whether to create-fresh, fail-fast ("run migrate"), or proceed.

package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// runMigrationGate is the startup gate. It runs in the root PersistentPreRunE
// (after --data-dir is applied) for every non-exempt command and is the only
// place the normal startup path touches on-disk schema state — and even then
// it only ever *creates* a genuinely fresh install or *reads* status. All
// mutation of an existing dir lives in `yoloai system migrate`.
//
// The decision tree (TOP = the shared top data dir, parent of TOP/library and
// TOP/cli):
//
//   - TOP absent, empty, or holding nothing but the TOP/.initializing sentinel
//     -> the fresh cases: create both realms, proceed. The sentinel counts as
//     empty here because nothing else exists beside it, so there is nothing a
//     rebuild could damage (DF128).
//   - TOP non-empty: check each realm (a too-new on-disk version surfaces as an
//     error — the user ran an older binary and must upgrade):
//   - both realms Fresh         -> MigrationRequired (a v0 flat install).
//   - exactly one realm Fresh   -> an interrupted build when the sentinel is
//     present and the other realm is current (retry it, quietly); otherwise
//     InconsistentDataDir (a realm went missing; loud, does not point at
//     migrate).
//   - any realm needs Migrate   -> MigrationRequired. The sentinel NEVER
//     overrides this; see checkDataDirStatus.
//   - both realms OK            -> proceed (clearing a stale sentinel).
//
// The gate never sniffs flat/legacy markers; routing "all-Fresh on a non-empty
// TOP" to migration is all it needs. Recognizing what that content actually is
// belongs to the migrate command.
func runMigrationGate(cmd *cobra.Command) error {
	if gateExempt(cmd) {
		return nil
	}

	top := cliutil.TopDir()
	empty, err := dirAbsentOrEmpty(top)
	if err != nil {
		return err
	}
	if empty {
		return initFreshDataDir()
	}
	return checkDataDirStatus(top)
}

// initFreshDataDir initializes both the CLI and library data directories. It
// creates realms at the current version and never converts anything, so its
// callers must have established that there is nothing here to convert: TOP is
// absent or empty, or holds only the sentinel, or holds one current realm and
// one missing one (see resumableInit). It must not be reached for a realm at
// LayoutMigrate — CreateFreshLibrary would stamp the current version over
// unconverted data.
//
// The sentinel it writes brackets the build: MarkInitializing before either
// realm exists, ClearInitializing only once both do. That is D110's
// stamp-written-last mirrored — the migration stamp goes last so it can never
// precede the data it certifies; this one goes first so an interrupted build
// leaves a fact behind instead of a state the gate has to guess at (DF128).
// It records that a build started; it certifies nothing about the result.
func initFreshDataDir() error {
	top := cliutil.TopDir()
	if err := fileutil.MkdirAll(top, 0750); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if err := cliutil.MarkInitializing(); err != nil {
		return fmt.Errorf("mark data dir initializing: %w", err)
	}
	if err := cliutil.CreateFreshCLI(); err != nil {
		return fmt.Errorf("initialize cli data dir: %w", err)
	}
	sys, err := cliutil.System()
	if err != nil {
		return err
	}
	if err := sys.CreateDataDir(); err != nil {
		return fmt.Errorf("initialize library data dir: %w", err)
	}
	return cliutil.ClearInitializing()
}

// checkDataDirStatus reads both realm statuses on a non-empty TOP and decides
// whether to proceed, require migration, or surface an inconsistency error.
func checkDataDirStatus(top string) error {
	cliSt, err := cliutil.CLIStatus()
	if err != nil {
		return err
	}
	sys, err := cliutil.System()
	if err != nil {
		return err
	}
	libSt, err := sys.DataDirStatus()
	if err != nil {
		return err
	}

	cliFresh := cliSt == config.LayoutFresh
	libFresh := libSt == config.LayoutFresh

	switch {
	case cliFresh && libFresh:
		// Non-empty TOP but neither realm is initialized: a pre-namespace
		// (v0) flat install. Migration relocates it.
		return yoerrors.NewMigrationRequiredError("")
	case cliFresh != libFresh:
		if resumableInit(cliSt, libSt) {
			// An interrupted initFreshDataDir: the sentinel says a build
			// started, and the realm it did finish is at the current version,
			// which is what our own build creates. Finishing it re-stamps that
			// realm at the version it already carries (a no-op) and creates the
			// missing one. Nothing is migrated, so nothing is mis-certified.
			return initFreshDataDir()
		}
		// One realm fresh, the other populated — a realm went missing from an
		// otherwise-present install. Too messy to reconcile automatically.
		// Without a sentinel this is DF128's genuine anomaly and stays loud:
		// the sentinel is what distinguishes it from a routine interrupted
		// first run, and there isn't one.
		fresh, populated := "cli", "library"
		if libFresh {
			fresh, populated = "library", "cli"
		}
		return yoerrors.NewInconsistentDataDirError(
			"inconsistent data directory under %s: the %s realm is uninitialized but the %s realm is present; this should not happen — inspect the directory manually",
			top, fresh, populated)
	case cliSt == config.LayoutMigrate || libSt == config.LayoutMigrate:
		// A realm needs migrating, and a sentinel does NOT override that — a
		// stale one outlives its build indefinitely, so it cannot vouch for
		// what the realms hold. Only `system migrate` converts data and stamps
		// it afterwards (D110); a gate that stamped here would certify data it
		// never converted.
		return yoerrors.NewMigrationRequiredError(migratingNamespace(cliSt, libSt))
	default:
		// Both realms at the current version. A sentinel here is the build
		// whose final remove failed: clear it and proceed, rebuilding nothing.
		if cliutil.IsInitializing() {
			return cliutil.ClearInitializing()
		}
		return nil
	}
}

// resumableInit reports whether an exactly-one-realm-Fresh TOP is an
// initFreshDataDir that was interrupted partway, which the gate may safely
// finish, rather than DF128's genuine anomaly, which it must report.
//
// Two things must hold. The sentinel must be present: it is the recorded fact
// that a build started, and without it a missing realm is unexplained. And the
// realm that does exist must be LayoutOK — a fresh build creates realms at the
// current version, so one needing migration was not left by the build the
// sentinel describes, and re-creating it would re-stamp unconverted data.
// A realm at LayoutMigrate therefore falls through to the loud message, and
// `system migrate` remains the only thing that may touch it.
func resumableInit(cliSt, libSt config.LayoutStatus) bool {
	if !cliutil.IsInitializing() {
		return false
	}
	return cliSt != config.LayoutMigrate && libSt != config.LayoutMigrate
}

// migratingNamespace names the realm that needs migration for the diagnostic
// message, or "" when both do (the recovery is identical either way).
func migratingNamespace(cliSt, libSt config.LayoutStatus) string {
	switch {
	case cliSt == config.LayoutMigrate && libSt == config.LayoutMigrate:
		return ""
	case cliSt == config.LayoutMigrate:
		return "cli"
	default:
		return "library"
	}
}

// gateExempt reports whether cmd must run even on an un-migrated or empty data
// dir. Exempt commands carry cliutil.AnnotationSkipMigrationGate (version,
// help, system migrate) or are part of Cobra's completion machinery (the
// hidden __complete* runtime commands and the visible `completion` command
// tree). The check walks from cmd up through its ancestors.
func gateExempt(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd, "completion":
			return true
		}
		if _, ok := c.Annotations[cliutil.AnnotationSkipMigrationGate]; ok {
			return true
		}
	}
	return false
}

// dirAbsentOrEmpty reports whether dir does not exist, or exists holding
// nothing but (at most) the TOP/.initializing sentinel — the gate's "fresh
// install" cases. A non-"not exist" read error (e.g. TOP is a plain file) is
// surfaced so the gate fails loudly rather than treating garbage as fresh.
//
// The sentinel does not count against emptiness because a TOP holding only it
// is one where a build wrote the sentinel and then died before creating either
// realm: nothing exists that a rebuild could damage. Counting it would leave
// that TOP wedged — not empty, so routed to checkDataDirStatus, where both
// realms read Fresh and it becomes "run system migrate", which then recognizes
// no case for it and refuses as an unrecognized data directory (DF128).
//
// This is the ONLY place the sentinel is allowed to mean "nothing is here".
// Once anything else exists beside it, what is on disk decides — see
// checkDataDirStatus.
func dirAbsentOrEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true, nil
		}
		return false, fmt.Errorf("inspect data dir %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.Name() != cliutil.InitializingSentinelName {
			return false, nil
		}
	}
	return true, nil
}
