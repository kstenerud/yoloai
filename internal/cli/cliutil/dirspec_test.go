package cliutil

import (
	"os"
	"path/filepath"
	"testing"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDirArg_Modes(t *testing.T) {
	// Use a real temp dir so filepath.Abs resolves consistently.
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	require.NoError(t, os.MkdirAll(app, 0750))

	tests := []struct {
		name                       string
		input                      string
		expectedMode               yoloai.DirMode
		expectedAllowDangerousPath bool
	}{
		{"bare path", app, "", false},
		{"copy suffix", app + ":copy", yoloai.DirModeCopy, false},
		{"rw suffix", app + ":rw", yoloai.DirModeRW, false},
		{"force suffix", app + ":force", "", true},
		{"overlay suffix", app + ":overlay", yoloai.DirModeOverlay, false},
		{"rw and force", app + ":rw:force", yoloai.DirModeRW, true},
		{"force and copy", app + ":force:copy", yoloai.DirModeCopy, true},
		{"copy and force", app + ":copy:force", yoloai.DirModeCopy, true},
		{"force and rw", app + ":force:rw", yoloai.DirModeRW, true},
		{"overlay and force", app + ":overlay:force", yoloai.DirModeOverlay, true},
		{"force and overlay", app + ":force:overlay", yoloai.DirModeOverlay, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseDirArg(tt.input, "/home/user", nil)
			require.NoError(t, err)
			assert.Equal(t, app, result.Path)
			assert.Equal(t, tt.expectedMode, result.Mode)
			assert.Equal(t, tt.expectedAllowDangerousPath, result.AllowDangerousPath)
		})
	}
}

func TestParseDirArg_CopyAll(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	require.NoError(t, os.MkdirAll(app, 0750))

	// :copy-all is mode copy with the gitignore-honoring opt-out set.
	result, err := ParseDirArg(app+":copy-all", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, yoloai.DirModeCopy, result.Mode)
	assert.True(t, result.IncludeIgnored, ":copy-all copies gitignored files")

	// plain :copy honors .gitignore (default).
	result, err = ParseDirArg(app+":copy", "/home/user", nil)
	require.NoError(t, err)
	assert.False(t, result.IncludeIgnored)

	// conflicts with the other modes, like :copy.
	_, err = ParseDirArg(app+":copy-all:rw", "/home/user", nil)
	require.Error(t, err)
}

func TestParseDirArg_ConflictingModes(t *testing.T) {
	tests := []struct {
		input   string
		errFrag string
	}{
		{"/tmp/app:copy:rw", "cannot combine"},
		{"/tmp/app:rw:copy", "cannot combine"},
		{"/tmp/app:copy:force:rw", "cannot combine"},
		{"/tmp/app:overlay:copy", "cannot combine"},
		{"/tmp/app:copy:overlay", "cannot combine"},
		{"/tmp/app:overlay:rw", "cannot combine"},
		{"/tmp/app:rw:overlay", "cannot combine"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := ParseDirArg(tt.input, "/home/user", nil)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errFrag)
		})
	}
}

func TestParseDirArg_AbsolutePath(t *testing.T) {
	// Absolute path is preserved.
	result, err := ParseDirArg("/tmp/absolute", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/absolute", result.Path)

	// Relative path is resolved to absolute.
	result, err = ParseDirArg("relative/path", "/home/user", nil)
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(result.Path))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cwd, "relative/path"), result.Path)
}

func TestParseDirArg_TildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	result, err := ParseDirArg("~/somedir:copy", home, nil)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "somedir"), result.Path)
	assert.Equal(t, yoloai.DirMode("copy"), result.Mode)
}

func TestParseDirArg_EnvVarExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	env := map[string]string{"HOME": home}
	result, err := ParseDirArg("${HOME}/somedir:copy", "/home/user", env)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "somedir"), result.Path)
	assert.Equal(t, yoloai.DirMode("copy"), result.Mode)
}

func TestParseDirArg_EnvVarUnset(t *testing.T) {
	_, err := ParseDirArg("${YOLOAI_TEST_NONEXISTENT}/dir:copy", "/home/user", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expand path")
}

func TestParseDirArg_PathWithColons(t *testing.T) {
	// Unknown suffixes stay as part of the path.
	result, err := ParseDirArg("/path/to/file:with:colons", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, "/path/to/file:with:colons", result.Path)
	assert.Equal(t, yoloai.DirMode(""), result.Mode)
	assert.False(t, result.AllowDangerousPath)

	// Known suffix after unknown colons.
	result, err = ParseDirArg("/path/to/file:with:copy", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, "/path/to/file:with", result.Path)
	assert.Equal(t, yoloai.DirMode("copy"), result.Mode)
}

func TestParseDirArg_MountPath(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	require.NoError(t, os.MkdirAll(app, 0750))

	tests := []struct {
		name                       string
		input                      string
		expectedPath               string
		expectedMountPath          string
		expectedMode               yoloai.DirMode
		expectedAllowDangerousPath bool
	}{
		{"mount path only", app + "=/opt/app", app, "/opt/app", "", false},
		{"mount path with rw", app + ":rw=/opt/app", app, "/opt/app", yoloai.DirModeRW, false},
		{"mount path with copy and force", app + ":copy:force=/opt/app", app, "/opt/app", yoloai.DirModeCopy, true},
		{"no mount path", app, app, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseDirArg(tt.input, "/home/user", nil)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedPath, result.Path)
			assert.Equal(t, tt.expectedMountPath, result.MountPath)
			assert.Equal(t, tt.expectedMode, result.Mode)
			assert.Equal(t, tt.expectedAllowDangerousPath, result.AllowDangerousPath)
		})
	}
}

func TestParseDirArg_DoubleColon(t *testing.T) {
	// Input with trailing "::" — the empty suffix after the last colon is
	// not a known suffix, so both colons become part of the path.
	result, err := ParseDirArg("/tmp/test::", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/test::", result.Path)
	assert.Equal(t, yoloai.DirMode(""), result.Mode)
	assert.False(t, result.AllowDangerousPath)
}

func TestParseDirArg_TrailingColon(t *testing.T) {
	// Input with trailing ":" — the empty suffix is not a known suffix,
	// so the colon becomes part of the path.
	result, err := ParseDirArg("/tmp/test:", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/test:", result.Path)
	assert.Equal(t, yoloai.DirMode(""), result.Mode)
	assert.False(t, result.AllowDangerousPath)
}

func TestDirArg_ResolvedMountPath(t *testing.T) {
	d := &yoloai.DirSpec{Path: "/host/path", MountPath: "/container/path"}
	assert.Equal(t, "/container/path", d.ResolvedMountPath())

	d2 := &yoloai.DirSpec{Path: "/host/path"}
	assert.Equal(t, "/host/path", d2.ResolvedMountPath())
}

// D81 (multi-workdir Phase 2): aux :copy and :overlay are now accepted.
// ParseAuxDirArg no longer rejects them.
func TestParseAuxDirArg_AcceptsCopy(t *testing.T) {
	d, err := ParseAuxDirArg("/tmp/aux:copy", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, yoloai.DirModeCopy, d.Mode)
}

func TestParseAuxDirArg_AcceptsOverlay(t *testing.T) {
	d, err := ParseAuxDirArg("/tmp/aux:overlay", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, yoloai.DirModeOverlay, d.Mode)
}

func TestParseAuxDirArg_AcceptsRW(t *testing.T) {
	d, err := ParseAuxDirArg("/tmp/aux:rw", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, yoloai.DirMode("rw"), d.Mode)
}

func TestParseAuxDirArg_AcceptsRO_Default(t *testing.T) {
	// no suffix → empty mode; caller defaults to :ro.
	d, err := ParseAuxDirArg("/tmp/aux", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, yoloai.DirMode(""), d.Mode)
}

func TestParseAuxDirArg_ParseErrorsPassThrough(t *testing.T) {
	// ParseDirArg surfaces its own non-usage errors (e.g. unset env var
	// in path); ParseAuxDirArg must propagate those unchanged.
	_, err := ParseAuxDirArg("${YOLOAI_TEST_NONEXISTENT}/dir", "/home/user", nil)
	require.Error(t, err)
}
