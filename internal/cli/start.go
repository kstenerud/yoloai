package cli

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "start <name>",
		Short:   "Start a stopped sandbox",
		GroupID: groupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			attach, _ := cmd.Flags().GetBool("attach")

			return withRuntime(cmd, func(ctx context.Context, rt runtime.Runtime) error {
				mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
				if err := mgr.Start(ctx, name); err != nil {
					return err
				}

				if !attach {
					return nil
				}

				containerName := sandbox.InstanceName(name)
				if err := waitForTmux(ctx, rt, containerName, 30*time.Second); err != nil {
					return fmt.Errorf("waiting for tmux session: %w", err)
				}

				return attachToSandbox(ctx, rt, containerName)
			})
		},
	}

	cmd.Flags().BoolP("attach", "a", false, "Auto-attach after starting")

	return cmd
}
