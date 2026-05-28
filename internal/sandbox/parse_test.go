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
		expectedMode  DirMode
		expectedForce bool
	}{
		{"bare path", app, "", false},
		{"copy suffix", app + ":copy", DirModeCopy, false},
		{"rw suffix", app + ":rw", DirModeRW, false},
		{"force suffix", app + ":force", "", true},
		{"overlay suffix", app + ":overlay", DirModeOverlay, false},
		{"rw and force", app + ":rw:force", DirModeRW, true},
		{"force and copy", app + ":force:copy", DirModeCopy, true},
		{"copy and force", app + ":copy:force", DirModeCopy, true},
		{"force and rw", app + ":force:rw", DirModeRW, true},
		{"overlay and force", app + ":overlay:force", DirModeOverlay, true},
		{"force and overlay", app + ":force:overlay", DirModeOverlay, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseDirArg(tt.input, "/home/user", nil)
			require.NoError(t, err)
			assert.Equal(t, app, result.Path)
			assert.Equal(t, tt.expectedMode, result.Mode)
			assert.Equal(t, tt.expectedForce, result.Force)
		})
	}
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
	assert.Equal(t, DirMode("copy"), result.Mode)
}

func TestParseDirArg_EnvVarExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	env := map[string]string{"HOME": home}
	result, err := ParseDirArg("${HOME}/somedir:copy", "/home/user", env)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "somedir"), result.Path)
	assert.Equal(t, DirMode("copy"), result.Mode)
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
	assert.Equal(t, DirMode(""), result.Mode)
	assert.False(t, result.Force)

	// Known suffix after unknown colons.
	result, err = ParseDirArg("/path/to/file:with:copy", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, "/path/to/file:with", result.Path)
	assert.Equal(t, DirMode("copy"), result.Mode)
}

func TestParseDirArg_MountPath(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	require.NoError(t, os.MkdirAll(app, 0750))

	tests := []struct {
		name              string
		input             string
		expectedPath      string
		expectedMountPath string
		expectedMode      DirMode
		expectedForce     bool
	}{
		{"mount path only", app + "=/opt/app", app, "/opt/app", "", false},
		{"mount path with rw", app + ":rw=/opt/app", app, "/opt/app", DirModeRW, false},
		{"mount path with copy and force", app + ":copy:force=/opt/app", app, "/opt/app", DirModeCopy, true},
		{"no mount path", app, app, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseDirArg(tt.input, "/home/user", nil)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedPath, result.Path)
			assert.Equal(t, tt.expectedMountPath, result.MountPath)
			assert.Equal(t, tt.expectedMode, result.Mode)
			assert.Equal(t, tt.expectedForce, result.Force)
		})
	}
}

func TestParseDirArg_DoubleColon(t *testing.T) {
	// Input with trailing "::" — the empty suffix after the last colon is
	// not a known suffix, so both colons become part of the path.
	result, err := ParseDirArg("/tmp/test::", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/test::", result.Path)
	assert.Equal(t, DirMode(""), result.Mode)
	assert.False(t, result.Force)
}

func TestParseDirArg_TrailingColon(t *testing.T) {
	// Input with trailing ":" — the empty suffix is not a known suffix,
	// so the colon becomes part of the path.
	result, err := ParseDirArg("/tmp/test:", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/test:", result.Path)
	assert.Equal(t, DirMode(""), result.Mode)
	assert.False(t, result.Force)
}

func TestDirArg_ResolvedMountPath(t *testing.T) {
	d := &DirSpec{Path: "/host/path", MountPath: "/container/path"}
	assert.Equal(t, "/container/path", d.ResolvedMountPath())

	d2 := &DirSpec{Path: "/host/path"}
	assert.Equal(t, "/host/path", d2.ResolvedMountPath())
}

// Q-U: aux dirs no longer support :copy or :overlay; only :rw and the
// default :ro. ParseAuxDirArg enforces this with a UsageError so the
// CLI can pass the message through without wrapping.
func TestParseAuxDirArg_RejectsCopy(t *testing.T) {
	_, err := ParseAuxDirArg("/tmp/aux:copy", "/home/user", nil)
	require.Error(t, err)
	var usage *UsageError
	require.ErrorAs(t, err, &usage)
	assert.Contains(t, err.Error(), "aux directories cannot use :copy")
	assert.Contains(t, err.Error(), "workdir")
	assert.Contains(t, err.Error(), ":rw")
}

func TestParseAuxDirArg_RejectsOverlay(t *testing.T) {
	_, err := ParseAuxDirArg("/tmp/aux:overlay", "/home/user", nil)
	require.Error(t, err)
	var usage *UsageError
	require.ErrorAs(t, err, &usage)
	assert.Contains(t, err.Error(), "aux directories cannot use :overlay")
}

func TestParseAuxDirArg_AcceptsRW(t *testing.T) {
	d, err := ParseAuxDirArg("/tmp/aux:rw", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, DirMode("rw"), d.Mode)
}

func TestParseAuxDirArg_AcceptsRO_Default(t *testing.T) {
	// no suffix → empty mode; caller defaults to :ro.
	d, err := ParseAuxDirArg("/tmp/aux", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, DirMode(""), d.Mode)
}

func TestParseAuxDirArg_ParseErrorsPassThrough(t *testing.T) {
	// ParseDirArg surfaces its own non-usage errors (e.g. unset env var
	// in path); ParseAuxDirArg must propagate those unchanged.
	_, err := ParseAuxDirArg("${YOLOAI_TEST_NONEXISTENT}/dir", "/home/user", nil)
	require.Error(t, err)
}
