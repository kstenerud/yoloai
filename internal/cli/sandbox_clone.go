package cli

// ABOUTME: `yoloai clone` — top-level command to clone a sandbox.
// ABOUTME: Also available as `yoloai sandbox clone` for backward compatibility.

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func runClone(cmd *cobra.Command, args []string) error {
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
}

func newCloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "clone <source> <dest>",
		Short:   "Clone a sandbox",
		GroupID: groupLifecycle,
		Args:    cobra.ExactArgs(2),
		RunE:    runClone,
	}
}

func newSandboxCloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clone <source> <dest>",
		Short: "Clone a sandbox",
		Args:  cobra.ExactArgs(2),
		RunE:  runClone,
	}
}
