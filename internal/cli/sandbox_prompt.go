// ABOUTME: `yoloai sandbox prompt` — show the prompt text for a sandbox.
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newSandboxPromptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prompt <name>",
		Short: "Show sandbox prompt",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _, err := resolveName(cmd, args)
			if err != nil {
				return err
			}

			sandboxDir, err := sandbox.RequireSandboxDir(name)
			if err != nil {
				return err
			}

			promptPath := filepath.Join(sandboxDir, "prompt.txt")
			data, err := os.ReadFile(promptPath) //nolint:gosec // path is sandbox-controlled
			if err != nil {
				if os.IsNotExist(err) {
					if jsonEnabled(cmd) {
						return writeJSON(cmd.OutOrStdout(), map[string]any{
							"name":   name,
							"prompt": nil,
						})
					}
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "No prompt configured")
					return err
				}
				return fmt.Errorf("read prompt: %w", err)
			}

			if jsonEnabled(cmd) {
				return writeJSON(cmd.OutOrStdout(), map[string]any{
					"name":   name,
					"prompt": string(data),
				})
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), string(data))
			return err
		},
	}
}
