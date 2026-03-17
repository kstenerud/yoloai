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
			defer openCLIJSONLSink(name, cmd)()
			noPrompt, _ := cmd.Flags().GetBool("no-prompt")
			restart, _ := cmd.Flags().GetBool("restart")
			clearState, _ := cmd.Flags().GetBool("clear-state")
			keepCache, _ := cmd.Flags().GetBool("keep-cache")
			keepFiles, _ := cmd.Flags().GetBool("keep-files")
			attach, _ := cmd.Flags().GetBool("attach")
			debug, _ := cmd.Flags().GetBool("debug")

			// --clear-state and --attach imply --restart
			if clearState || attach {
				restart = true
			}

			if jsonEnabled(cmd) && attach {
				return fmt.Errorf("--json and --attach are incompatible")
			}

			backend := resolveBackendForSandbox(name)
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
				slog.Info("resetting sandbox", "event", "sandbox.reset", "sandbox", name, "restart", restart, "clear_state", clearState) //nolint:gosec // G706: name is validated by ValidateName
				if err := mgr.Reset(ctx, sandbox.ResetOptions{
					Name:       name,
					Restart:    restart,
					ClearState: clearState,
					KeepCache:  keepCache,
					KeepFiles:  keepFiles,
					NoPrompt:   noPrompt,
					Debug:      debug,
				}); err != nil {
					return sandboxErrorHint(name, err)
				}
				slog.Info("sandbox reset complete", "event", "sandbox.reset.complete", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName

				if jsonEnabled(cmd) {
					return writeJSON(cmd.OutOrStdout(), map[string]string{
						"name":   name,
						"action": "reset",
					})
				}

				if attach {
					meta, err := sandbox.LoadMeta(sandbox.Dir(name))
					if err != nil {
						return err
					}
					user := tmuxExecUser(meta)
					containerName := sandbox.InstanceName(name)
					if err := waitForTmux(ctx, rt, containerName, name, 30*time.Second, user); err != nil {
						return fmt.Errorf("waiting for tmux session: %w", err)
					}
					return attachToSandbox(ctx, rt, containerName, name, user)
				}

				if restart {
					_, err = fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s reset\nRun 'yoloai attach %s' to reconnect\n", name, name)
				} else {
					_, err = fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s reset\n", name)
				}
				return err
			})
		},
	}

	cmd.Flags().Bool("no-prompt", false, "Skip re-sending prompt after reset")
	cmd.Flags().Bool("restart", false, "Stop and restart the container")
	cmd.Flags().Bool("clear-state", false, "Wipe agent runtime state (implies --restart)")
	cmd.Flags().Bool("keep-cache", false, "Preserve cache directory")
	cmd.Flags().Bool("keep-files", false, "Preserve files directory")
	cmd.Flags().BoolP("attach", "a", false, "Auto-attach after restart (implies --restart)")

	return cmd
}
