// ABOUTME: `yoloai sandbox config` — show the container config for a sandbox.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/spf13/cobra"
)

func newSandboxConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config <name>",
		Short: "Show sandbox configuration",
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

			configPath := filepath.Join(sandboxDir, "config.json")
			data, err := os.ReadFile(configPath) //nolint:gosec // path is sandbox-controlled
			if err != nil {
				return fmt.Errorf("read config: %w", err)
			}

			if jsonEnabled(cmd) {
				// Pass through raw JSON
				_, err = cmd.OutOrStdout().Write(data)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout())
				return err
			}

			// Pretty-print for human consumption
			var pretty json.RawMessage
			if err := json.Unmarshal(data, &pretty); err != nil {
				// If not valid JSON, just print raw
				_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return err
			}

			formatted, err := json.MarshalIndent(pretty, "", "  ")
			if err != nil {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return err
			}

			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(formatted))
			return err
		},
	}
}
