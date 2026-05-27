package configcmd

// ABOUTME: CLI commands for reading and writing yoloai configuration settings.
// ABOUTME: Routes through yoloai.SystemClient.Config(); rendering only lives here.

import (
	"errors"
	"fmt"
	"os"

	"github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/cli/cliutil"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func NewCmd() *cobra.Command {
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
	out, err := cliutil.NewSystemClient().Config().Effective(cmd.Context())
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
//
// CLI semantics: a missing key exits 1 silently in human mode (matches
// shell expectations for "is this set?"-style scripts) but returns a
// loud error in --json mode so machine-readable callers always get
// structured failure.
func configGetKey(cmd *cobra.Command, key string) error {
	value, err := cliutil.NewSystemClient().Config().Get(cmd.Context(), key)
	if err != nil {
		if errors.Is(err, yoloai.ErrConfigKeyNotFound) {
			if cliutil.JSONEnabled(cmd) {
				return fmt.Errorf("key %q not found", key)
			}
			os.Exit(1)
		}
		return err
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
	if err := cliutil.NewSystemClient().Config().Set(cmd.Context(), key, value); err != nil {
		return err
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
			if err := cliutil.NewSystemClient().Config().Reset(cmd.Context(), args[0]); err != nil {
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
