// ABOUTME: Top-level shortcut commands that delegate to longer 'sandbox <verb>'
// ABOUTME: equivalents (ls, log, exec, vscode). Kept separate so the dispatch
// ABOUTME: layer in commands.go stays compact.
package sandboxcmd

import (
	"github.com/kstenerud/yoloai/internal/cli/cliutil"
	"github.com/spf13/cobra"
)

func NewLsAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Short:   "List sandboxes (shortcut for 'sandbox list')",
		GroupID: cliutil.GroupSandboxTools,
		Args:    cobra.NoArgs,
		RunE:    runList,
	}
	addListFlags(cmd)
	return cmd
}

func NewLogAliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "log <name>",
		Short:   "Show sandbox log (shortcut for 'sandbox log')",
		GroupID: cliutil.GroupSandboxTools,
		Args:    cobra.ArbitraryArgs,
		RunE:    runLog,
	}
	addLogFlags(cmd)
	return cmd
}

func addLogFlags(cmd *cobra.Command) {
	cmd.Flags().String("source", "", "comma-separated sources: cli,sandbox,monitor,hooks")
	cmd.Flags().String("level", "info", "minimum log level: debug|info|warn|error")
	cmd.Flags().String("since", "", "show entries since duration (5m) or local time (14:20:00)")
	cmd.Flags().Bool("raw", false, "emit raw JSONL (no formatting)")
	cmd.Flags().Bool("agent", false, "show agent output (ANSI stripped)")
	cmd.Flags().Bool("agent-raw", false, "show raw agent terminal stream")
	cmd.Flags().BoolP("follow", "f", false, "tail log live; auto-exits when sandbox is done")
	cmd.MarkFlagsMutuallyExclusive("agent", "agent-raw")
	cmd.MarkFlagsMutuallyExclusive("agent", "raw")
	cmd.MarkFlagsMutuallyExclusive("agent-raw", "raw")
	cmd.MarkFlagsMutuallyExclusive("agent", "source")
	cmd.MarkFlagsMutuallyExclusive("agent", "level")
	cmd.MarkFlagsMutuallyExclusive("agent", "since")
	cmd.MarkFlagsMutuallyExclusive("agent-raw", "source")
	cmd.MarkFlagsMutuallyExclusive("agent-raw", "level")
	cmd.MarkFlagsMutuallyExclusive("agent-raw", "since")
}

func NewExecAliasCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "exec <name> <command> [args...]",
		Short:   "Run a command inside a sandbox (shortcut for 'sandbox exec')",
		GroupID: cliutil.GroupSandboxTools,
		Args:    cobra.MinimumNArgs(1),
		RunE:    runExec,
	}
}

func NewVscodeAliasCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "vscode <name>",
		Short:   "Open a sandbox in VS Code (shortcut for 'sandbox vscode')",
		GroupID: cliutil.GroupSandboxTools,
		Args:    cobra.ExactArgs(1),
		RunE:    newSandboxVscodeCmd().RunE,
	}
}
