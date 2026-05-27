// ABOUTME: 'version' command — prints the build version, commit, and date.
// ABOUTME: Supports JSON output via the global --json flag.
package cli

import (
	"fmt"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/spf13/cobra"
)

func newVersionCmd(version, commit, date string) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Show version information",
		GroupID: groupAdmin,
		Args:    cobra.NoArgs,
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
