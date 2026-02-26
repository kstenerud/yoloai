package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "setup",
		Short:   "Run interactive setup",
		GroupID: groupAdmin,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.ErrOrStderr(), "Interactive setup command is not yet implemented.")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.ErrOrStderr(), "To change settings, edit ~/.yoloai/config.yaml directly.")
			return err
		},
	}
}
