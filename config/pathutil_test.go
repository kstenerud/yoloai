package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ExpandPath tests

func TestExpandPath_KnownVar(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	result, err := ExpandPath("${HOME}/projects")
	require.NoError(t, err)
	assert.Equal(t, home+"/projects", result)
}

func TestExpandPath_ChainedVars(t *testing.T) {
	t.Setenv("VAR1", "/first")
	t.Setenv("VAR2", "second")

	result, err := ExpandPath("${VAR1}/${VAR2}")
	require.NoError(t, err)
	assert.Equal(t, "/first/second", result)
}

func TestExpandPath_BareVarLiteral(t *testing.T) {
	t.Setenv("VAR", "expanded")

	result, err := ExpandPath("$VAR/path")
	require.NoError(t, err)
	assert.Equal(t, "$VAR/path", result, "bare $VAR must not be expanded")
}

func TestExpandPath_UnsetVar(t *testing.T) {
	_, err := ExpandPath("${YOLOAI_TEST_UNSET_VAR_XYZ}/path")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "YOLOAI_TEST_UNSET_VAR_XYZ")
	assert.Contains(t, err.Error(), "not set")
}

func TestExpandPath_UnclosedBrace(t *testing.T) {
	_, err := ExpandPath("${UNCLOSED")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unclosed")
}

func TestExpandPath_TildeAndVar(t *testing.T) {
	t.Setenv("MYDIR", "projects")
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	result, err := ExpandPath("~/${MYDIR}/foo")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "projects/foo"), result)
}

func TestExpandPath_NoVars(t *testing.T) {
	result, err := ExpandPath("/plain/path")
	require.NoError(t, err)
	assert.Equal(t, "/plain/path", result)
}

func TestExpandPath_Empty(t *testing.T) {
	result, err := ExpandPath("")
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestExpandPath_EmptyVarName(t *testing.T) {
	_, err := ExpandPath("${}/path")
	assert.Error(t, err, "empty var name should error")
}

func TestExpandPath_SetButEmpty(t *testing.T) {
	t.Setenv("EMPTY_VAR", "")

	result, err := ExpandPath("/prefix/${EMPTY_VAR}/suffix")
	require.NoError(t, err)
	assert.Equal(t, "/prefix//suffix", result, "set-but-empty var should expand to empty string")
}

func TestExpandPath_ValueContainsDollarBrace(t *testing.T) {
	t.Setenv("TRICKY", "has${NESTED}inside")

	result, err := ExpandPath("/start/${TRICKY}/end")
	require.NoError(t, err)
	assert.Equal(t, "/start/has${NESTED}inside/end", result, "must not re-expand values")
}

func TestExpandPath_AdjacentVars(t *testing.T) {
	t.Setenv("AA", "hello")
	t.Setenv("BB", "world")

	result, err := ExpandPath("${AA}${BB}")
	require.NoError(t, err)
	assert.Equal(t, "helloworld", result)
}

// ExpandTilde tests

func TestExpandTilde_Home(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(home, ".config"), ExpandTilde("~/.config"))
}

func TestExpandTilde_NoTilde(t *testing.T) {
	assert.Equal(t, "/usr/local/bin", ExpandTilde("/usr/local/bin"))
}

func TestExpandTilde_TildeOnly(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	// "~" with nothing after → just home dir
	assert.Equal(t, home, ExpandTilde("~"))
}

func TestExpandTilde_Relative(t *testing.T) {
	// No tilde → returned unchanged
	assert.Equal(t, "relative/path", ExpandTilde("relative/path"))
}
