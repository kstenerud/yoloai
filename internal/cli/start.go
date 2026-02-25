package cli

import (
	"context"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "start <name>",
		Short:   "Start a stopped sandbox",
		GroupID: groupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			return withManager(cmd, func(ctx context.Context, mgr *sandbox.Manager) error {
				return mgr.Start(ctx, name)
			})
		},
	}
}
