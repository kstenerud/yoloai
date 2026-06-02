// ABOUTME: Tests for plain-int schema versioning, the shared RealmStatus
// ABOUTME: check, and CreateFreshLibrary initialization.

package config

import (
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
