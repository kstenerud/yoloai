package system

// ABOUTME: `yoloai system migrate` — the only place that mutates an existing
// ABOUTME: data dir, bringing both the CLI and library realms to the current layout.

import (
	"fmt"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/spf13/cobra"
)

func newSystemMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Migrate the data directory to the current on-disk layout",
		Long: `Bring the yoloai data directory up to the layout this build expects.

The normal startup path never migrates: it fails fast and points here when the
data directory is out of date. This command is the only one that mutates an
existing data directory.

It is idempotent — running it on an already-current directory is a no-op — and
safe to re-run after a partial failure (a realm already at the current version
is skipped).`,
		Args:        cobra.NoArgs,
		Annotations: cliutil.SkipMigrationGateAnnotations,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSystemMigrate(cmd)
		},
	}
}

func runSystemMigrate(cmd *cobra.Command) error {
	sys := cliutil.NewSystemClient()

	cliSt, err := cliutil.CLIStatus()
	if err != nil {
		return err
	}
	libSt, err := sys.DataDirStatus()
	if err != nil {
		return err
	}
	if cliSt == config.LayoutOK && libSt == config.LayoutOK {
		return reportMigrateNoop(cmd)
	}

	// CLI realm first: on a v0 flat install this performs the flat -> namespaced
	// relocation that lifts library-owned content up into TOP/library, which the
	// library realm step below then stamps. It refuses to touch an unrecognized
	// directory rather than mangling it.
	if err := cliutil.MigrateCLI(); err != nil {
		return err
	}

	// Library realm: re-read its status (the relocation above may have created
	// TOP/library) and bring it current. A relocated-but-unstamped dir reads as
	// Migrate; a genuinely empty dir reads as Fresh and is create-freshed.
	libSt, err = sys.DataDirStatus()
	if err != nil {
		return err
	}
	switch libSt {
	case config.LayoutFresh:
		if err := sys.CreateFresh(); err != nil {
			return err
		}
	case config.LayoutMigrate:
		if err := sys.Migrate(cmd.Context()); err != nil {
			return err
		}
	}

	return reportMigrateOK(cmd)
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
