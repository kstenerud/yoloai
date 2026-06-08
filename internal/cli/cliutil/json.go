package cliutil

// ABOUTME: Helper functions for --json flag support across all CLI commands.

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/kstenerud/yoloai/yoerrors"
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
//
// Per the --json convention (standards/cli.md), v must be a JSON OBJECT, never a
// bare array: every command emits a top-level object so consumers rely on a
// stable shape and commands can grow sibling fields without breaking parsers.
// Use WriteJSONList for single-array list commands.
func WriteJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// WriteJSONList writes a list-type command's output as a single-key envelope
// object: {"<key>": [...]}. This keeps every command's top-level shape an object
// (see WriteJSON) and guarantees the array is [] rather than null when empty.
// Commands that carry sibling metadata alongside the list (e.g. sandbox list's
// unavailable_backends) build their own struct instead and wrap each array field
// with EmptyIfNil.
func WriteJSONList[T any](w io.Writer, key string, items []T) error {
	return WriteJSON(w, map[string]any{key: EmptyIfNil(items)})
}

// EmptyIfNil returns s, or a non-nil empty slice when s is nil, so it marshals
// as [] rather than null. Use for every array field inside a custom envelope
// struct — a nil Go slice otherwise serializes as null, which the convention
// forbids (consumers must never have to handle both [] and null).
func EmptyIfNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
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
	return yoerrors.NewUsageError("--json is not supported for interactive command %q", name)
}
