// ABOUTME: Unit tests for ResolveDirSpecifier and SelectTrackedDir — the dir
// ABOUTME: selection helpers used by the diff and apply commands.
package cliutil

import (
	"testing"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeEnv(dirs []yoloai.DirInfo) *yoloai.Environment {
	return &yoloai.Environment{Name: "testbox", Dirs: dirs}
}

func copyDir(hostPath, mountPath string) yoloai.DirInfo {
	return yoloai.DirInfo{HostPath: hostPath, MountPath: mountPath, Mode: yoloai.DirModeCopy}
}

func TestResolveDirSpecifier_ExactHostPath(t *testing.T) {
	env := makeEnv([]yoloai.DirInfo{
		copyDir("/a/b/web", "/mnt/web"),
		copyDir("/a/b/api", "/mnt/api"),
	})
	d, err := ResolveDirSpecifier(env, "/a/b/web")
	require.NoError(t, err)
	assert.Equal(t, "/a/b/web", d.HostPath)
}

func TestResolveDirSpecifier_ExactMountPath(t *testing.T) {
	env := makeEnv([]yoloai.DirInfo{
		copyDir("/a/b/web", "/mnt/web"),
		copyDir("/a/b/api", "/mnt/api"),
	})
	d, err := ResolveDirSpecifier(env, "/mnt/api")
	require.NoError(t, err)
	assert.Equal(t, "/a/b/api", d.HostPath)
}

func TestResolveDirSpecifier_Basename(t *testing.T) {
	env := makeEnv([]yoloai.DirInfo{
		copyDir("/a/b/web", "/mnt/web"),
		copyDir("/a/b/api", "/mnt/api"),
	})
	d, err := ResolveDirSpecifier(env, "web")
	require.NoError(t, err)
	assert.Equal(t, "/a/b/web", d.HostPath)
}

func TestResolveDirSpecifier_SegmentAlignedSuffix(t *testing.T) {
	env := makeEnv([]yoloai.DirInfo{
		copyDir("/a/b/web", "/mnt/web"),
		copyDir("/a/c/api", "/mnt/api"),
	})
	d, err := ResolveDirSpecifier(env, "b/web")
	require.NoError(t, err)
	assert.Equal(t, "/a/b/web", d.HostPath)
}

func TestResolveDirSpecifier_AmbiguousBasename(t *testing.T) {
	env := makeEnv([]yoloai.DirInfo{
		copyDir("/a/web", "/mnt/web1"),
		copyDir("/b/web", "/mnt/web2"),
	})
	_, err := ResolveDirSpecifier(env, "web")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
}

func TestResolveDirSpecifier_NoMatch(t *testing.T) {
	env := makeEnv([]yoloai.DirInfo{
		copyDir("/a/b/web", "/mnt/web"),
	})
	_, err := ResolveDirSpecifier(env, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no tracked dir")
}

func TestSelectTrackedDir_SingleDir(t *testing.T) {
	env := makeEnv([]yoloai.DirInfo{copyDir("/a/web", "/mnt/web")})
	hostPath, selected, rest, err := SelectTrackedDir(env, []string{"abc123"})
	require.NoError(t, err)
	assert.Equal(t, "", hostPath)
	assert.Equal(t, "/a/web", selected.HostPath)
	assert.Equal(t, []string{"abc123"}, rest)
}

func TestSelectTrackedDir_NoDirs(t *testing.T) {
	env := makeEnv(nil)
	hostPath, selected, rest, err := SelectTrackedDir(env, []string{"abc123"})
	require.NoError(t, err)
	assert.Equal(t, "", hostPath)
	assert.Equal(t, yoloai.DirInfo{}, selected)
	assert.Equal(t, []string{"abc123"}, rest)
}

func TestSelectTrackedDir_MultiDirValidSpec(t *testing.T) {
	env := makeEnv([]yoloai.DirInfo{
		copyDir("/a/web", "/mnt/web"),
		copyDir("/a/api", "/mnt/api"),
	})
	hostPath, selected, rest, err := SelectTrackedDir(env, []string{"web", "abc123"})
	require.NoError(t, err)
	assert.Equal(t, "/a/web", hostPath)
	assert.Equal(t, "/a/web", selected.HostPath)
	assert.Equal(t, []string{"abc123"}, rest)
}

func TestSelectTrackedDir_MultiDirNoArgs(t *testing.T) {
	env := makeEnv([]yoloai.DirInfo{
		copyDir("/a/web", "/mnt/web"),
		copyDir("/a/api", "/mnt/api"),
	})
	_, _, _, err := SelectTrackedDir(env, []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 tracked dirs")
}

func TestSelectTrackedDir_MultiDirDashFirst(t *testing.T) {
	env := makeEnv([]yoloai.DirInfo{
		copyDir("/a/web", "/mnt/web"),
		copyDir("/a/api", "/mnt/api"),
	})
	_, _, _, err := SelectTrackedDir(env, []string{"--", "file.go"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tracked dirs")
}
