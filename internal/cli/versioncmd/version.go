// ABOUTME: 'version' command — prints the build version, commit, and date.
// ABOUTME: Supports JSON output via the global --json flag.
package versioncmd

import (
	"fmt"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/spf13/cobra"
)

func NewCmd(version, commit, date string) *cobra.Command {
	return &cobra.Command{
		Use:         "version",
		Short:       "Show version information",
		GroupID:     cliutil.GroupAdmin,
		Args:        cobra.NoArgs,
		Annotations: cliutil.SkipMigrationGateAnnotations,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cliutil.JSONEnabled(cmd) {
				return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{
					"version": version,
					"commit":  commit,
					"date":    date,
				})
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "yoloai version %s (commit: %s, built: %s)\n", version, commit, date)
			return err
		},
	}
}
