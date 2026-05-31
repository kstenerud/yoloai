// ABOUTME: `yoloai sandbox <name> prompt` handler — show the prompt text for a sandbox.
package sandboxcmd

import (
	"fmt"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/spf13/cobra"
)

func runSandboxPrompt(cmd *cobra.Command, name string) error {
	text, configured, err := cliutil.NewSystemClient().Prompt(name)
	if err != nil {
		return err
	}

	if !configured {
		if cliutil.JSONEnabled(cmd) {
			return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
				"name":   name,
				"prompt": nil,
			})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "No prompt configured")
		return err
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]any{
			"name":   name,
			"prompt": text,
		})
	}

	_, err = fmt.Fprint(cmd.OutOrStdout(), text)
	return err
}
