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

// CLIStatus reports what the CLI realm's DataDir (TOP/cli) needs before use,
// using the same dumb, read-only check the library realm uses: LayoutFresh
// (absent/empty), LayoutMigrate (older version), or LayoutOK (current). A
// too-new on-disk version returns an error. The startup gate calls this; it
// never mutates anything.
func CLIStatus() (config.LayoutStatus, error) {
	return config.RealmStatus(CLIDir(), CLISchemaVersion)
}

// CreateFreshCLI initializes the CLI realm at the current version: it creates
// TOP/cli and writes the plain-int version stamp. Called on a genuinely fresh
// install (by the gate) and as the terminal step of the flat -> namespaced
// migration.
func CreateFreshCLI() error {
	if err := fileutil.MkdirAll(CLIDir(), 0750); err != nil {
		return fmt.Errorf("create cli data dir: %w", err)
	}
	return config.WriteSchemaVersion(CLISchemaVersionPath(), CLISchemaVersion)
}

// MigrateCLI brings the shared top dir's CLI-owned layout up to
// CLISchemaVersion. It is the CLI realm's mutation — invoked only by the
// explicit `yoloai system migrate` command, never on the normal startup path
// (the gate uses the read-only CLIStatus instead).
//
// On a pre-namespace (v0) flat install — detected deterministically by a flat
// TOP/config.yaml with no TOP/library beside it — it relocates the
// library-owned dirs into TOP/library and the CLI-owned dirs into TOP/cli,
// carries the legacy setup_complete flag forward as first-run-tip suppression,
// drops the old flat state file, then stamps. The flat -> namespaced move is a
// CLI concern: it restructures the dir *above* the library's root, which the
// library (rooted at and confined to its DataDir) cannot and must not do
// itself.
//
// Idempotent: an already-stamped layout is a no-op. It validates that TOP is
// something it recognizes and errors on unrecognized content rather than
// relocating arbitrary files (e.g. when --data-dir points at the wrong path).
//
// A TOP/.initializing sentinel (DF128) is also recognized: `system migrate`
// is gate-exempt, so it never sees runMigrationGate's own sentinel handling,
// and without a case here a sentinel-marked TOP falls to the "not a
// recognized yoloai data directory" default — the exact wedge the sentinel
// exists to make recoverable.
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
		// Already at the current version (v1 has no further CLI steps yet).
		return nil
	}

	top := TopDir()
	switch {
	case isFlatV0Install(top):
		if err := relocateFlatToNamespaced(top); err != nil {
			return err
		}
		return CreateFreshCLI()
	case dirExists(filepath.Join(top, libraryNamespace)) || dirExists(CLIDir()):
		// A namespaced layout with no CLI stamp: adopt it by stamping, relocating
		// nothing — the layout is already the shape we want.
		//
		// This reads like leftovers from the interim builds between D60 and the
		// stamp landing, and it is not: the condition is any TOP where one realm
		// exists without the CLI stamp, which a shipped install reaches whenever
		// the library realm is created WITHOUT the CLI — an embedder rooted at
		// TOP/library, on a TOP the user later runs the CLI against. The startup
		// gate calls that state InconsistentDataDir and refuses; this branch is
		// what repairs it. Do not delete it as dead code (it was nearly retired
		// on that reading, 2026-07-17).
		return CreateFreshCLI()
	case IsInitializing():
		// A crashed initFreshDataDir left TOP/.initializing behind. Every
		// realm reachable while it is present is, by construction, still
		// skeletal (the sentinel is written before either realm exists), so
		// initializing the CLI realm fresh is always safe here — the case
		// above already caught a partially-built cli/ or library/ alongside
		// it.
		return CreateFreshCLI()
	case dirAbsentOrEmpty(top):
		// Nothing on disk yet: initialize the CLI realm fresh.
		return CreateFreshCLI()
	default:
		// A non-empty TOP that is neither a flat v0 install nor a namespaced
		// layout — we don't recognize it. Refuse rather than mangle it.
		return fmt.Errorf("cannot migrate %q: not a recognized yoloai data directory (expected a flat v0 install or a library/cli layout)", top)
	}
}

// CurrentLibraryDataDir returns where library-owned content physically lives
// right now: TOP itself on an un-relocated flat v0 install (its config.yaml,
// sandboxes/, profiles/ sit directly under TOP), otherwise the namespaced
// TOP/library. The read-only `system migrate` preview roots its audit here so it
// inspects the real, pre-relocation location; the apply path relocates first and
// then operates on the namespaced layout. Uses the same flat-v0 predicate as
// MigrateCLI, so preview and apply agree on which install is flat.
func CurrentLibraryDataDir() string {
	top := TopDir()
	if isFlatV0Install(top) {
		return top
	}
	return filepath.Join(top, libraryNamespace)
}

// CurrentLibrarySchema reads the library realm's on-disk schema version at its
// current physical location (see CurrentLibraryDataDir), returning 0 when it is
// unstamped or flat (pre-.schema-version). Used to name the release range a
// blocked `system migrate` should downgrade to.
func CurrentLibrarySchema() int {
	v, ok, err := config.ReadSchemaVersion(config.SchemaVersionPathFor(CurrentLibraryDataDir()))
	if err != nil || !ok {
		return 0
	}
	return v
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
// field was setup_complete. The config package doesn't define this type (the
// library doesn't own setup ceremony), so this one-shot migration parses it
// locally.
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

// dirAbsentOrEmpty reports whether dir does not exist or exists but contains no
// entries. A read error other than "not exist" (e.g. dir is a plain file) reads
// as non-empty so MigrateCLI's garbage branch can reject it.
func dirAbsentOrEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	return len(entries) == 0
}
