// ABOUTME: CLI-namespace paths under TOP/cli — state the app owns, kept
// ABOUTME: separate from the library's TOP/library so multiple apps can share TOP.

package cliutil

import "path/filepath"

const (
	// libraryNamespace is the subdirectory of the top data dir that the
	// library owns (sandboxes, profiles, cache, config, defaults, ...).
	// The library Layout is rooted here; see Layout / LayoutForDataDir.
	libraryNamespace = "library"

	// cliNamespace is the subdirectory of the top data dir that the CLI
	// app owns (extensions, app state). The library never reads it — this
	// separation lets a second app embedding yoloai share the same top
	// dir without its bookkeeping bleeding into the library's.
	cliNamespace = "cli"
)

// TopDir returns the shared top data directory (TOP) — the parent of both
// TOP/library and TOP/cli. Defaults to $HOME/.yoloai, or the --data-dir
// value when one was supplied. Derived from the library Layout's DataDir
// (rooted at TOP/library by Layout / LayoutForDataDir).
func TopDir() string {
	if rootLayout.DataDir != "" {
		return filepath.Dir(rootLayout.DataDir)
	}
	return filepath.Join(resolveHome(), ".yoloai")
}

// CLIDir returns TOP/cli — the root of the CLI app's own on-disk state.
func CLIDir() string {
	return filepath.Join(TopDir(), cliNamespace)
}

// CLIExtensionsDir returns TOP/cli/extensions — where user-defined
// `yoloai x` extensions live. A pure CLI feature; not a library concern.
func CLIExtensionsDir() string {
	return filepath.Join(CLIDir(), "extensions")
}

// CLIStatePath returns TOP/cli/state.yaml — the CLI app's own state file
// (e.g. whether the first-run setup wizard has been shown). The library
// keeps no such setup-ceremony state; recording it is the app's business.
func CLIStatePath() string {
	return filepath.Join(CLIDir(), "state.yaml")
}

// CLISchemaVersionPath returns TOP/cli/.schema-version — the stamp that
// versions the CLI app's on-disk layout, independent of the library's own
// TOP/library/.schema-version stamp.
func CLISchemaVersionPath() string {
	return filepath.Join(CLIDir(), ".schema-version")
}
