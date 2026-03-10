package cli

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "attach <name>",
		Short:   "Attach to a sandbox's session (tmux)",
		GroupID: groupWorkflow,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonEnabled(cmd) {
				return errJSONNotSupported("attach")
			}

			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			resume, _ := cmd.Flags().GetBool("resume")

			backend := resolveBackendForSandbox(name)
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				info, err := sandbox.InspectSandbox(ctx, rt, name)
				if err != nil {
					return sandboxErrorHint(name, err)
				}

				containerName := sandbox.InstanceName(name)

				switch info.Status {
				case sandbox.StatusActive, sandbox.StatusIdle, sandbox.StatusDone, sandbox.StatusFailed:
					// OK — user can attach to see output

				case sandbox.StatusStopped:
					if !resume {
						return fmt.Errorf("sandbox %q: %w", name, sandbox.ErrContainerNotRunning)
					}

				default:
					return fmt.Errorf("sandbox %q: %w", name, sandbox.ErrContainerNotRunning)
				}

				// --resume: restart agent before attaching
				if resume && info.Status != sandbox.StatusActive && info.Status != sandbox.StatusIdle {
					mgr := sandbox.NewManager(rt, backend, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
					if err := mgr.Start(ctx, name, sandbox.StartOpts{Resume: true}); err != nil {
						return err
					}
					if err := waitForTmux(ctx, rt, containerName, 30*time.Second); err != nil {
						return fmt.Errorf("waiting for tmux session: %w", err)
					}
				}

				slog.Debug("attaching to tmux session", "container", containerName)
				setTerminalTitle(name)
				defer setTerminalTitle("")
				return rt.InteractiveExec(ctx, containerName, []string{"tmux", "attach", "-t", "main"}, "yoloai", "")
			})
		},
	}

	cmd.Flags().Bool("resume", false, "Restart agent with resume prompt before attaching")

	return cmd
}
