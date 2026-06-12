// ABOUTME: Unit tests for the MCP proxy's pure helpers — placeholder expansion
// ABOUTME: in inner-command args ({workdir}/{files}/{cache}/{dir:N}).

package mcpsrv

import (
	"testing"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(hostFS bool, workdir string, auxDirs ...string) *yoloai.Environment {
	dirs := make([]yoloai.DirInfo, 0, 1+len(auxDirs))
	dirs = append(dirs, yoloai.DirInfo{MountPath: workdir})
	for _, d := range auxDirs {
		dirs = append(dirs, yoloai.DirInfo{MountPath: d})
	}
	return &yoloai.Environment{
		HostFilesystem: hostFS,
		Dirs:           dirs,
	}
}

func TestExpandCmd_ContainerPaths(t *testing.T) {
	// HostFilesystem=false → fixed in-container paths for {files}/{cache}.
	got, err := expandCmd(
		[]string{"tool", "--in", "{workdir}/x", "--f", "{files}a", "--c", "{cache}b"},
		"/host/files", "/host/cache",
		env(false, "/yoloai/work"),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"tool", "--in", "/yoloai/work/x", "--f", "/yoloai/files/a", "--c", "/yoloai/cache/b"}, got)
}

func TestExpandCmd_HostFilesystemUsesHostDirs(t *testing.T) {
	// HostFilesystem=true → {files}/{cache} resolve to the on-host dirs; {workdir}
	// is always MountPath.
	got, err := expandCmd(
		[]string{"{workdir}", "{files}", "{cache}"},
		"/host/files", "/host/cache",
		env(true, "/host/work"),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"/host/work", "/host/files", "/host/cache"}, got)
}

func TestExpandCmd_AuxDirByIndex(t *testing.T) {
	got, err := expandCmd(
		[]string{"{dir:0}", "{dir:1}"},
		"", "",
		env(false, "/w", "/aux0", "/aux1"),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"/aux0", "/aux1"}, got)
}

func TestExpandCmd_MultiplePlaceholdersInOneArg(t *testing.T) {
	got, err := expandCmd(
		[]string{"{dir:0}:{dir:1}:{workdir}"},
		"", "",
		env(false, "/w", "/aux0", "/aux1"),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"/aux0:/aux1:/w"}, got)
}

func TestExpandCmd_DirIndexOutOfRange(t *testing.T) {
	_, err := expandCmd([]string{"{dir:2}"}, "", "", env(false, "/w", "/aux0"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestExpandCmd_DirIndexNegative(t *testing.T) {
	_, err := expandCmd([]string{"{dir:-1}"}, "", "", env(false, "/w", "/aux0"))
	require.Error(t, err, "negative index must be rejected, not silently used")
}

func TestExpandCmd_DirIndexNonNumeric(t *testing.T) {
	_, err := expandCmd([]string{"{dir:abc}"}, "", "", env(false, "/w", "/aux0"))
	require.Error(t, err)
}

func TestExpandCmd_DirIndexEmptyDirs(t *testing.T) {
	_, err := expandCmd([]string{"{dir:0}"}, "", "", env(false, "/w"))
	require.Error(t, err, "any {dir:N} on a sandbox with no aux dirs is out of range")
}

func TestExpandCmd_UnclosedDirPlaceholderPassesThrough(t *testing.T) {
	// No closing brace → not a valid placeholder; left verbatim, no error.
	got, err := expandCmd([]string{"{dir:0 oops"}, "", "", env(false, "/w", "/aux0"))
	require.NoError(t, err)
	assert.Equal(t, []string{"{dir:0 oops"}, got)
}

func TestExpandCmd_NoPlaceholdersUnchanged(t *testing.T) {
	in := []string{"plain", "--flag", "value"}
	got, err := expandCmd(in, "", "", env(false, "/w"))
	require.NoError(t, err)
	assert.Equal(t, in, got)
}

func TestExpandCmd_EmptyCmd(t *testing.T) {
	got, err := expandCmd(nil, "", "", env(false, "/w"))
	require.NoError(t, err)
	assert.Empty(t, got)
}
