package cli

import (
	"context"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "setup",
		Short:   "Run interactive setup",
		GroupID: groupAdmin,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withManager(cmd, func(ctx context.Context, mgr *sandbox.Manager) error {
				return mgr.RunSetup(ctx)
			})
		},
	}
}
