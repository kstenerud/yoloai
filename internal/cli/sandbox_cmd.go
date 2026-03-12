// ABOUTME: `yoloai sandbox` parent command with name-first dispatch.
// ABOUTME: `list` is a real Cobra subcommand; everything else dispatched by RunE.
// ABOUTME: Subcommands: list, info, log, exec, prompt, allow, allowed, deny.
package cli

import (
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

// sandboxSubcmds is the set of known sandbox subcommands dispatched by RunE.
// "list" is excluded — it's a real Cobra subcommand.
var sandboxSubcmds = map[string]bool{
	"info": true, "log": true, "exec": true, "prompt": true,
	"allow": true, "allowed": true, "deny": true,
}

func newSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sandbox",
		Aliases: []string{"sb"},
		Short:   "Sandbox tools",
		Long: `Sandbox tools.

Subcommands:
  list                       List sandboxes and their status
  <name> info                Show sandbox configuration and state
  <name> log [--raw]         Show sandbox session log
  <name> exec <command>      Run a command inside the sandbox
  <name> prompt              Show sandbox prompt
  <name> allow <domain>...   Allow additional domains (network-isolated)
  <name> allowed             Show allowed domains
  <name> deny <domain>...    Remove domains from the allowlist`,
		GroupID: groupSandboxTools,
		Args:    cobra.ArbitraryArgs,
		RunE:    sandboxDispatch,
	}
	cmd.Flags().Bool("raw", false, "Show raw output with ANSI escape sequences")

	cmd.AddCommand(newSandboxListCmd())

	return cmd
}

func sandboxDispatch(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}

	var name, subcmd string
	var rest []string

	if sandboxSubcmds[args[0]] {
		// args[0] is a subcommand — name must come from YOLOAI_SANDBOX
		envName := os.Getenv(EnvSandboxName)
		if envName == "" {
			return sandbox.NewUsageError("sandbox name required before subcommand (or set YOLOAI_SANDBOX)")
		}
		if err := sandbox.ValidateName(envName); err != nil {
			return err
		}
		name = envName
		subcmd = args[0]
		rest = args[1:]
	} else {
		if err := sandbox.ValidateName(args[0]); err != nil {
			return err
		}
		name = args[0]
		if len(args) < 2 {
			return sandbox.NewUsageError("subcommand required: info, log, exec, prompt, allow, allowed, deny")
		}
		subcmd = args[1]
		rest = args[2:]
	}

	switch subcmd {
	case "info":
		return runSandboxInfo(cmd, name)
	case "log":
		// Re-inject name into args so runLog can call resolveName internally
		return runLog(cmd, append([]string{name}, rest...))
	case "exec":
		// Re-inject name into args so runExec can call resolveName internally
		return runExec(cmd, append([]string{name}, rest...))
	case "prompt":
		return runSandboxPrompt(cmd, name)
	case "allow":
		return runSandboxAllow(cmd, name, rest)
	case "allowed":
		return runSandboxAllowed(cmd, name)
	case "deny":
		return runSandboxDeny(cmd, name, rest)
	default:
		return fmt.Errorf("unknown subcommand %q: valid subcommands are info, log, exec, prompt, allow, allowed, deny", subcmd)
	}
}
