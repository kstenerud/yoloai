package cli

import (
	"fmt"
	"log/slog"

	"github.com/kstenerud/yoloai/internal/docker"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newResetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset <name>",
		Short: "Re-copy workdir and reset git baseline",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}
			noPrompt, _ := cmd.Flags().GetBool("no-prompt")
			clean, _ := cmd.Flags().GetBool("clean")

			ctx := cmd.Context()
			client, err := docker.NewClient(ctx)
			if err != nil {
				return err
			}
			defer client.Close() //nolint:errcheck // best-effort cleanup

			mgr := sandbox.NewManager(client, slog.Default(), cmd.ErrOrStderr())

			if err := mgr.Reset(ctx, sandbox.ResetOptions{
				Name:     name,
				Clean:    clean,
				NoPrompt: noPrompt,
			}); err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Sandbox %s reset\n", name)
			return err
		},
	}

	cmd.Flags().Bool("no-prompt", false, "Skip re-sending prompt after reset")
	cmd.Flags().Bool("clean", false, "Also wipe agent-state directory")

	return cmd
}
