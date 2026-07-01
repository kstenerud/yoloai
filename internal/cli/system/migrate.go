package system

// ABOUTME: `yoloai system migrate` — the only place that mutates an existing
// ABOUTME: data dir, bringing both the CLI and library realms to the current layout.

import (
	"fmt"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/spf13/cobra"
)

func newSystemMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate the data directory to the current on-disk layout",
		Long: `Bring the yoloai data directory up to the layout this build expects.

The normal startup path never migrates: it fails fast and points here when the
data directory is out of date. This command is the only one that mutates an
existing data directory.

It is idempotent — running it on an already-current directory is a no-op — and
safe to re-run after a partial failure (a realm already at the current version
is skipped). Interrupted at any point, a re-run resumes cleanly.

Use --check for a read-only pre-upgrade audit (writes nothing). A migration that
would discard uncommitted work (e.g. a stopped overlay sandbox) refuses unless
you additionally pass --abandon-stopped-overlay; --yes alone never destroys work.`,
		Args:        cobra.NoArgs,
		Annotations: cliutil.SkipMigrationGateAnnotations,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSystemMigrate(cmd)
		},
	}
	cmd.Flags().Bool("check", false, "Show what would be migrated and exit, writing nothing")
	cmd.Flags().Bool("dry-run", false, "Preview the migration without applying it")
	cmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt for destructive operations")
	cmd.Flags().Bool("abandon-stopped-overlay", false, "Authorize abandoning uncommitted changes in stopped overlay sandboxes")
	cmd.MarkFlagsMutuallyExclusive("check", "dry-run")
	return cmd
}

func runSystemMigrate(cmd *cobra.Command) error {
	sys, err := cliutil.System()
	if err != nil {
		return err
	}
	opts := planApplyOpts{
		sys:            sys,
		yes:            cliutil.EffectiveYes(cmd),
		abandonOverlay: flagBool(cmd, "abandon-stopped-overlay"),
		json:           cliutil.JSONEnabled(cmd),
		in:             cmd.InOrStdin(),
		out:            cmd.OutOrStdout(),
		errw:           cmd.ErrOrStderr(),
	}

	cliSt, err := cliutil.CLIStatus()
	if err != nil {
		return err
	}
	libSt, err := sys.DataDirStatus()
	if err != nil {
		return err
	}

	// Read-only preview: report realm status + the framework plan, mutate nothing.
	if flagBool(cmd, "check") || flagBool(cmd, "dry-run") {
		return previewMigration(cmd.Context(), opts, cliSt, libSt)
	}

	// Both realms current: the library stamp reaches LibrarySchemaVersion only
	// after the framework flatten stamps last, so LayoutOK here means there is no
	// framework work left either — a true no-op.
	if cliSt == config.LayoutOK && libSt == config.LayoutOK {
		return reportMigrateNoop(cmd)
	}

	// Frozen v0->v3 ladder, then the crash-safe framework (v3->v4 overlay flatten),
	// plan/apply-gated, which stamps the realm to v4 last.
	if err := applyFrozenLadder(cmd, sys); err != nil {
		return err
	}
	report, err := runPlanApply(cmd.Context(), opts)
	if err != nil {
		return err
	}
	if err := renderReport(opts, report); err != nil {
		return err
	}
	return reportMigrateOK(cmd)
}

// applyFrozenLadder runs the sealed v0->v3 migration: the CLI flat->namespaced
// relocation, then the library realm (create-fresh or migrate). On a v0 flat
// install the relocation lifts library-owned content up into TOP/library, which
// the library step then stamps; it refuses to touch an unrecognized directory
// rather than mangling it.
func applyFrozenLadder(cmd *cobra.Command, sys *yoloai.System) error {
	if err := cliutil.MigrateCLI(); err != nil {
		return err
	}
	// Re-read library status: the relocation may have created TOP/library. A
	// relocated-but-unstamped dir reads as Migrate; a genuinely empty dir reads
	// as Fresh and is create-freshed.
	libSt, err := sys.DataDirStatus()
	if err != nil {
		return err
	}
	switch libSt {
	case config.LayoutFresh:
		return sys.CreateDataDir()
	case config.LayoutMigrate:
		return sys.MigrateDataDir(cmd.Context())
	}
	return nil
}

// flagBool reads a bool flag, defaulting to false if absent.
func flagBool(cmd *cobra.Command, name string) bool {
	v, _ := cmd.Flags().GetBool(name)
	return v
}

// reportMigrateNoop reports that the data dir was already current.
func reportMigrateNoop(cmd *cobra.Command) error {
	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{"action": "already-current"})
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), "Data directory is already up to date.")
	return err
}

// reportMigrateOK reports a completed migration.
func reportMigrateOK(cmd *cobra.Command) error {
	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{"action": "migrated"})
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), "Data directory migrated successfully.")
	return err
}
