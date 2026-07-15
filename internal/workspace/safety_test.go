// ABOUTME: Guardrails against unsafe workdir/aux-dir configuration: flagging
// ABOUTME: home/root/system dirs as dangerous mount targets, and rejecting
// ABOUTME: overlapping (identical or parent/child) directory sets.
package workspace

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsDangerousDir_Home(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.True(t, IsDangerousDir(home, home))
}

func TestIsDangerousDir_Root(t *testing.T) {
	assert.True(t, IsDangerousDir("/", "/home/user"))
}

func TestIsDangerousDir_SystemDirs(t *testing.T) {
	systemDirs := []string{
		"/usr", "/etc", "/var", "/boot", "/bin", "/sbin", "/lib",
		"/System", "/Library", "/Applications",
	}
	for _, dir := range systemDirs {
		assert.True(t, IsDangerousDir(dir, "/home/user"), "expected %s to be dangerous", dir)
	}
}

func TestIsDangerousDir_SafeDir(t *testing.T) {
	assert.False(t, IsDangerousDir("/tmp/myproject", "/home/user"))
}

func TestCheckPathOverlap_NoOverlap(t *testing.T) {
	err := CheckPathOverlap([]string{"/a", "/b", "/c"})
	assert.NoError(t, err)
}

func TestCheckPathOverlap_ParentChild(t *testing.T) {
	err := CheckPathOverlap([]string{"/a", "/a/b"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/a")
	assert.Contains(t, err.Error(), "/a/b")
}

func TestCheckPathOverlap_Identical(t *testing.T) {
	err := CheckPathOverlap([]string{"/a", "/a"})
	assert.Error(t, err)
}

func TestCheckPathOverlap_DisjointSimilarNames(t *testing.T) {
	err := CheckPathOverlap([]string{"/abc", "/ab"})
	assert.NoError(t, err, "/ab is not a parent of /abc")
}
