// ABOUTME: Tests for plain-int schema versioning, the shared RealmStatus check,
// ABOUTME: CreateFreshLibrary init, and the MigrateLibrary step engine.

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaVersion_PlainIntRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".schema-version")

	// Missing stamp reads as (0, false, nil).
	v, exists, err := ReadSchemaVersion(path)
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, 0, v)

	require.NoError(t, WriteSchemaVersion(path, 3))

	// On-disk form is a bare integer — no JSON/YAML wrapping.
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is a test temp file
	require.NoError(t, err)
	assert.Equal(t, "3", string(raw))

	v, exists, err = ReadSchemaVersion(path)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, 3, v)
}

func TestReadSchemaVersion_ToleratesWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".schema-version")
	require.NoError(t, os.WriteFile(path, []byte(" 2\n"), 0600))

	v, exists, err := ReadSchemaVersion(path)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, 2, v)
}

func TestReadSchemaVersion_RejectsNonInteger(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".schema-version")
	require.NoError(t, os.WriteFile(path, []byte("{\"version\":1}"), 0600))

	_, _, err := ReadSchemaVersion(path)
	require.Error(t, err)
}

func TestRealmStatus(t *testing.T) {
	t.Run("absent dir is fresh", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		st, err := RealmStatus(dir, 1)
		require.NoError(t, err)
		assert.Equal(t, LayoutFresh, st)
	})

	t.Run("empty dir is fresh", func(t *testing.T) {
		dir := t.TempDir() // exists, no entries
		st, err := RealmStatus(dir, 1)
		require.NoError(t, err)
		assert.Equal(t, LayoutFresh, st)
	})

	t.Run("populated dir below current needs migrate", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, WriteSchemaVersion(SchemaVersionPathFor(dir), 0))
		st, err := RealmStatus(dir, 1)
		require.NoError(t, err)
		assert.Equal(t, LayoutMigrate, st)
	})

	t.Run("populated but unstamped reads as v0 -> migrate", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "stray"), []byte("x"), 0600))
		st, err := RealmStatus(dir, 1)
		require.NoError(t, err)
		assert.Equal(t, LayoutMigrate, st)
	})

	t.Run("at current version is ok", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, WriteSchemaVersion(SchemaVersionPathFor(dir), 1))
		st, err := RealmStatus(dir, 1)
		require.NoError(t, err)
		assert.Equal(t, LayoutOK, st)
	})

	t.Run("newer than build errors", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, WriteSchemaVersion(SchemaVersionPathFor(dir), 2))
		_, err := RealmStatus(dir, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "upgrade yoloai")
	})

	t.Run("non-directory at path is present, not fresh", func(t *testing.T) {
		base := t.TempDir()
		path := filepath.Join(base, "afile")
		require.NoError(t, os.WriteFile(path, []byte("x"), 0600))
		st, err := RealmStatus(path, 1)
		require.NoError(t, err)
		assert.Equal(t, LayoutMigrate, st) // unstamped -> v0 -> migrate
	})
}

func TestCreateFreshLibrary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	layout := NewLayout(dir)

	require.NoError(t, CreateFreshLibrary(layout))

	v, exists, err := ReadSchemaVersion(layout.SchemaVersionPath())
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, LibrarySchemaVersion, v)

	st, err := RealmStatus(dir, LibrarySchemaVersion)
	require.NoError(t, err)
	assert.Equal(t, LayoutOK, st)

	// Idempotent: a second call leaves it at the current version.
	require.NoError(t, CreateFreshLibrary(layout))
	v, _, err = ReadSchemaVersion(layout.SchemaVersionPath())
	require.NoError(t, err)
	assert.Equal(t, LibrarySchemaVersion, v)
}

// An unstamped but populated DataDir reads as version 0: MigrateLibrary walks it
// up to the current version, records the stamp, and must not destroy existing
// content. v0 -> v1 is a no-op transform today; locking the preserve-and-stamp
// guarantee here means a future real transform inherits the expectation.
func TestMigrateLibrary_UnstampedV0_StampsAndPreservesData(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	require.NoError(t, os.MkdirAll(dir, 0750))
	// Pre-existing sandbox data with no stamp — this is what a pre-versioning
	// install looks like on disk.
	marker := filepath.Join(dir, "sandboxes", "keep.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(marker), 0750))
	require.NoError(t, os.WriteFile(marker, []byte("data"), 0600))

	layout := NewLayout(dir)
	require.NoError(t, MigrateLibrary(layout))

	v, exists, err := ReadSchemaVersion(layout.SchemaVersionPath())
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, LibrarySchemaVersion, v)

	st, err := RealmStatus(dir, LibrarySchemaVersion)
	require.NoError(t, err)
	assert.Equal(t, LayoutOK, st)

	assert.FileExists(t, marker, "existing content must survive the migration")
}

func TestMigrateLibrary_AlreadyCurrent_Idempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	layout := NewLayout(dir)
	require.NoError(t, CreateFreshLibrary(layout)) // stamped at current

	require.NoError(t, MigrateLibrary(layout))
	require.NoError(t, MigrateLibrary(layout))

	v, _, err := ReadSchemaVersion(layout.SchemaVersionPath())
	require.NoError(t, err)
	assert.Equal(t, LibrarySchemaVersion, v)
}

func TestMigrateLibrary_NewerThanBuild_Errors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library")
	require.NoError(t, os.MkdirAll(dir, 0750))
	layout := NewLayout(dir)
	require.NoError(t, WriteSchemaVersion(layout.SchemaVersionPath(), LibrarySchemaVersion+1))

	err := MigrateLibrary(layout)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upgrade yoloai")
}

// TestMigrateLibraryStep locks the per-version transform contract so a future
// bump has a pattern to extend: every version in the registered range
// [0, LibrarySchemaVersion) must apply cleanly, and the first unregistered
// version must be refused rather than silently skipped. When you add a
// vN -> vN+1 step, bump LibrarySchemaVersion and add the matching `case N:` to
// migrateLibraryStep — the registered loop then covers the new step
// automatically and the unregistered guard moves up with the constant.
func TestMigrateLibraryStep(t *testing.T) {
	layout := NewLayout(t.TempDir())

	for from := range LibrarySchemaVersion {
		t.Run(fmt.Sprintf("registered_v%d", from), func(t *testing.T) {
			require.NoError(t, migrateLibraryStep(layout, from),
				"every registered step on the path MigrateLibrary walks must succeed")
		})
	}

	t.Run("unregistered_version_errors", func(t *testing.T) {
		err := migrateLibraryStep(layout, LibrarySchemaVersion)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no migration registered")
	})
}
