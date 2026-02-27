package cli

// ABOUTME: CLI commands for reading and writing yoloai config.yaml settings.

import (
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		Short:   "Get or set configuration values",
		GroupID: groupAdmin,
	}

	cmd.AddCommand(
		newConfigGetCmd(),
		newConfigSetCmd(),
	)

	return cmd
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [key]",
		Short: "Print configuration value(s)",
		Long: `Print configuration values from ~/.yoloai/config.yaml.

Without arguments, prints the entire config file.
With a dotted key (e.g., defaults.backend), prints just that value.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				data, err := sandbox.ReadConfigRaw()
				if err != nil {
					return err
				}
				if data != nil {
					_, err = fmt.Fprint(cmd.OutOrStdout(), string(data))
					return err
				}
				return nil
			}

			value, found, err := sandbox.GetConfigValue(args[0])
			if err != nil {
				return err
			}
			if !found {
				os.Exit(1)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), value)
			return err
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Long: `Set a configuration value in ~/.yoloai/config.yaml.

Uses dotted paths for nested keys (e.g., defaults.backend).
Creates the config file if it doesn't exist.
Preserves comments and formatting.`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			configPath, err := sandbox.ConfigPath()
			if err != nil {
				return err
			}

			// Create config file if it doesn't exist.
			if _, err := os.Stat(configPath); os.IsNotExist(err) {
				dir := configPath[:len(configPath)-len("/config.yaml")]
				if err := os.MkdirAll(dir, 0750); err != nil {
					return fmt.Errorf("create config directory: %w", err)
				}
				if err := os.WriteFile(configPath, []byte("{}\n"), 0600); err != nil {
					return fmt.Errorf("create config.yaml: %w", err)
				}
			}

			return sandbox.UpdateConfigFields(map[string]string{
				args[0]: args[1],
			})
		},
	}
}
