// ABOUTME: Tests for System.Prune host-side classification (known /
// ABOUTME: never-init / corrupt-trash / data-bearing-refuse), trash quarantine,
// ABOUTME: and EmptyTrash.

package yoloai

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkSandboxDir creates DataDir/sandboxes/<name>/ and returns its path.
func mkSandboxDir(t *testing.T, c *System, name string) string {
	t.Helper()
	dir := c.layout.SandboxDir(name)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	return dir
}

// writeEnv writes environment.json content into a sandbox dir.
func writeEnv(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "environment.json"), []byte(content), 0o600))
}

func findItem(items []PruneItem, kind PruneItemKind, name string) bool {
	for _, it := range items {
		if it.Kind == kind && it.Name == name {
			return true
		}
	}
	return false
}

func findRefused(rs []RefusedSandbox, name string) bool {
	for _, r := range rs {
		if r.Name == name {
			return true
		}
	}
	return false
}

func findTrashed(ts []TrashedSandbox, name string) bool {
	for _, tt := range ts {
		if tt.Name == name {
			return true
		}
	}
	return false
}

// TestPrune_ClassifiesSandboxDirs verifies the four-way classification on a
// dry run: known (untouched), never-init (delete), corrupt-no-data (trash),
// data-bearing (refuse).
func TestPrune_ClassifiesSandboxDirs(t *testing.T) {
	c := newTestClient(t)

	// known: valid metadata.
	good := mkSandboxDir(t, c, "good")
	writeEnv(t, good, `{"version":1}`)

	// never-init: no metadata, no work dir.
	mkSandboxDir(t, c, "neverinit")

	// corrupt: unparseable metadata, no work dir.
	corrupt := mkSandboxDir(t, c, "corrupt")
	writeEnv(t, corrupt, `{not json`)

	// version-too-new: parseable but migrate rejects, no work dir.
	toonew := mkSandboxDir(t, c, "toonew")
	writeEnv(t, toonew, `{"version":99}`)

	// data-bearing: overlay upper/ with content (host-side, no container needed).
	dirty := mkSandboxDir(t, c, "dirty")
	require.NoError(t, os.MkdirAll(filepath.Join(dirty, "work", "proj", "upper"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dirty, "work", "proj", "upper", "f"), []byte("x"), 0o600))

	res, err := c.Prune(context.Background(), PruneOptions{DryRun: true})
	require.NoError(t, err)

	assert.True(t, findItem(res.RemovedItems, PruneKindSandboxDir, "neverinit"), "never-init dir should be slated for delete")
	assert.True(t, findTrashed(res.Trashed, "corrupt"), "corrupt-no-data should be quarantined")
	assert.True(t, findTrashed(res.Trashed, "toonew"), "version-too-new no-data should be quarantined")
	assert.True(t, findRefused(res.RefusedDataBearing, "dirty"), "data-bearing dir should be refused")

	// "good" must not appear in any removal/trash/refuse list.
	assert.False(t, findItem(res.RemovedItems, PruneKindSandboxDir, "good"))
	assert.False(t, findTrashed(res.Trashed, "good"))
	assert.False(t, findRefused(res.RefusedDataBearing, "good"))

	// Dry run must not mutate the filesystem.
	assert.DirExists(t, c.layout.SandboxDir("neverinit"))
	assert.DirExists(t, c.layout.SandboxDir("corrupt"))
}

// TestPrune_ExecutesClassifications verifies the actual (non-dry-run) prune
// deletes never-init dirs, quarantines corrupt dirs to trash, and leaves
// data-bearing dirs untouched.
func TestPrune_ExecutesClassifications(t *testing.T) {
	c := newTestClient(t)

	mkSandboxDir(t, c, "neverinit")
	corrupt := mkSandboxDir(t, c, "corrupt")
	writeEnv(t, corrupt, `{bad`)
	dirty := mkSandboxDir(t, c, "dirty")
	require.NoError(t, os.MkdirAll(filepath.Join(dirty, "work", "proj", "upper"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dirty, "work", "proj", "upper", "f"), []byte("x"), 0o600))

	res, err := c.Prune(context.Background(), PruneOptions{DryRun: false})
	require.NoError(t, err)

	assert.NoDirExists(t, c.layout.SandboxDir("neverinit"), "never-init dir removed")
	assert.NoDirExists(t, c.layout.SandboxDir("corrupt"), "corrupt dir moved out of sandboxes")
	assert.DirExists(t, filepath.Join(c.layout.TrashDir(), "corrupt"), "corrupt dir quarantined to trash")
	assert.DirExists(t, c.layout.SandboxDir("dirty"), "data-bearing dir left untouched")

	assert.Equal(t, 1, res.TrashContents.Count, "trash summary reflects the quarantined dir")
}

// TestEmptyTrash_RemovesAll verifies EmptyTrash deletes all trash entries.
func TestEmptyTrash_RemovesAll(t *testing.T) {
	c := newTestClient(t)

	require.NoError(t, os.MkdirAll(filepath.Join(c.layout.TrashDir(), "a"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(c.layout.TrashDir(), "b"), 0o750))

	removed, _, err := c.EmptyTrash()
	require.NoError(t, err)
	assert.Equal(t, 2, removed)

	entries, err := os.ReadDir(c.layout.TrashDir())
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// TestEmptyTrash_NoTrashDirIsNoOp verifies EmptyTrash is a no-op when the
// trash dir does not exist.
func TestEmptyTrash_NoTrashDirIsNoOp(t *testing.T) {
	c := newTestClient(t)
	removed, freed, err := c.EmptyTrash()
	require.NoError(t, err)
	assert.Zero(t, removed)
	assert.Zero(t, freed)
}
