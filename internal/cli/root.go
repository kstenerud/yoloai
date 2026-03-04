// Package cli defines the Cobra command tree for the yoloAI CLI.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/extension"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

// exitCodeSIGINT is the conventional exit code for SIGINT (128 + signal 2).
const exitCodeSIGINT = 130

// Execute runs the root command and returns the exit code.
func Execute(ctx context.Context, version, commit, date string) int {
	rootCmd := newRootCmd(version, commit, date)

	// Track which command was active when the error occurred so we can
	// show a context-aware help hint (e.g. "Run 'yoloai system prune -h' for help").
	var activeCmd *cobra.Command
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, _ []string) { activeCmd = cmd }
	prev := rootCmd.FlagErrorFunc()
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		activeCmd = cmd
		if prev != nil {
			return prev(cmd, err)
		}
		return err
	})

	err := rootCmd.ExecuteContext(ctx)
	if err == nil {
		return 0
	}

	if errors.Is(err, context.Canceled) {
		return exitCodeSIGINT
	}

	// In JSON mode, errors go to stderr as JSON
	if jsonFlag, _ := rootCmd.PersistentFlags().GetBool("json"); jsonFlag {
		writeJSONError(os.Stderr, err)
	} else {
		fmt.Fprintf(os.Stderr, "yoloai: %s\n", err) //nolint:errcheck // best-effort stderr write
		if activeCmd != nil {
			fmt.Fprintf(os.Stderr, "Run '%s -h' for help\n", activeCmd.CommandPath()) //nolint:errcheck // best-effort stderr write
		}
	}

	var exitErr *extension.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}

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
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			fmt.Fprintln(w, "yoloai — sandboxed AI coding agent runner") //nolint:errcheck // best-effort stdout write
			fmt.Fprintln(w)                                              //nolint:errcheck // best-effort stdout write
			fmt.Fprintln(w, "Run 'yoloai help' to get started")          //nolint:errcheck // best-effort stdout write
			fmt.Fprintln(w, "Run 'yoloai -h' for command-line options")  //nolint:errcheck // best-effort stdout write
		},
	}

	// Disable Cobra's built-in help subcommand; we register our own.
	// GroupID prevents an empty "Additional Commands:" header in -h output.
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true, Use: "no-help", GroupID: groupAdmin})

	// Register --help/-h as persistent so it appears under "Global Flags"
	// on every command. Cobra's InitDefaultHelpFlag skips adding a local
	// --help when one already exists via persistent inheritance.
	rootCmd.PersistentFlags().BoolP("help", "h", false, "Help for this command")

	rootCmd.PersistentFlags().CountP("verbose", "v", "Increase output verbosity (-v for debug, -vv reserved)")
	rootCmd.PersistentFlags().CountP("quiet", "q", "Suppress non-essential output (-q for warn, -qq for error only)")
	rootCmd.PersistentFlags().Bool("json", false, "Output as JSON (machine-readable)")

	registerCommands(rootCmd, version, commit, date)

	return rootCmd
}
