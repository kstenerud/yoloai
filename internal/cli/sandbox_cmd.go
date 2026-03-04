// ABOUTME: `yoloai sandbox` parent command grouping sandbox tool
// ABOUTME: subcommands: list, info, log, exec, network, prompt.
package cli

import "github.com/spf13/cobra"

func newSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sandbox",
		Aliases: []string{"sb"},
		Short:   "Sandbox tools",
		GroupID: groupSandboxTools,
	}

	cloneAlias := newSandboxCloneCmd()
	cloneAlias.Hidden = true

	cmd.AddCommand(
		newSandboxListCmd(),
		newSandboxInfoCmd(),
		newSandboxLogCmd(),
		newSandboxExecCmd(),
		newSandboxNetworkCmd(),
		newSandboxPromptCmd(),
		cloneAlias,
	)

	return cmd
}
