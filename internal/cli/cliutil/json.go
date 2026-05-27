package cliutil

// ABOUTME: Helper functions for --json flag support across all CLI commands.

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// JSONEnabled checks if the --json persistent flag is set on the command.
func JSONEnabled(cmd *cobra.Command) bool {
	// Check persistent flags first (where --json is registered)
	if f := cmd.PersistentFlags().Lookup("json"); f != nil {
		v, _ := cmd.PersistentFlags().GetBool("json")
		return v
	}
	// Fallback to checking inherited flags (for subcommands)
	v, _ := cmd.Flags().GetBool("json")
	return v
}

// WriteJSON marshals v as indented JSON and writes it to w with a trailing newline.
func WriteJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// WriteJSONError writes a JSON error object to w. Used for stderr error output
// when --json is active.
func WriteJSONError(w io.Writer, err error) {
	data, _ := json.Marshal(map[string]string{"error": err.Error()})
	fmt.Fprintf(w, "%s\n", data) //nolint:errcheck // best-effort stderr write
}

// EffectiveYes returns true if --yes is set or --json is enabled.
// JSON mode implies --yes because interactive prompts can't work in a
// machine-readable pipeline.
func EffectiveYes(cmd *cobra.Command) bool {
	yes, _ := cmd.Flags().GetBool("yes")
	return yes || JSONEnabled(cmd)
}

// ErrJSONNotSupported returns an error indicating that --json is not supported
// for interactive commands like attach and exec.
func ErrJSONNotSupported(name string) error {
	return sandbox.NewUsageError("--json is not supported for interactive command %q", name)
}
