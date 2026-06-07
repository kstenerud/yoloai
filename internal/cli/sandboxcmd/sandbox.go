// ABOUTME: `yoloai sandbox` parent command with name-first dispatch.
// ABOUTME: `list` is a real Cobra subcommand; everything else dispatched by RunE.
// ABOUTME: Subcommands: list, info, log, exec, prompt, allow, allowed, deny, bugreport, vscode, unlock, terminal-snapshot.
package sandboxcmd

import (
	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/spf13/cobra"
)

// sandboxSubcmds is the set of known sandbox subcommands dispatched by RunE.
// "list" is excluded — it's a real Cobra subcommand.
var sandboxSubcmds = map[string]bool{
	"info": true, "log": true, "exec": true, "prompt": true,
	"allow": true, "allowed": true, "deny": true, "bugreport": true,
	"vscode": true, "unlock": true, "terminal-snapshot": true,
}

func NewSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sandbox",
		Aliases: []string{"sb"},
		Short:   "Sandbox tools",
		Long:    "Sandbox tools.",
		GroupID: cliutil.GroupSandboxTools,
		Args:    cobra.ArbitraryArgs,
		RunE:    sandboxDispatch,
	}
	addLogFlags(cmd)

	listCmd := newSandboxListCmd()
	listCmd.Hidden = true
	cmd.AddCommand(listCmd)
	cmd.AddCommand(newSandboxVscodeCmd())

	cmd.SetUsageTemplate(`Usage:
  {{.CommandPath}} list [flags]
  {{.CommandPath}} <name> <subcommand> [args...]

Commands:
  list                       List sandboxes and their status
  <name> info                Show sandbox configuration and state
  <name> log [flags]         Show sandbox log
  <name> exec <command>      Run a command inside the sandbox
  <name> prompt              Show sandbox prompt
  <name> allow <domain>...   Allow additional domains (network-isolated)
  <name> allowed             Show allowed domains
  <name> deny <domain>...    Remove domains from the allowlist
  <name> bugreport [safe|unsafe]  Write a bug report for the sandbox
  <name> vscode                   Open sandbox in VS Code (attach-to-container)
  <name> unlock                   Force-clear a stale lock file (rare)
  <name> terminal-snapshot [--ansi]  Capture the agent's rendered tmux pane (DF3){{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}
`)

	return cmd
}

func sandboxDispatch(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}

	name, subcmd, rest, err := resolveSandboxDispatchArgs(args)
	if err != nil {
		return err
	}

	// Track sandbox name so the --bugreport flag defer can include sandbox sections.
	if cliutil.BugReportFile != nil {
		cliutil.BugReportSandboxName = name
	}

	return runSandboxSubcommand(cmd, subcmd, name, rest)
}

// runSandboxSubcommand dispatches resolved (subcmd, name, rest) to the
// matching handler. Split out from sandboxDispatch so the switch's
// branch count fits the cyclomatic-complexity budget for one function.
func runSandboxSubcommand(cmd *cobra.Command, subcmd, name string, rest []string) error {
	switch subcmd {
	case "info":
		return runSandboxInfo(cmd, name)
	case "log":
		// Re-inject name into args so runLog can call ResolveName internally
		return runLog(cmd, append([]string{name}, rest...))
	case "exec":
		// Re-inject name into args so runExec can call ResolveName internally
		return runExec(cmd, append([]string{name}, rest...))
	case "prompt":
		return runSandboxPrompt(cmd, name)
	case "allow":
		return runSandboxAllow(cmd, name, rest)
	case "allowed":
		return runSandboxAllowed(cmd, name)
	case "deny":
		return runSandboxDeny(cmd, name, rest)
	case "bugreport":
		reportType := "safe"
		if len(rest) > 0 {
			reportType = rest[0]
		}
		return runSandboxBugReport(cmd, name, reportType)
	case "vscode":
		return newSandboxVscodeCmd().RunE(cmd, append([]string{name}, rest...))
	case "unlock":
		return runSandboxUnlock(cmd, name)
	case "terminal-snapshot":
		return runTerminalSnapshot(cmd, name, rest)
	default:
		return yoerrors.NewUsageError("unknown subcommand %q: valid subcommands are info, log, exec, prompt, allow, allowed, deny, bugreport, vscode, unlock, terminal-snapshot", subcmd)
	}
}

// resolveSandboxDispatchArgs extracts name, subcmd, and rest args from sandbox dispatch args.
func resolveSandboxDispatchArgs(args []string) (name, subcmd string, rest []string, err error) {
	if sandboxSubcmds[args[0]] {
		// args[0] is a subcommand — name must come from YOLOAI_SANDBOX
		envName := cliutil.SandboxNameFromEnv()
		if envName == "" {
			return "", "", nil, yoerrors.NewUsageError("sandbox name required before subcommand (or set YOLOAI_SANDBOX)")
		}
		if err := cliutil.ValidateName(envName); err != nil {
			return "", "", nil, err
		}
		return envName, args[0], args[1:], nil
	}

	if err := cliutil.ValidateName(args[0]); err != nil {
		return "", "", nil, err
	}
	if len(args) < 2 {
		return "", "", nil, yoerrors.NewUsageError("subcommand required: info, log, exec, prompt, allow, allowed, deny, bugreport, vscode, unlock, terminal-snapshot")
	}
	return args[0], args[1], args[2:], nil
}
