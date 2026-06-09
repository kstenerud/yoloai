// ABOUTME: Tests for workspace CopyDiff and RWDiff free-function wrappers.
// ABOUTME: Detailed diff logic tests live in internal/git/ops_test.go.
package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- CopyDiff wrapper ---

func TestCopyDiff_Wrapper_NoChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)
	out, err := CopyDiff(testEnv(), dir, sha, nil, false, false)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestCopyDiff_Wrapper_WithChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	sha := headSHA(t, dir)
	writeTestFile(t, dir, "file.txt", "hello world\n")

	out, err := CopyDiff(testEnv(), dir, sha, nil, false, false)
	require.NoError(t, err)
	assert.Contains(t, out, "hello world")
}

// --- RWDiff wrapper ---

func TestRWDiff_Wrapper_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	out, err := RWDiff(testEnv(), dir, nil, false, false)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestRWDiff_Wrapper_WithChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	writeTestFile(t, dir, "file.txt", "hello\n")
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "initial")

	writeTestFile(t, dir, "file.txt", "hello world\n")

	out, err := RWDiff(testEnv(), dir, nil, false, false)
	require.NoError(t, err)
	assert.Contains(t, out, "hello world")
}

// --- helper ---

func headSHA(t *testing.T, dir string) string {
	t.Helper()
	sha, err := HeadSHAWithEnv(testEnv(), dir)
	require.NoError(t, err)
	return sha
}
