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

	env := map[string]string{"HOME": home}
	result, err := ExpandPath("${HOME}/projects", home, env)
	require.NoError(t, err)
	assert.Equal(t, home+"/projects", result)
}

func TestExpandPath_ChainedVars(t *testing.T) {
	env := map[string]string{"VAR1": "/first", "VAR2": "second"}

	result, err := ExpandPath("${VAR1}/${VAR2}", "/home/user", env)
	require.NoError(t, err)
	assert.Equal(t, "/first/second", result)
}

func TestExpandPath_BareVarLiteral(t *testing.T) {
	env := map[string]string{"VAR": "expanded"}

	result, err := ExpandPath("$VAR/path", "/home/user", env)
	require.NoError(t, err)
	assert.Equal(t, "$VAR/path", result, "bare $VAR must not be expanded")
}

func TestExpandPath_UnsetVar(t *testing.T) {
	_, err := ExpandPath("${YOLOAI_TEST_UNSET_VAR_XYZ}/path", "/home/user", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "YOLOAI_TEST_UNSET_VAR_XYZ")
	assert.Contains(t, err.Error(), "not set")
}

func TestExpandPath_UnclosedBrace(t *testing.T) {
	_, err := ExpandPath("${UNCLOSED", "/home/user", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unclosed")
}

func TestExpandPath_TildeAndVar(t *testing.T) {
	env := map[string]string{"MYDIR": "projects"}
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	result, err := ExpandPath("~/${MYDIR}/foo", home, env)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "projects/foo"), result)
}

func TestExpandPath_NoVars(t *testing.T) {
	result, err := ExpandPath("/plain/path", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, "/plain/path", result)
}

func TestExpandPath_Empty(t *testing.T) {
	result, err := ExpandPath("", "/home/user", nil)
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestExpandPath_EmptyVarName(t *testing.T) {
	_, err := ExpandPath("${}/path", "/home/user", nil)
	assert.Error(t, err, "empty var name should error")
}

func TestExpandPath_SetButEmpty(t *testing.T) {
	env := map[string]string{"EMPTY_VAR": ""}

	result, err := ExpandPath("/prefix/${EMPTY_VAR}/suffix", "/home/user", env)
	require.NoError(t, err)
	assert.Equal(t, "/prefix//suffix", result, "set-but-empty var should expand to empty string")
}

func TestExpandPath_ValueContainsDollarBrace(t *testing.T) {
	env := map[string]string{"TRICKY": "has${NESTED}inside"}

	result, err := ExpandPath("/start/${TRICKY}/end", "/home/user", env)
	require.NoError(t, err)
	assert.Equal(t, "/start/has${NESTED}inside/end", result, "must not re-expand values")
}

func TestExpandPath_AdjacentVars(t *testing.T) {
	env := map[string]string{"AA": "hello", "BB": "world"}

	result, err := ExpandPath("${AA}${BB}", "/home/user", env)
	require.NoError(t, err)
	assert.Equal(t, "helloworld", result)
}

// ExpandTilde tests

func TestExpandTilde_Home(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(home, ".config"), ExpandTilde("~/.config", home))
}

func TestExpandTilde_NoTilde(t *testing.T) {
	assert.Equal(t, "/usr/local/bin", ExpandTilde("/usr/local/bin", "/home/user"))
}

func TestExpandTilde_TildeOnly(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	// "~" with nothing after → just home dir
	assert.Equal(t, home, ExpandTilde("~", home))
}

func TestExpandTilde_Relative(t *testing.T) {
	// No tilde → returned unchanged
	assert.Equal(t, "relative/path", ExpandTilde("relative/path", "/home/user"))
}
