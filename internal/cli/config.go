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
		newConfigResetCmd(),
	)

	return cmd
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [key]",
		Short: "Print configuration value(s)",
		Long: `Print configuration values from ~/.yoloai/profiles/base/config.yaml.

Without arguments, prints all settings with effective values (defaults + overrides).
With a dotted key (e.g., backend), prints just that value.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				out, err := sandbox.GetEffectiveConfig()
				if err != nil {
					return err
				}
				_, err = fmt.Fprint(cmd.OutOrStdout(), out)
				return err
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
		Long: `Set a configuration value in ~/.yoloai/profiles/base/config.yaml.

Uses dotted paths for nested keys (e.g., tart.image).
Creates the config file if it doesn't exist.
Preserves comments and formatting.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			if err := sandbox.UpdateConfigFields(map[string]string{
				args[0]: args[1],
			}); err != nil {
				return err
			}

			if jsonEnabled(cmd) {
				return writeJSON(cmd.OutOrStdout(), map[string]string{
					"key":    args[0],
					"value":  args[1],
					"action": "set",
				})
			}
			return nil
		},
	}
}

func newConfigResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset <key>",
		Short: "Reset a configuration value to its default",
		Long: `Remove a key from ~/.yoloai/profiles/base/config.yaml, reverting it to the internal default.

Works at any level: a single value (backend), a map entry
(env.OLLAMA_API_BASE), or an entire section (tart).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := sandbox.DeleteConfigField(args[0]); err != nil {
				return err
			}
			if jsonEnabled(cmd) {
				return writeJSON(cmd.OutOrStdout(), map[string]string{
					"key":    args[0],
					"action": "reset",
				})
			}
			return nil
		},
	}
}
