package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "attach <name>",
		Short:   "Attach to a sandbox's tmux session",
		GroupID: groupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			return withRuntime(cmd, func(ctx context.Context, rt runtime.Runtime) error {
				info, err := sandbox.InspectSandbox(ctx, rt, name)
				if err != nil {
					return err
				}

				switch info.Status {
				case sandbox.StatusRunning, sandbox.StatusDone, sandbox.StatusFailed:
					// OK — user can attach to see output
				default:
					return fmt.Errorf("sandbox %q: %w", name, sandbox.ErrContainerNotRunning)
				}

				containerName := sandbox.ContainerName(name)
				slog.Debug("attaching to tmux session", "container", containerName)

				return rt.InteractiveExec(ctx, containerName, []string{"tmux", "attach", "-t", "main"}, "yoloai")
			})
		},
	}
}
