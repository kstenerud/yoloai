// Package cli defines the Cobra command tree for the yoloAI CLI.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// Execute runs the root command and returns the exit code.
func Execute(ctx context.Context, version, commit, date string) int {
	rootCmd := newRootCmd(version, commit, date)

	err := rootCmd.ExecuteContext(ctx)
	if err == nil {
		return 0
	}

	fmt.Fprintf(os.Stderr, "yoloai: %s\n", err) //nolint:errcheck // best-effort stderr write

	var usageErr *sandbox.UsageError
	if errors.As(err, &usageErr) {
		return 2
	}

	var configErr *sandbox.ConfigError
	if errors.As(err, &configErr) {
		return 3
	}

	return 1
}

// newRootCmd creates the root Cobra command with all subcommands registered.
func newRootCmd(version, commit, date string) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "yoloai",
		Short: "Sandboxed AI coding agent runner",
		Long: `Run AI coding agents in full-auto mode, safely. Agents run with
safety checks disabled inside disposable sandboxes — they work fast
and unattended while your originals stay protected. When done, review the
diff and apply what you want to keep.`,
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	rootCmd.PersistentFlags().CountP("verbose", "v", "Increase output verbosity (-v for debug, -vv reserved)")
	rootCmd.PersistentFlags().CountP("quiet", "q", "Suppress non-essential output (-q for warn, -qq for error only)")
	rootCmd.PersistentFlags().Bool("no-color", false, "Disable colored output")

	registerCommands(rootCmd, version, commit, date)

	return rootCmd
}
