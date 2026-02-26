package cli

import (
	"context"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "setup",
		Short:   "Run interactive setup",
		GroupID: groupAdmin,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			backend := resolveBackend(cmd)
			return withManager(cmd, backend, func(ctx context.Context, mgr *sandbox.Manager) error {
				return mgr.RunSetup(ctx)
			})
		},
	}

	cmd.Flags().String("backend", "", "Runtime backend (docker, tart)")

	return cmd
}
