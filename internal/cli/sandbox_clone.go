package cli

// ABOUTME: `yoloai sandbox clone` subcommand. Clones an existing sandbox
// ABOUTME: into a new stopped sandbox by deep-copying its state directory.

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newSandboxCloneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clone <source> <dest>",
		Short: "Clone a sandbox",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, dst := args[0], args[1]
			mgr := sandbox.NewManager(nil, "", slog.Default(), os.Stdin, os.Stderr)
			err := mgr.Clone(cmd.Context(), sandbox.CloneOptions{Source: src, Dest: dst})
			if err != nil {
				return err
			}

			if jsonEnabled(cmd) {
				return writeJSON(cmd.OutOrStdout(), map[string]any{
					"source": src,
					"dest":   dst,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Cloned %s → %s\n", src, dst) //nolint:errcheck
			return nil
		},
	}
	return cmd
}
