// ABOUTME: PruneToFileSet is the half of a reset that copying cannot do —
// ABOUTME: removing the agent's additions, what the source dropped, and any
// ABOUTME: secret an older, laxer sync leaked into the work copy (DF117).
package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPruneToFileSet_RemovesWhatTheSetDoesNotName(t *testing.T) {
	dst := t.TempDir()
	write(t, filepath.Join(dst, "a.txt"), "keep")
	write(t, filepath.Join(dst, "sub", "b.txt"), "keep")
	write(t, filepath.Join(dst, "agent-new.txt"), "the agent made this")
	write(t, filepath.Join(dst, "sub", "agent-sub.txt"), "and this")

	require.NoError(t, PruneToFileSet(dst, []string{"a.txt", filepath.Join("sub", "b.txt")}))

	assert.True(t, exists(filepath.Join(dst, "a.txt")))
	assert.True(t, exists(filepath.Join(dst, "sub", "b.txt")))
	assert.True(t, exists(filepath.Join(dst, "sub")), "a parent of a kept file is not junk")
	assert.False(t, exists(filepath.Join(dst, "agent-new.txt")), "reset discards the agent's work")
	assert.False(t, exists(filepath.Join(dst, "sub", "agent-sub.txt")))
}

// The DF117 case: a secret an older reset leaked into the work copy must be
// removed by a later one, not preserved by it. Pruning against the set the copy
// was built from is what makes that true without anyone enumerating secrets.
func TestPruneToFileSet_RemovesAPreviouslyLeakedSecret(t *testing.T) {
	dst := t.TempDir()
	write(t, filepath.Join(dst, "app.js"), "code")
	write(t, filepath.Join(dst, ".env"), "AWS_SECRET_KEY=hunter2")
	write(t, filepath.Join(dst, "node_modules", "dep.js"), "artifact")

	require.NoError(t, PruneToFileSet(dst, []string{"app.js"}))

	assert.False(t, exists(filepath.Join(dst, ".env")), "a leaked secret must not survive a reset")
	assert.False(t, exists(filepath.Join(dst, "node_modules")), "nor a re-imported artifact tree")
	assert.True(t, exists(filepath.Join(dst, "app.js")))
}

// A filename containing rsync wildcard characters is just a name here: the prune
// compares paths, never patterns, which is the property that made dropping rsync
// worth it (DF117).
func TestPruneToFileSet_TreatsWildcardNamesLiterally(t *testing.T) {
	dst := t.TempDir()
	write(t, filepath.Join(dst, "weird[1].txt"), "keep me")
	write(t, filepath.Join(dst, "weird1.txt"), "delete me")
	write(t, filepath.Join(dst, "star*.txt"), "keep me too")

	require.NoError(t, PruneToFileSet(dst, []string{"weird[1].txt", "star*.txt"}))

	assert.True(t, exists(filepath.Join(dst, "weird[1].txt")), "a bracket is a character, not a class")
	assert.True(t, exists(filepath.Join(dst, "star*.txt")))
	assert.False(t, exists(filepath.Join(dst, "weird1.txt")), "and it must not have matched this one")
}

// .git is the caller's to replace as a unit; a prune that walked into it would
// destroy the baseline repo create just built (DF118).
func TestPruneToFileSet_LeavesGitAlone(t *testing.T) {
	dst := t.TempDir()
	write(t, filepath.Join(dst, "app.js"), "code")
	write(t, filepath.Join(dst, ".git", "HEAD"), "ref: refs/heads/main")
	write(t, filepath.Join(dst, ".git", "objects", "ab", "cdef"), "object")

	require.NoError(t, PruneToFileSet(dst, []string{"app.js"}))

	assert.True(t, exists(filepath.Join(dst, ".git", "HEAD")), "the work copy's repo is not project junk")
	assert.True(t, exists(filepath.Join(dst, ".git", "objects", "ab", "cdef")))
}

func TestPruneToFileSet_RemovesEmptyDirNotInSet(t *testing.T) {
	dst := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dst, "gone"), 0o750))
	write(t, filepath.Join(dst, "kept", "f.txt"), "x")

	require.NoError(t, PruneToFileSet(dst, []string{filepath.Join("kept", "f.txt")}))

	assert.False(t, exists(filepath.Join(dst, "gone")))
	assert.True(t, exists(filepath.Join(dst, "kept", "f.txt")))
}

// An empty directory the set names explicitly (:copy-all preserves them) must
// survive, even though nothing inside it accounts for it.
func TestPruneToFileSet_KeepsEmptyDirNamedInSet(t *testing.T) {
	dst := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dst, "emptydir"), 0o750))

	require.NoError(t, PruneToFileSet(dst, []string{"emptydir"}))

	assert.True(t, exists(filepath.Join(dst, "emptydir")))
}

func TestPruneToFileSet_EmptySetClearsEverythingButGit(t *testing.T) {
	dst := t.TempDir()
	write(t, filepath.Join(dst, "a.txt"), "x")
	write(t, filepath.Join(dst, "sub", "b.txt"), "y")
	write(t, filepath.Join(dst, ".git", "HEAD"), "ref: refs/heads/main")

	require.NoError(t, PruneToFileSet(dst, nil))

	assert.False(t, exists(filepath.Join(dst, "a.txt")))
	assert.False(t, exists(filepath.Join(dst, "sub")))
	assert.True(t, exists(filepath.Join(dst, ".git", "HEAD")))
}
