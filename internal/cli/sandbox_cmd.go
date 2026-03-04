// ABOUTME: `yoloai sandbox` parent command grouping sandbox inspection
// ABOUTME: subcommands: list, info, log, exec, network, prompt, clone.
package cli

import "github.com/spf13/cobra"

func newSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sandbox",
		Aliases: []string{"sb"},
		Short:   "Sandbox inspection",
		GroupID: groupInspect,
	}

	cmd.AddCommand(
		newSandboxListCmd(),
		newSandboxInfoCmd(),
		newSandboxLogCmd(),
		newSandboxExecCmd(),
		newSandboxNetworkCmd(),
		newSandboxPromptCmd(),
		newSandboxCloneCmd(),
	)

	return cmd
}
