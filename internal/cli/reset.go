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

func newResetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "reset <name>",
		Short:   "Re-copy workdir and reset git baseline",
		GroupID: groupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}
			noPrompt, _ := cmd.Flags().GetBool("no-prompt")
			clean, _ := cmd.Flags().GetBool("clean")
			noRestart, _ := cmd.Flags().GetBool("no-restart")
			attach, _ := cmd.Flags().GetBool("attach")
			debug, _ := cmd.Flags().GetBool("debug")

			if clean && noRestart {
				return sandbox.NewUsageError("cannot wipe agent state while agent is running; use --clean without --no-restart, or stop the agent first")
			}
			if attach && noRestart {
				return sandbox.NewUsageError("--attach and --no-restart are incompatible; the agent is still running so just reattach")
			}

			backend := resolveBackendForSandbox(name)
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				mgr := sandbox.NewManager(rt, backend, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
				if err := mgr.Reset(ctx, sandbox.ResetOptions{
					Name:      name,
					Clean:     clean,
					NoPrompt:  noPrompt,
					NoRestart: noRestart,
					Debug:     debug,
				}); err != nil {
					return err
				}

				if jsonEnabled(cmd) {
					return writeJSON(cmd.OutOrStdout(), map[string]string{
						"name":   name,
						"action": "reset",
					})
				}

				if attach {
					containerName := sandbox.InstanceName(name)
					if err := waitForTmux(ctx, rt, containerName, 30*time.Second); err != nil {
						return fmt.Errorf("waiting for tmux session: %w", err)
					}
					return attachToSandbox(ctx, rt, containerName)
				}

				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s reset\nRun 'yoloai attach %s' to reconnect\n", name, name)
				return err
			})
		},
	}

	cmd.Flags().Bool("no-prompt", false, "Skip re-sending prompt after reset")
	cmd.Flags().Bool("clean", false, "Also wipe agent-state directory")
	cmd.Flags().Bool("no-restart", false, "Keep agent running, reset workspace in-place")
	cmd.Flags().BoolP("attach", "a", false, "Auto-attach after reset")

	return cmd
}
