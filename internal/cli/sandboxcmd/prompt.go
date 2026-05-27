// ABOUTME: `yoloai sandbox <name> prompt` handler — show the prompt text for a sandbox.
package sandboxcmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/spf13/cobra"
)

func runSandboxPrompt(cmd *cobra.Command, name string) error {
	sandboxDir := cliutil.Layout().SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return err
	}

	promptPath := filepath.Join(sandboxDir, "prompt.txt")
	data, err := os.ReadFile(promptPath) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		if os.IsNotExist(err) {
			if cliutil.JSONEnabled(cmd) {
				return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
					"name":   name,
					"prompt": nil,
				})
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "No prompt configured")
			return err
		}
		return fmt.Errorf("read prompt: %w", err)
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
			"name":   name,
			"prompt": string(data),
		})
	}

	_, err = fmt.Fprint(cmd.OutOrStdout(), string(data))
	return err
}
