// ABOUTME: Execute() entry point and root Cobra command wiring, including
// ABOUTME: exit-code mapping, bug-report lifecycle, and command group registration.
// Package cli defines the Cobra command tree for the yoloAI CLI.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/cli/bugreport"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/kstenerud/yoloai/internal/cli/extension"
	"github.com/kstenerud/yoloai/internal/cli/sandboxcmd"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// exitCodeSIGINT is the conventional exit code for SIGINT (128 + signal 2).
const exitCodeSIGINT = 130

// Execute runs the root command and returns the exit code.
func Execute(ctx context.Context, version, commit, date string) (exitCode int) {
	cliutil.SetBuildInfo(version, commit, date)
	rootCmd := NewRootCmd(version, commit, date)

	// Track which command was active when the error occurred so we can
	// show a context-aware help hint (e.g. "Run 'yoloai system prune -h' for help").
	var activeCmd *cobra.Command
	// NewRootCmd installs a PersistentPreRunE that applies --data-dir and runs
	// the startup migration gate. Cobra runs only the most specific
	// PersistentPreRunE, so preserve that one and run it first — both InitLogger
	// and initBugReport touch the data dir, so they must come after the layout
	// is resolved and the gate has created/validated it.
	bootstrap := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		activeCmd = cmd
		if bootstrap != nil {
			if err := bootstrap(cmd, args); err != nil {
				return err
			}
		}
		cliutil.InitLogger(cmd)
		return initBugReport(cmd, version, commit, date)
	}
	prev := rootCmd.FlagErrorFunc()
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		activeCmd = cmd
		if prev != nil {
			return prev(cmd, err)
		}
		return err
	})

	// Deferred bug report finalizer: writes sections 13-14 and renames temp → final.
	var runErr error
	var panicked bool
	defer func() {
		r := recover()
		if r != nil {
			panicked = true
		}
		finalizeBugReport(exitCode, runErr, panicked)
		if panicked {
			panic(r)
		}
	}()

	runErr = rootCmd.ExecuteContext(ctx)
	if runErr == nil {
		return 0
	}

	if errors.Is(runErr, context.Canceled) {
		return exitCodeSIGINT
	}

	printRunError(runErr, rootCmd, activeCmd)
	return errorExitCode(runErr)
}

// printRunError writes the run error to stderr in JSON or human-readable form.
func printRunError(runErr error, rootCmd *cobra.Command, activeCmd *cobra.Command) {
	if jsonFlag, _ := rootCmd.PersistentFlags().GetBool("json"); jsonFlag {
		cliutil.WriteJSONError(os.Stderr, runErr)
		return
	}
	fmt.Fprintf(os.Stderr, "yoloai: %s\n", runErr)

	// If this is a raw ENOSPC that wasn't wrapped at the call site, the
	// user just saw the underlying error without the recovery hint that
	// *DiskSpaceError carries. Append the same hint here so the message
	// is actionable regardless of whether the runtime layer wrapped.
	if _, alreadyWrapped := errors.AsType[*yoerrors.DiskSpaceError](runErr); !alreadyWrapped && yoerrors.IsDiskSpaceError(runErr) {
		fmt.Fprintln(os.Stderr, "Free space and retry:")
		fmt.Fprintln(os.Stderr, "  yoloai system disk             # show what yoloai is using")
		fmt.Fprintln(os.Stderr, "  yoloai system prune            # reclaim cache, no rebuild")
		fmt.Fprintln(os.Stderr, "  yoloai system prune --images   # also remove base images (forces rebuild)")
	}

	if activeCmd != nil {
		fmt.Fprintf(os.Stderr, "Run '%s -h' for help\n", activeCmd.CommandPath())
	}
}

// initBugReport handles --bugreport flag setup during PersistentPreRunE.
func initBugReport(cmd *cobra.Command, version, commit, date string) error {
	brType, _ := cmd.Root().PersistentFlags().GetString("bugreport")
	if brType == "" {
		return nil
	}
	if brType != "safe" && brType != "unsafe" {
		return yoerrors.NewUsageError("--bugreport: must be safe or unsafe")
	}
	name, err := bugreport.Filename(time.Now().UTC())
	if err != nil {
		return fmt.Errorf("--bugreport: %w", err)
	}
	cliutil.BugReportFinalName = name
	cliutil.BugReportType = brType
	f, err := fileutil.OpenFile(name+".tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("--bugreport: open temp file: %w", err)
	}
	cliutil.BugReportFile = f
	bugreport.WriteHeader(f, version, commit, date, brType)
	bugreport.WriteCommandInvocation(f, brType)
	sys, err := cliutil.System()
	if err != nil {
		return err
	}
	bugreport.WriteDiagnostics(f, sys.Diagnostics(cmd.Context()), brType)
	cliutil.AddLogSink(&cliutil.LiveLogBuf, slog.LevelDebug)
	return nil
}

// finalizeBugReport writes the bug report footer and renames the temp file if one is open.
func finalizeBugReport(exitCode int, runErr error, panicked bool) {
	if cliutil.BugReportFile == nil {
		return
	}
	code := exitCode
	if panicked {
		code = 1
	}
	if cliutil.BugReportSandboxName != "" {
		sandboxcmd.WriteSandboxSectionsForFlag(cliutil.BugReportFile, cliutil.BugReportSandboxName, cliutil.BugReportType)
	}
	bugreport.WriteLiveLog(cliutil.BugReportFile, cliutil.LiveLogBuf.Bytes(), cliutil.BugReportType)
	bugreport.WriteExit(cliutil.BugReportFile, code, runErr, panicked)
	_ = cliutil.BugReportFile.Close()
	_ = os.Rename(cliutil.BugReportFinalName+".tmp", cliutil.BugReportFinalName)
	if info, err := os.Stat(cliutil.BugReportFinalName); err == nil && info.Size() > 65536 {
		fmt.Fprintf(os.Stderr, "Warning: report exceeds GitHub's issue body limit (65,536 characters).\n")
		fmt.Fprintf(os.Stderr, "Upload as a Gist instead: gh gist create %s\n", cliutil.BugReportFinalName)
	}
	fmt.Fprintf(os.Stderr, "Bug report written: %s\n", cliutil.BugReportFinalName)
}

// errorExitCode maps an error to the appropriate exit code.
//
// The typed yoerrors taxonomy carries its own exit code via the
// ExitCoder interface (F16): a single errors.AsType[ExitCoder] match
// replaces the former per-type cascade, so adding a new typed error
// with an ExitCode method participates automatically. extension.ExitError
// is checked first (it carries an arbitrary child-process code, not one
// of our fixed codes), and the disk-space string sniff stays as a
// fallback for unwrapped ENOSPC that flowed up from a backend without
// typing.
func errorExitCode(err error) int {
	if exitErr, ok := errors.AsType[*extension.ExitError](err); ok {
		return exitErr.Code
	}

	if coder, ok := errors.AsType[yoerrors.ExitCoder](err); ok {
		return coder.ExitCode()
	}

	if yoerrors.IsDiskSpaceError(err) {
		return yoerrors.ExitDiskSpace
	}

	return 1
}

// NewRootCmd creates the root Cobra command with all subcommands registered.
func NewRootCmd(version, commit, date string) *cobra.Command {
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
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true, Use: "no-help", GroupID: cliutil.GroupAdmin})

	// Register --help/-h as persistent so it appears under "Global Flags"
	// on every command. Cobra's InitDefaultHelpFlag skips adding a local
	// --help when one already exists via persistent inheritance.
	rootCmd.PersistentFlags().BoolP("help", "h", false, "Help for this command")

	rootCmd.PersistentFlags().CountP("verbose", "v", "Increase output verbosity (-v for debug, -vv reserved)")
	rootCmd.PersistentFlags().CountP("quiet", "q", "Suppress non-essential output (-q for error only)")
	rootCmd.PersistentFlags().Bool("json", false, "Output as JSON (machine-readable)")
	rootCmd.PersistentFlags().Bool("debug", false, "Enable debug-level entries in cli.jsonl")
	rootCmd.PersistentFlags().String("bugreport", "", "Write bug report (safe|unsafe)")
	rootCmd.PersistentFlags().String("data-dir", "", "Override the yoloai data directory (default: $HOME/.yoloai/). HTTP/MCP/daemon/test embedders pass explicit paths; see development-principles.md §12.")

	// Persistent pre-run: record the process-wide rootLayout from the
	// --data-dir flag (defaulting to $HOME/.yoloai when empty) before any
	// command handler — or the migration gate below — reads it. The HOME
	// read goes through the single allowlisted site (cliutil.resolveHome).
	prevPersistentPreRunE := rootCmd.PersistentPreRunE
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		dataDir, _ := cmd.Flags().GetString("data-dir")
		cliutil.SetRootLayoutFromFlag(dataDir)
		// Run the read-only migration gate before any command touches the data
		// dir: it create-freshes a genuinely new install, fails fast telling
		// the user to run `yoloai system migrate` when the dir is out of date,
		// or proceeds. It never migrates silently — that lives in the explicit
		// migrate command. Exempt commands (version, help, completion, the
		// migrate command itself) skip it.
		if err := runMigrationGate(cmd); err != nil {
			return err
		}
		// Register file-defined agents (~/.yoloai/agents/*.yaml) so every command
		// — including the static AgentTypes() catalog (`system agents`), which
		// never constructs a Client — sees them. Idempotent with NewClient's own
		// registration for library embedders.
		if err := agent.RegisterFileAgents(cliutil.Layout().AgentsDir()); err != nil {
			return err
		}
		if prevPersistentPreRunE != nil {
			return prevPersistentPreRunE(cmd, args)
		}
		return nil
	}

	// Establish the default root Layout before building the command tree.
	// Dynamic `yoloai x` extension subcommands are registered now, at
	// construction time — before flag parsing — and read CLIExtensionsDir(),
	// so a Layout must already exist. --data-dir cannot influence which
	// extensions load (it isn't parsed yet); the PersistentPreRunE above
	// re-applies the flag for every other handler.
	cliutil.SetRootLayoutFromFlag("")

	registerCommands(rootCmd, version, commit, date)

	return rootCmd
}
