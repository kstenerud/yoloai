// ABOUTME: `yoloai sandbox network` parent command for managing sandbox
// ABOUTME: network allowlists: add, list, remove subcommands.
package cli

import "github.com/spf13/cobra"

func newSandboxNetworkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Manage sandbox network allowlist",
	}
	cmd.AddCommand(
		newSandboxNetworkAddCmd(),
		newSandboxNetworkListCmd(),
		newSandboxNetworkRemoveCmd(),
	)
	return cmd
}
