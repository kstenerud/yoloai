// ABOUTME: `yoloai sandbox <name> prompt` handler — show the prompt text for a sandbox.
package sandboxcmd

import (
	"fmt"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/spf13/cobra"
)

func runSandboxPrompt(cmd *cobra.Command, name string) error {
	c, err := cliutil.Client(cmd)
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck // best-effort cleanup
	sb, err := c.Sandbox(name)
	if err != nil {
		return err
	}
	text, configured, err := sb.Agent().Prompt()
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
