package cli

import (
	"context"
	"fmt"

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

			if clean && noRestart {
				return sandbox.NewUsageError("cannot wipe agent state while agent is running; use --clean without --no-restart, or stop the agent first")
			}

			backend := resolveBackendForSandbox(name)
			return withManager(cmd, backend, func(ctx context.Context, mgr *sandbox.Manager) error {
				if err := mgr.Reset(ctx, sandbox.ResetOptions{
					Name:      name,
					Clean:     clean,
					NoPrompt:  noPrompt,
					NoRestart: noRestart,
				}); err != nil {
					return err
				}

				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s reset\n", name)
				return err
			})
		},
	}

	cmd.Flags().Bool("no-prompt", false, "Skip re-sending prompt after reset")
	cmd.Flags().Bool("clean", false, "Also wipe agent-state directory")
	cmd.Flags().Bool("no-restart", false, "Keep agent running, reset workspace in-place")

	return cmd
}
