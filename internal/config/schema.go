// ABOUTME: Deterministic on-disk schema versioning for a realm's DataDir.
// ABOUTME: A plain-int .schema-version stamp drives status + migration; no artifact-sniffing.

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// LibrarySchemaVersion is the current on-disk schema version the library
// expects in its DataDir. Bump this when the library's on-disk layout or
// file formats change in a way that needs a migration step; add a matching
// case to migrateLibraryStep and document the change in BREAKING-CHANGES.
const LibrarySchemaVersion = 1

// LayoutStatus is the verdict of a realm status check: what a realm's DataDir
// needs before it can be used. A too-new on-disk version is reported as an
// error from RealmStatus, not as a status value, because it is unrecoverable
// without upgrading the binary.
type LayoutStatus int

const (
	// LayoutFresh means the realm's DataDir is absent or empty: no data to
	// migrate, so it should be created fresh at the current version.
	LayoutFresh LayoutStatus = iota
	// LayoutMigrate means the DataDir exists at an older version and must be
	// brought current before use.
	LayoutMigrate
	// LayoutOK means the DataDir exists at exactly the current version.
	LayoutOK
)

// RealmStatus reports what dataDir needs relative to currentVersion. It is a
// pure, cheap check that looks only at dataDir and its plain-int
// .schema-version stamp — never at sibling realms, config files, or any other
// artifact. Recognizing what unstamped-but-non-empty content actually is
// belongs to the migrate command, not here.
//
//   - dataDir absent or empty      -> LayoutFresh
//   - stamp version  < current     -> LayoutMigrate
//   - stamp version == current     -> LayoutOK
//   - stamp version  > current     -> error (older binary; upgrade yoloai)
//
// A non-empty dataDir with no stamp reads as version 0, so it routes to
// LayoutMigrate (or LayoutOK only if current is 0).
func RealmStatus(dataDir string, currentVersion int) (LayoutStatus, error) {
	info, err := os.Stat(dataDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return LayoutFresh, nil
		}
		return 0, fmt.Errorf("inspect data dir: %w", err)
	}
	if !info.IsDir() {
		// Garbage at the realm path (a plain file): present, not fresh.
		// Route to migrate, which validates and rejects unrecognized
		// content rather than treating it as empty.
		return LayoutMigrate, nil
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return 0, fmt.Errorf("inspect data dir: %w", err)
	}
	if len(entries) == 0 {
		return LayoutFresh, nil
	}
	version, _, err := ReadSchemaVersion(SchemaVersionPathFor(dataDir))
	if err != nil {
		return 0, err
	}
	switch {
	case version > currentVersion:
		return 0, fmt.Errorf("data dir schema version %d is newer than this build supports (%d); upgrade yoloai", version, currentVersion)
	case version < currentVersion:
		return LayoutMigrate, nil
	default:
		return LayoutOK, nil
	}
}

// SchemaVersionPathFor returns dataDir/.schema-version — the stamp path for an
// arbitrary realm DataDir. Layout.SchemaVersionPath() is the library realm's
// convenience wrapper; this lets the shared RealmStatus check serve any realm.
func SchemaVersionPathFor(dataDir string) string {
	return filepath.Join(dataDir, ".schema-version")
}

// ReadSchemaVersion reads the plain-int schema stamp at path. The bool reports
// whether a stamp exists; a missing stamp returns (0, false, nil). The file is
// a bare integer (optionally surrounded by whitespace) — no JSON or YAML.
func ReadSchemaVersion(path string) (version int, exists bool, err error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is DataDir/.schema-version
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read schema version: %w", err)
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false, fmt.Errorf("parse schema version %q: %w", string(data), err)
	}
	return v, true, nil
}

// WriteSchemaVersion writes version as a plain integer to the stamp at path.
func WriteSchemaVersion(path string, version int) error {
	if err := fileutil.WriteFile(path, []byte(strconv.Itoa(version)), 0600); err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	return nil
}

// CreateFreshLibrary initializes the library realm's DataDir at the current
// schema version: it creates the directory and writes the version stamp. It is
// deliberately minimal — operational scaffolding (sandboxes/, profiles/,
// default config.yaml, …) stays in the engine's EnsureSetup. The startup gate
// calls this on a genuinely fresh install; direct embedders that own a clean
// dedicated DataDir call it themselves.
func CreateFreshLibrary(layout Layout) error {
	if err := fileutil.MkdirAll(layout.DataDir, 0750); err != nil {
		return fmt.Errorf("create library data dir: %w", err)
	}
	return WriteSchemaVersion(layout.SchemaVersionPath(), LibrarySchemaVersion)
}

// MigrateLibrary brings the library's DataDir up to LibrarySchemaVersion,
// then stamps it. The stamp is the only signal consulted: an unstamped
// DataDir is treated as version 0, migrations run from there, and the new
// version is recorded so every future run dispatches deterministically off
// the stamp rather than re-inspecting on-disk artifacts.
//
// v0 -> v1 is intentionally a no-op transform: the per-sandbox file shapes
// were already current before this stamp existed (the meta.json ->
// environment.json family of renames predates it), so reaching v1 only
// means recording the stamp. The flat -> namespaced directory move is a
// CLI-side concern handled above the library, since it restructures the dir
// above the library's root.
//
// This is invoked only by the explicit migrate command (and direct embedders
// that own their DataDir) — the engine no longer runs it automatically.
func MigrateLibrary(layout Layout) error {
	stampPath := layout.SchemaVersionPath()
	current, _, err := ReadSchemaVersion(stampPath)
	if err != nil {
		return err
	}
	if current > LibrarySchemaVersion {
		return fmt.Errorf("library data dir schema version %d is newer than this build supports (%d); upgrade yoloai", current, LibrarySchemaVersion)
	}
	for v := current; v < LibrarySchemaVersion; v++ {
		if err := migrateLibraryStep(layout, v); err != nil {
			return fmt.Errorf("library migration v%d -> v%d: %w", v, v+1, err)
		}
	}
	if current == LibrarySchemaVersion {
		return nil
	}
	return WriteSchemaVersion(stampPath, LibrarySchemaVersion)
}

// migrateLibraryStep applies the single transform that takes the library's
// DataDir from version `from` to `from+1`. Add a case per future bump.
func migrateLibraryStep(_ Layout, from int) error {
	switch from {
	case 0:
		// v0 -> v1: no on-disk transform; stamping happens in MigrateLibrary.
		return nil
	default:
		return fmt.Errorf("no migration registered from version %d", from)
	}
}
