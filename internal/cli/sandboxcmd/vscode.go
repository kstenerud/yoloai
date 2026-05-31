// ABOUTME: `yoloai sandbox <name> vscode` command — opens a running sandbox in VS Code.
// ABOUTME: Builds a container attach URI and launches VS Code or prints connection instructions.

package sandboxcmd

import (
	"fmt"
	"os/exec"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/spf13/cobra"
)

func newSandboxVscodeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "vscode <name>",
		Short: "Open a running sandbox in VS Code (attach-to-container)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := cliutil.ResolveName(cmd, args)
			if err != nil {
				return err
			}

			attach, err := cliutil.NewSystemClient().VscodeAttach(name)
			if err != nil {
				return err
			}

			if !attach.Supported {
				fmt.Fprintf(cmd.OutOrStdout(), //nolint:errcheck // best-effort output
					"Container attach is not supported for the %s backend.\n"+
						"Use --vscode-tunnel when creating the sandbox instead.\n",
					attach.Backend)
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Opening VS Code attached to sandbox %q...\n", name) //nolint:errcheck // best-effort output
			fmt.Fprintf(cmd.OutOrStdout(), "Container: %s\n", attach.ContainerName)             //nolint:errcheck // best-effort output
			fmt.Fprintf(cmd.OutOrStdout(), "Workdir:   %s\n\n", attach.WorkdirPath)             //nolint:errcheck // best-effort output

			// Try to open VS Code if `code` is on PATH
			if _, lookErr := exec.LookPath("code"); lookErr == nil {
				openCmd := exec.Command("code", "--folder-uri", attach.FolderURI) //nolint:gosec // G204: uri is constructed from trusted sandbox metadata
				if runErr := openCmd.Run(); runErr != nil {
					// Fall through to print instructions
					fmt.Fprintf(cmd.OutOrStdout(), "Failed to open VS Code (is VS Code CLI installed?)\n") //nolint:errcheck // best-effort output
				} else {
					return nil
				}
			}

			// VS Code CLI not found or failed — print instructions
			fmt.Fprintf(cmd.OutOrStdout(), "Install the VS Code CLI and run:\n")         //nolint:errcheck // best-effort output
			fmt.Fprintf(cmd.OutOrStdout(), "  code --folder-uri %q\n", attach.FolderURI) //nolint:errcheck // best-effort output
			return nil
		},
	}
}
