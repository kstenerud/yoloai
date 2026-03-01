package cli

// ABOUTME: `yoloai sandbox` parent command grouping sandbox inspection
// ABOUTME: subcommands: list, info, log, exec, network-allow.

import "github.com/spf13/cobra"

func newSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sandbox",
		Short:   "Sandbox inspection",
		GroupID: groupInspect,
	}

	cmd.AddCommand(
		newSandboxListCmd(),
		newSandboxInfoCmd(),
		newSandboxLogCmd(),
		newSandboxExecCmd(),
		newSandboxNetworkAllowCmd(),
	)

	return cmd
}
