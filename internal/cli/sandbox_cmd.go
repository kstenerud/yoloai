// ABOUTME: `yoloai sandbox` parent command grouping sandbox inspection
// ABOUTME: subcommands: list, info, log, exec, network-allow, prompt, config, clone.
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
		newSandboxNetworkAllowCmd(),
		newSandboxPromptCmd(),
		newSandboxConfigShowCmd(),
		newSandboxCloneCmd(),
	)

	return cmd
}
