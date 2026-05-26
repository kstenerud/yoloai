// ABOUTME: `yoloai sandbox <name> vscode` command — opens a running sandbox in VS Code.
// ABOUTME: Builds a container attach URI and launches VS Code or prints connection instructions.

package cli

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/sandbox/store"
	"github.com/spf13/cobra"
)

func newSandboxVscodeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "vscode <name>",
		Short: "Open a running sandbox in VS Code (attach-to-container)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			sandboxDir := cliLayout().SandboxDir(name)
			if err := store.RequireSandboxDir(sandboxDir); err != nil {
				return sandbox.ErrSandboxNotFound
			}

			meta, err := store.LoadMeta(sandboxDir)
			if err != nil {
				return fmt.Errorf("load sandbox metadata: %w", err)
			}

			// Check backend support for container attach. Query the
			// backend descriptor's ContainerAttach capability rather than
			// matching on backend names — new container-compatible
			// backends will be supported automatically when they declare
			// the capability.
			desc, descOK := runtime.Descriptor(meta.Backend)
			if !descOK || !desc.Capabilities.ContainerAttach {
				fmt.Fprintf(cmd.OutOrStdout(), //nolint:errcheck // best-effort output
					"Container attach is not supported for the %s backend.\n"+
						"Use --vscode-tunnel when creating the sandbox instead.\n",
					meta.Backend)
				return nil
			}

			// Build the attach URI
			containerName := store.InstanceName(meta.Name)
			payload := map[string]string{"containerName": containerName}
			jsonBytes, err := json.Marshal(payload)
			if err != nil {
				return fmt.Errorf("marshal container payload: %w", err)
			}
			hexEncoded := hex.EncodeToString(jsonBytes)
			workdirPath := meta.Workdir.MountPath

			uri := fmt.Sprintf("vscode-remote://attached-container+%s%s", hexEncoded, workdirPath)

			fmt.Fprintf(cmd.OutOrStdout(), "Opening VS Code attached to sandbox %q...\n", name) //nolint:errcheck // best-effort output
			fmt.Fprintf(cmd.OutOrStdout(), "Container: %s\n", containerName)                    //nolint:errcheck // best-effort output
			fmt.Fprintf(cmd.OutOrStdout(), "Workdir:   %s\n\n", workdirPath)                    //nolint:errcheck // best-effort output

			// Try to open VS Code if `code` is on PATH
			codePath, lookErr := exec.LookPath("code")
			if lookErr == nil {
				_ = codePath
				openCmd := exec.Command("code", "--folder-uri", uri) //nolint:gosec // G204: uri is constructed from trusted sandbox metadata
				if runErr := openCmd.Run(); runErr != nil {
					// Fall through to print instructions
					fmt.Fprintf(cmd.OutOrStdout(), "Failed to open VS Code (is VS Code CLI installed?)\n") //nolint:errcheck // best-effort output
				} else {
					return nil
				}
			}

			// VS Code CLI not found or failed — print instructions
			fmt.Fprintf(cmd.OutOrStdout(), "Install the VS Code CLI and run:\n") //nolint:errcheck // best-effort output
			fmt.Fprintf(cmd.OutOrStdout(), "  code --folder-uri %q\n", uri)      //nolint:errcheck // best-effort output
			return nil
		},
	}
}
