// ABOUTME: CLI command to restart a sandbox (stop + start).
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

func newRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "restart <name>",
		Short:   "Restart the agent in an existing sandbox",
		GroupID: groupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			attach, _ := cmd.Flags().GetBool("attach")

			if jsonEnabled(cmd) && attach {
				return fmt.Errorf("--json and --attach are incompatible")
			}

			backend := resolveBackendForSandbox(name)
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				mgr := sandbox.NewManager(rt, backend, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())

				if err := mgr.Stop(ctx, name); err != nil {
					return err
				}
				if err := mgr.Start(ctx, name); err != nil {
					return err
				}

				if jsonEnabled(cmd) {
					return writeJSON(cmd.OutOrStdout(), map[string]string{
						"name":   name,
						"action": "restarted",
					})
				}

				if attach {
					containerName := sandbox.InstanceName(name)
					if err := waitForTmux(ctx, rt, containerName, 30*time.Second); err != nil {
						return fmt.Errorf("waiting for tmux session: %w", err)
					}
					return attachToSandbox(ctx, rt, containerName)
				}

				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s restarted\nRun 'yoloai attach %s' to reconnect\n", name, name)
				return err
			})
		},
	}

	cmd.Flags().BoolP("attach", "a", false, "Auto-attach after restart")

	return cmd
}
