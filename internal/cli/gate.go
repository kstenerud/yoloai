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
//   - TOP absent or empty  -> the only fresh cases: create both realms, proceed.
//   - TOP non-empty: check each realm (a too-new on-disk version surfaces as an
//     error — the user ran an older binary and must upgrade):
//   - both realms Fresh         -> MigrationRequired (a v0 flat install).
//   - exactly one realm Fresh   -> InconsistentDataDir (a realm went missing;
//     loud, does not point at migrate).
//   - any realm needs Migrate   -> MigrationRequired.
//   - both realms OK            -> proceed.
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
		if err := cliutil.CreateFreshCLI(); err != nil {
			return fmt.Errorf("initialize cli data dir: %w", err)
		}
		if err := cliutil.NewSystemClient().CreateFresh(); err != nil {
			return fmt.Errorf("initialize library data dir: %w", err)
		}
		return nil
	}

	cliSt, err := cliutil.CLIStatus()
	if err != nil {
		return err
	}
	libSt, err := cliutil.NewSystemClient().DataDirStatus()
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
		// One realm fresh, the other populated — a realm went missing from an
		// otherwise-present install. Too messy to reconcile automatically.
		fresh, populated := "cli", "library"
		if libFresh {
			fresh, populated = "library", "cli"
		}
		return yoerrors.NewInconsistentDataDirError(
			"inconsistent data directory under %s: the %s realm is uninitialized but the %s realm is present; this should not happen — inspect the directory manually",
			top, fresh, populated)
	case cliSt == config.LayoutMigrate || libSt == config.LayoutMigrate:
		return yoerrors.NewMigrationRequiredError(migratingNamespace(cliSt, libSt))
	default:
		// Both realms at the current version.
		return nil
	}
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

// dirAbsentOrEmpty reports whether dir does not exist or exists but contains no
// entries — the gate's two "fresh install" cases. A non-"not exist" read error
// (e.g. TOP is a plain file) is surfaced so the gate fails loudly rather than
// treating garbage as fresh.
func dirAbsentOrEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true, nil
		}
		return false, fmt.Errorf("inspect data dir %s: %w", dir, err)
	}
	return len(entries) == 0, nil
}
