package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDirArg_Modes(t *testing.T) {
	// Use a real temp dir so filepath.Abs resolves consistently.
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	require.NoError(t, os.MkdirAll(app, 0750))

	tests := []struct {
		name          string
		input         string
		expectedMode  string
		expectedForce bool
	}{
		{"bare path", app, "", false},
		{"copy suffix", app + ":copy", "copy", false},
		{"rw suffix", app + ":rw", "rw", false},
		{"force suffix", app + ":force", "", true},
		{"rw and force", app + ":rw:force", "rw", true},
		{"force and copy", app + ":force:copy", "copy", true},
		{"copy and force", app + ":copy:force", "copy", true},
		{"force and rw", app + ":force:rw", "rw", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseDirArg(tt.input)
			require.NoError(t, err)
			assert.Equal(t, app, result.Path)
			assert.Equal(t, tt.expectedMode, result.Mode)
			assert.Equal(t, tt.expectedForce, result.Force)
		})
	}
}

func TestParseDirArg_ConflictingModes(t *testing.T) {
	tests := []string{
		"/tmp/app:copy:rw",
		"/tmp/app:rw:copy",
		"/tmp/app:copy:force:rw",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := ParseDirArg(input)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "cannot combine :copy and :rw")
		})
	}
}

func TestParseDirArg_AbsolutePath(t *testing.T) {
	// Absolute path is preserved.
	result, err := ParseDirArg("/tmp/absolute")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/absolute", result.Path)

	// Relative path is resolved to absolute.
	result, err = ParseDirArg("relative/path")
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(result.Path))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cwd, "relative/path"), result.Path)
}

func TestParseDirArg_TildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	result, err := ParseDirArg("~/somedir:copy")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "somedir"), result.Path)
	assert.Equal(t, "copy", result.Mode)
}

func TestParseDirArg_PathWithColons(t *testing.T) {
	// Unknown suffixes stay as part of the path.
	result, err := ParseDirArg("/path/to/file:with:colons")
	require.NoError(t, err)
	assert.Equal(t, "/path/to/file:with:colons", result.Path)
	assert.Equal(t, "", result.Mode)
	assert.False(t, result.Force)

	// Known suffix after unknown colons.
	result, err = ParseDirArg("/path/to/file:with:copy")
	require.NoError(t, err)
	assert.Equal(t, "/path/to/file:with", result.Path)
	assert.Equal(t, "copy", result.Mode)
}
