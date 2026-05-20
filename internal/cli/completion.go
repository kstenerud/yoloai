// ABOUTME: 'completion' command — generates shell completion scripts. Wired
// ABOUTME: under 'yoloai system completion' rather than at the top level.
package cli

import (
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion script",
		Long: `Generate shell completion script for the specified shell.

To load completions:

Bash:
  source <(yoloai system completion bash)

Zsh:
  source <(yoloai system completion zsh)

Fish:
  yoloai system completion fish | source

PowerShell:
  yoloai system completion powershell | Out-String | Invoke-Expression`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(cmd.OutOrStdout(), true)
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			default:
				return sandbox.NewUsageError("unsupported shell: %s (valid: bash, zsh, fish, powershell)", args[0])
			}
		},
	}
}
