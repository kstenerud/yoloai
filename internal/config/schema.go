// ABOUTME: Deterministic on-disk schema versioning for the library's DataDir.
// ABOUTME: A .schema-version stamp drives which migrations run; no artifact-sniffing.

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// LibrarySchemaVersion is the current on-disk schema version the library
// expects in its DataDir. Bump this when the library's on-disk layout or
// file formats change in a way that needs a migration step; add a matching
// case to migrateLibraryStep and document the change in BREAKING-CHANGES.
const LibrarySchemaVersion = 1

// schemaStamp is the JSON shape written to Layout.SchemaVersionPath().
type schemaStamp struct {
	Version int `json:"version"`
}

// ReadSchemaVersion reads the schema stamp at path. The bool reports
// whether a stamp exists; a missing stamp returns (0, false, nil).
func ReadSchemaVersion(path string) (version int, exists bool, err error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is DataDir/.schema-version
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read schema version: %w", err)
	}
	var s schemaStamp
	if err := json.Unmarshal(data, &s); err != nil {
		return 0, false, fmt.Errorf("parse schema version: %w", err)
	}
	return s.Version, true, nil
}

// WriteSchemaVersion writes version to the stamp at path.
func WriteSchemaVersion(path string, version int) error {
	data, err := json.Marshal(schemaStamp{Version: version})
	if err != nil {
		return fmt.Errorf("marshal schema version: %w", err)
	}
	if err := fileutil.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	return nil
}

// MigrateLibrary brings the library's DataDir up to LibrarySchemaVersion,
// then stamps it. The stamp is the only signal consulted: an unstamped
// DataDir is treated as version 0 (whether brand-new or a pre-stamp
// install), migrations run from there, and the new version is recorded so
// every future run dispatches deterministically off the stamp rather than
// re-inspecting on-disk artifacts.
//
// v0 -> v1 is intentionally a no-op transform: the per-sandbox file shapes
// were already current before this stamp existed (the meta.json ->
// environment.json family of renames predates it), so reaching v1 only
// means recording the stamp. Embedders (daemon, HTTP server) get this for
// free; the flat -> namespaced directory move is a CLI-side concern handled
// above the library, since it restructures the dir above the library's root.
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
