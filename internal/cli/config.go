package cli

// ABOUTME: CLI commands for reading and writing yoloai configuration settings.
// ABOUTME: Routes global keys to ~/.yoloai/config.yaml, others to profile config.

import (
	"fmt"
	"os"

	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		Short:   "Get or set global configuration values",
		GroupID: cliutil.GroupAdmin,
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
		Long: `Print configuration values.

Without arguments, prints all settings with effective values (defaults + overrides).
With a dotted key (e.g., backend), prints just that value.

Global settings (tmux_conf, model_aliases) are stored in ~/.yoloai/config.yaml.
Default settings are stored in ~/.yoloai/defaults/config.yaml.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runConfigGet,
	}
}

// runConfigGet implements the config get command body.
func runConfigGet(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return configGetAll(cmd)
	}
	return configGetKey(cmd, args[0])
}

// configGetAll prints all effective configuration values.
func configGetAll(cmd *cobra.Command) error {
	out, err := config.GetEffectiveConfig(cliutil.Layout())
	if err != nil {
		return err
	}
	if cliutil.JSONEnabled(cmd) {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(out), &m); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
		return cliutil.WriteJSON(cmd.OutOrStdout(), m)
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), out)
	return err
}

// configGetKey prints a single configuration value by dotted key.
func configGetKey(cmd *cobra.Command, key string) error {
	value, found, err := config.GetConfigValue(cliutil.Layout(), key)
	if err != nil {
		return err
	}
	if !found {
		if cliutil.JSONEnabled(cmd) {
			return fmt.Errorf("key %q not found", key)
		}
		os.Exit(1)
	}
	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{
			"key":   key,
			"value": value,
		})
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), value)
	return err
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Long: `Set a configuration value.

Uses dotted paths for nested keys (e.g., tart.image).
Creates the config file if it doesn't exist.
Preserves comments and formatting.

Global settings (tmux_conf, model_aliases) are stored in ~/.yoloai/config.yaml.
Default settings are stored in ~/.yoloai/defaults/config.yaml.`,
		Args: cobra.ExactArgs(2),
		RunE: runConfigSet,
	}
}

// runConfigSet implements the config set command body.
func runConfigSet(cmd *cobra.Command, args []string) error {
	key, value := args[0], args[1]
	if config.IsGlobalKey(key) {
		if err := ensureGlobalConfig(); err != nil {
			return err
		}
		if err := config.UpdateGlobalConfigFields(cliutil.Layout(), map[string]string{key: value}); err != nil {
			return err
		}
	} else {
		if err := ensureProfileConfig(); err != nil {
			return err
		}
		if err := config.UpdateConfigFields(cliutil.Layout(), map[string]string{key: value}); err != nil {
			return err
		}
	}

	if cliutil.JSONEnabled(cmd) {
		return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{
			"key":    key,
			"value":  value,
			"action": "set",
		})
	}
	return nil
}

// ensureGlobalConfig creates the global config file if it does not exist.
func ensureGlobalConfig() error {
	configPath := cliutil.Layout().GlobalConfigPath()
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		return nil
	}
	dir := configPath[:len(configPath)-len("/config.yaml")]
	if err := fileutil.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	return fileutil.WriteFile(configPath, []byte("{}\n"), 0600)
}

// ensureProfileConfig creates the profile config file if it does not exist.
func ensureProfileConfig() error {
	configPath := cliutil.Layout().DefaultsConfigPath()
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		return nil
	}
	dir := configPath[:len(configPath)-len("/config.yaml")]
	if err := fileutil.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	scaffold := config.GenerateScaffoldConfig(config.DefaultConfigYAML)
	if err := fileutil.WriteFile(configPath, []byte(scaffold), 0600); err != nil {
		return fmt.Errorf("create defaults/config.yaml: %w", err)
	}
	return nil
}

func newConfigResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset <key>",
		Short: "Reset a configuration value to its default",
		Long: `Remove a key from configuration, reverting it to the internal default.

Works at any level: a single value (backend), a map entry
(env.OLLAMA_API_BASE), or an entire section (tart).

Global settings (tmux_conf, model_aliases) are stored in ~/.yoloai/config.yaml.
Default settings are stored in ~/.yoloai/defaults/config.yaml.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			if config.IsGlobalKey(args[0]) {
				err = config.DeleteGlobalConfigField(cliutil.Layout(), args[0])
			} else {
				err = config.DeleteConfigField(cliutil.Layout(), args[0])
			}
			if err != nil {
				return err
			}
			if cliutil.JSONEnabled(cmd) {
				return cliutil.WriteJSON(cmd.OutOrStdout(), map[string]string{
					"key":    args[0],
					"action": "reset",
				})
			}
			return nil
		},
	}
}
