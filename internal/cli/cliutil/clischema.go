// ABOUTME: CLI-side schema versioning + the one-shot flat->namespaced bootstrap
// ABOUTME: that relocates a pre-namespace ~/.yoloai into TOP/library + TOP/cli.

package cliutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"gopkg.in/yaml.v3"
)

// CLISchemaVersion is the current on-disk layout version for the shared top
// dir's CLI-owned namespace. v1 introduced the library/cli split; bump this
// (and add a migration step) when the CLI's on-disk layout changes again.
const CLISchemaVersion = 1

// libraryOwnedEntries are the names that, in a pre-namespace (v0) flat
// install, sat directly under TOP and belong to the library. The bootstrap
// relocates each (when present) into TOP/library. Per-sandbox lockfiles live
// under sandboxes/ so they move with it; the library .schema-version stamp
// did not exist in v0, so there is none to move (the library re-stamps inside
// TOP/library on its next MigrateLibrary).
var libraryOwnedEntries = []string{
	"sandboxes",
	"profiles",
	"cache",
	"trash",
	"defaults",
	"config.yaml",
	"tart-base-metadata",
	"tart-base-locks",
	"docker-base-locks",
	"cni",
	"vscode-cli",
}

// MigrateCLI brings the shared top dir's CLI-owned layout up to
// CLISchemaVersion and stamps TOP/cli/.schema-version. The stamp is the only
// signal consulted once present: a stamped layout short-circuits immediately.
//
// On the first run of a namespace-aware binary against a pre-namespace (v0)
// flat install — detected once, deterministically, by a flat TOP/config.yaml
// with no TOP/library beside it — it relocates the library-owned dirs into
// TOP/library and the CLI-owned dirs into TOP/cli, carries the legacy
// setup_complete flag forward as first-run-tip suppression, drops the old flat
// state file, then stamps. The flat -> namespaced move is a CLI concern: it
// restructures the dir *above* the library's root, which the library (rooted
// at and confined to its DataDir) cannot and must not do itself.
func MigrateCLI() error {
	stampPath := CLISchemaVersionPath()
	current, exists, err := config.ReadSchemaVersion(stampPath)
	if err != nil {
		return err
	}
	if exists {
		if current > CLISchemaVersion {
			return fmt.Errorf("cli data dir schema version %d is newer than this build supports (%d); upgrade yoloai", current, CLISchemaVersion)
		}
		return nil
	}

	top := TopDir()
	if isFlatV0Install(top) {
		if err := relocateFlatToNamespaced(top); err != nil {
			return err
		}
		return stampCLI(stampPath)
	}

	// No stamp and no flat install to migrate. On a brand-new install
	// (nothing on disk yet) defer stamping so read-only commands like
	// `yoloai version` don't materialize directories; the stamp lands once
	// real work has created the layout. If the namespaced layout already
	// exists (e.g. created by an interim build before the stamp existed),
	// record the stamp now so future runs short-circuit.
	if dirExists(filepath.Join(top, libraryNamespace)) || dirExists(CLIDir()) {
		return stampCLI(stampPath)
	}
	return nil
}

// stampCLI records CLISchemaVersion at stampPath, creating TOP/cli first
// (WriteSchemaVersion does not create parent dirs).
func stampCLI(stampPath string) error {
	if err := fileutil.MkdirAll(CLIDir(), 0750); err != nil {
		return fmt.Errorf("create cli namespace: %w", err)
	}
	return config.WriteSchemaVersion(stampPath, CLISchemaVersion)
}

// isFlatV0Install reports whether top looks like a pre-namespace install: a
// flat config.yaml directly under TOP, with no TOP/library beside it. This
// heuristic runs at most once per TOP — after the bootstrap the stamp is
// authoritative and this is never consulted again.
func isFlatV0Install(top string) bool {
	if dirExists(filepath.Join(top, libraryNamespace)) {
		return false
	}
	info, err := os.Stat(filepath.Join(top, "config.yaml"))
	return err == nil && !info.IsDir()
}

// relocateFlatToNamespaced performs the v0 -> v1 move in place under TOP.
// Renames stay within one filesystem (same parent dir) so they are atomic and
// cheap.
func relocateFlatToNamespaced(top string) error {
	libDir := filepath.Join(top, libraryNamespace)
	cliDir := filepath.Join(top, cliNamespace)
	if err := fileutil.MkdirAll(libDir, 0750); err != nil {
		return fmt.Errorf("create library namespace: %w", err)
	}
	if err := fileutil.MkdirAll(cliDir, 0750); err != nil {
		return fmt.Errorf("create cli namespace: %w", err)
	}
	for _, name := range libraryOwnedEntries {
		if err := moveIfExists(filepath.Join(top, name), filepath.Join(libDir, name)); err != nil {
			return err
		}
	}
	if err := moveIfExists(filepath.Join(top, "extensions"), filepath.Join(cliDir, "extensions")); err != nil {
		return err
	}
	return migrateFlatSetupState(top)
}

// moveIfExists renames from -> to when from exists; a missing source is a
// no-op (not every entry is present in every install).
func moveIfExists(from, to string) error {
	if _, err := os.Lstat(from); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", from, err)
	}
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("relocate %s -> %s: %w", from, to, err)
	}
	return nil
}

// flatState mirrors the legacy TOP/state.yaml shape (pre-namespace), whose only
// field was setup_complete. The config package no longer defines this type
// (the library stopped owning setup ceremony), so this one-shot migration
// parses it locally.
type flatState struct {
	SetupComplete bool `yaml:"setup_complete"`
}

// migrateFlatSetupState carries the legacy setup_complete flag forward: an
// upgrader who already finished first-run setup should not see the onboarding
// tip resurface, so a true value is recorded as FirstRunTipShown. The old flat
// state file is then removed.
func migrateFlatSetupState(top string) error {
	oldPath := filepath.Join(top, "state.yaml")
	data, err := os.ReadFile(oldPath) //nolint:gosec // G304: legacy state path under TOP
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // no legacy state; the first-run tip will fire normally
		}
		return fmt.Errorf("read legacy state.yaml: %w", err)
	}
	var legacy flatState
	// A corrupt legacy file must not block the migration; on parse failure
	// fall through with setup_complete=false and let the tip fire.
	_ = yaml.Unmarshal(data, &legacy)
	if legacy.SetupComplete {
		if err := SaveCLIState(&CLIState{FirstRunTipShown: true}); err != nil {
			return err
		}
	}
	if err := os.Remove(oldPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove legacy state.yaml: %w", err)
	}
	return nil
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
