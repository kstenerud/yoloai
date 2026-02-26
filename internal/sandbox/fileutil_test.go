package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expandTilde tests

func TestExpandTilde_Home(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(home, ".config"), expandTilde("~/.config"))
}

func TestExpandTilde_NoTilde(t *testing.T) {
	assert.Equal(t, "/usr/local/bin", expandTilde("/usr/local/bin"))
}

func TestExpandTilde_TildeOnly(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	// "~" with nothing after → just home dir
	assert.Equal(t, home, expandTilde("~"))
}

func TestExpandTilde_Relative(t *testing.T) {
	// No tilde → returned unchanged
	assert.Equal(t, "relative/path", expandTilde("relative/path"))
}

// copyDir tests

func TestCopyDir_Basic(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, src, "file.txt", "hello")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0750))
	writeTestFile(t, src, "sub/nested.txt", "world")

	dst := filepath.Join(t.TempDir(), "copy")
	require.NoError(t, copyDir(src, dst))

	content, err := os.ReadFile(filepath.Join(dst, "file.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))

	content, err = os.ReadFile(filepath.Join(dst, "sub", "nested.txt")) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, "world", string(content))
}

func TestCopyDir_SourceMissing(t *testing.T) {
	err := copyDir("/nonexistent/path", filepath.Join(t.TempDir(), "dst"))
	assert.Error(t, err)
}

// readJSONMap / writeJSONMap tests

func TestReadJSONMap_Exists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"key":"value","num":42}`), 0600))

	m, err := readJSONMap(path)
	require.NoError(t, err)
	assert.Equal(t, "value", m["key"])
	assert.Equal(t, float64(42), m["num"])
}

func TestReadJSONMap_Missing(t *testing.T) {
	m, err := readJSONMap(filepath.Join(t.TempDir(), "nope.json"))
	require.NoError(t, err)
	assert.Empty(t, m)
}

func TestReadJSONMap_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte(`not json`), 0600))

	_, err := readJSONMap(path)
	assert.Error(t, err)
}

func TestWriteJSONMap_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	original := map[string]any{"name": "test", "count": float64(7)}
	require.NoError(t, writeJSONMap(path, original))

	loaded, err := readJSONMap(path)
	require.NoError(t, err)
	assert.Equal(t, original, loaded)
}

func TestWriteJSONMap_Overwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	require.NoError(t, writeJSONMap(path, map[string]any{"old": true}))
	require.NoError(t, writeJSONMap(path, map[string]any{"new": true}))

	loaded, err := readJSONMap(path)
	require.NoError(t, err)
	assert.Nil(t, loaded["old"])
	assert.Equal(t, true, loaded["new"])
}
