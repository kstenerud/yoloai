package cli

// ABOUTME: Helper functions for --json flag support across all CLI commands.

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// jsonEnabled checks if the --json persistent flag is set on the command.
func jsonEnabled(cmd *cobra.Command) bool {
	// Check persistent flags first (where --json is registered)
	if f := cmd.PersistentFlags().Lookup("json"); f != nil {
		v, _ := cmd.PersistentFlags().GetBool("json")
		return v
	}
	// Fallback to checking inherited flags (for subcommands)
	v, _ := cmd.Flags().GetBool("json")
	return v
}

// writeJSON marshals v as indented JSON and writes it to w with a trailing newline.
func writeJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// writeJSONError writes a JSON error object to w. Used for stderr error output
// when --json is active.
func writeJSONError(w io.Writer, err error) {
	data, _ := json.Marshal(map[string]string{"error": err.Error()})
	fmt.Fprintf(w, "%s\n", data) //nolint:errcheck // best-effort stderr write
}

// errJSONNotSupported returns an error indicating that --json is not supported
// for interactive commands like attach and exec.
func errJSONNotSupported(name string) error {
	return fmt.Errorf("--json is not supported for interactive command %q", name)
}

// requireYesForJSON returns an error if --json is set without --yes.
// Commands with confirmation prompts must require --yes in JSON mode since
// interactive prompts can't work in a machine-readable pipeline.
func requireYesForJSON(cmd *cobra.Command) error {
	if !jsonEnabled(cmd) {
		return nil
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		return fmt.Errorf("--json requires --yes to skip confirmation prompts")
	}
	return nil
}
