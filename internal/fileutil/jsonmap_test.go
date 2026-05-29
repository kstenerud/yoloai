package fileutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ReadJSONMap / WriteJSONMap tests

func TestReadJSONMap_Exists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"key":"value","num":42}`), 0600))

	m, err := ReadJSONMap(path)
	require.NoError(t, err)
	assert.Equal(t, "value", m["key"])
	assert.Equal(t, float64(42), m["num"])
}

func TestReadJSONMap_Missing(t *testing.T) {
	m, err := ReadJSONMap(filepath.Join(t.TempDir(), "nope.json"))
	require.NoError(t, err)
	assert.Empty(t, m)
}

func TestReadJSONMap_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte(`not json`), 0600))

	_, err := ReadJSONMap(path)
	assert.Error(t, err)
}

func TestWriteJSONMap_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	original := map[string]any{"name": "test", "count": float64(7)}
	require.NoError(t, WriteJSONMap(path, original))

	loaded, err := ReadJSONMap(path)
	require.NoError(t, err)
	assert.Equal(t, original, loaded)
}

func TestWriteJSONMap_Overwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	require.NoError(t, WriteJSONMap(path, map[string]any{"old": true}))
	require.NoError(t, WriteJSONMap(path, map[string]any{"new": true}))

	loaded, err := ReadJSONMap(path)
	require.NoError(t, err)
	assert.Nil(t, loaded["old"])
	assert.Equal(t, true, loaded["new"])
}
