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

func newResetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "reset <name>",
		Short:   "Re-copy workdir into sandbox and reset diff baseline",
		GroupID: groupLifecycle,
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}
			noPrompt, _ := cmd.Flags().GetBool("no-prompt")
			restart, _ := cmd.Flags().GetBool("restart")
			state, _ := cmd.Flags().GetBool("state")
			keepCache, _ := cmd.Flags().GetBool("keep-cache")
			keepFiles, _ := cmd.Flags().GetBool("keep-files")
			attach, _ := cmd.Flags().GetBool("attach")
			debug, _ := cmd.Flags().GetBool("debug")

			// --state and --attach imply --restart
			if state || attach {
				restart = true
			}

			if jsonEnabled(cmd) && attach {
				return fmt.Errorf("--json and --attach are incompatible")
			}

			backend := resolveBackendForSandbox(name)
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				mgr := sandbox.NewManager(rt, backend, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
				if err := mgr.Reset(ctx, sandbox.ResetOptions{
					Name:      name,
					Restart:   restart,
					State:     state,
					KeepCache: keepCache,
					KeepFiles: keepFiles,
					NoPrompt:  noPrompt,
					Debug:     debug,
				}); err != nil {
					return sandboxErrorHint(name, err)
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
					return attachToSandbox(ctx, rt, containerName, name)
				}

				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s reset\nRun 'yoloai attach %s' to reconnect\n", name, name)
				return err
			})
		},
	}

	cmd.Flags().Bool("debug", false, "Enable debug logging in sandbox entrypoint")
	cmd.Flags().Bool("no-prompt", false, "Skip re-sending prompt after reset")
	cmd.Flags().Bool("restart", false, "Stop and restart the container")
	cmd.Flags().Bool("state", false, "Also wipe agent runtime state")
	cmd.Flags().Bool("keep-cache", false, "Preserve cache directory")
	cmd.Flags().Bool("keep-files", false, "Preserve files directory")
	cmd.Flags().BoolP("attach", "a", false, "Auto-attach after restart (implies --restart)")

	return cmd
}
