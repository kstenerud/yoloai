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
			defer openCLIJSONLSink(name, cmd)()

			attach, _ := cmd.Flags().GetBool("attach")
			resume, _ := cmd.Flags().GetBool("resume")
			prompt, _ := cmd.Flags().GetString("prompt")
			promptFile, _ := cmd.Flags().GetString("prompt-file")

			if jsonEnabled(cmd) && attach {
				return fmt.Errorf("--json and --attach are incompatible")
			}

			// Set terminal title early so it shows the sandbox name during start
			if attach {
				setTerminalTitle(name)
				defer setTerminalTitle("")
			}

			slog.Info("starting sandbox", "event", "sandbox.start", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName
			backend := resolveBackendForSandbox(name)
			return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
				mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
				if err := mgr.Start(ctx, name, sandbox.StartOptions{
					Resume:     resume,
					Prompt:     prompt,
					PromptFile: promptFile,
				}); err != nil {
					return sandboxErrorHint(name, err)
				}
				slog.Info("sandbox started", "event", "sandbox.start.complete", "sandbox", name) //nolint:gosec // G706: name is validated by ValidateName

				if jsonEnabled(cmd) {
					return writeJSON(cmd.OutOrStdout(), map[string]string{
						"name":   name,
						"action": "started",
					})
				}

				if !attach {
					return nil
				}

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
			})
		},
	}

	cmd.Flags().BoolP("attach", "a", false, "Auto-attach after starting")
	cmd.Flags().Bool("resume", false, "Re-feed original prompt with continuation preamble")
	cmd.Flags().StringP("prompt", "p", "", "New prompt text (overwrites existing prompt)")
	cmd.Flags().StringP("prompt-file", "f", "", "File containing new prompt")

	cmd.MarkFlagsMutuallyExclusive("resume", "prompt")
	cmd.MarkFlagsMutuallyExclusive("resume", "prompt-file")
	cmd.MarkFlagsMutuallyExclusive("prompt", "prompt-file")

	return cmd
}
