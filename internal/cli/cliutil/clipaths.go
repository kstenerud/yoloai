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

	// InitializingSentinelName is the basename of TOP/.initializing — see
	// TopInitializingSentinelPath. Exported so the gate can tell a TOP that
	// holds nothing but the sentinel from one that holds real content.
	InitializingSentinelName = ".initializing"
)

// TopDir returns the shared top data directory (TOP) — the parent of both
// TOP/library and TOP/cli. Derived from the root Layout's DataDir (rooted at
// TOP/library by LayoutForDataDir), which is $HOME/.yoloai by default or the
// --data-dir value when supplied. Like Layout(), this is a pure accessor: it
// requires the root Layout to have been set (SetRootLayoutFromFlag in
// production, SetRootLayout in direct-handler tests).
func TopDir() string {
	return filepath.Dir(Layout().DataDir)
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

// TopInitializingSentinelPath returns TOP/.initializing — an empty marker
// file, sibling to TOP/cli and TOP/library, that brackets a fresh TOP build
// (see MarkInitializing). It lives directly under TOP rather than under
// CLIDir because it describes the directory *above* the library's root; the
// library is rooted at and confined to its own DataDir and must not speak
// about TOP (D60/D61).
func TopInitializingSentinelPath() string {
	return filepath.Join(TopDir(), InitializingSentinelName)
}
