// Package cli defines the Cobra command tree for the yoloai CLI.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// UsageError indicates bad arguments or missing required args (exit code 2).
type UsageError struct {
	Err error
}

func (err *UsageError) Error() string { return err.Err.Error() }
func (err *UsageError) Unwrap() error { return err.Err }

// ConfigError indicates a configuration problem (exit code 3).
type ConfigError struct {
	Err error
}

func (err *ConfigError) Error() string { return err.Err.Error() }
func (err *ConfigError) Unwrap() error { return err.Err }

// Execute runs the root command and returns the exit code.
func Execute(ctx context.Context, version, commit, date string) int {
	rootCmd := newRootCmd(version, commit, date)

	err := rootCmd.ExecuteContext(ctx)
	if err == nil {
		return 0
	}

	fmt.Fprintf(os.Stderr, "yoloai: %s\n", err) //nolint:errcheck // best-effort stderr write

	var usageErr *UsageError
	if errors.As(err, &usageErr) {
		return 2
	}

	var configErr *ConfigError
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
		Long: `Run AI coding CLI agents inside disposable Docker containers with
copy/diff/apply workflow. Your originals are protected — the agent works on
an isolated copy, you review what changed, and choose what to keep.`,
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	rootCmd.PersistentFlags().CountP("verbose", "v", "Increase output verbosity (-v for debug, -vv reserved)")
	rootCmd.PersistentFlags().CountP("quiet", "q", "Suppress non-essential output (-q for warn, -qq for error only)")
	rootCmd.PersistentFlags().Bool("no-color", false, "Disable colored output")

	registerCommands(rootCmd, version, commit, date)

	return rootCmd
}
