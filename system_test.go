// ABOUTME: Tests for the System sub-handle: cross-backend introspection
// ABOUTME: (Info / Backends / Doctor), name validation, and Prune host-side
// ABOUTME: classification (known / never-init / corrupt-trash / data-bearing) + EmptyTrash.

package yoloai

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/yoerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSystemClient_Info verifies paths are derived from the layout and that the
// backend probe returns exactly one status per registered backend (names in
// registration order; unavailable backends carry a reason).
func TestSystemClient_Info(t *testing.T) {
	c := newTestClient(t)

	info, err := c.Info(context.Background())
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, c.layout.YoloaiDir(), info.DataDir)
	assert.Equal(t, c.layout.SandboxesDir(), info.SandboxesDir)
	assert.Equal(t, c.layout.GlobalConfigPath(), info.GlobalConfig)
	assert.Equal(t, c.layout.DefaultsConfigPath(), info.DefaultsConfig)

	descs := runtime.Descriptors()
	require.Len(t, info.Backends, len(descs), "one BackendInfo per registered backend")
	for i, b := range info.Backends {
		assert.Equal(t, descs[i].Type, b.Type, "backend statuses preserve registration order")
		if !b.Available {
			assert.NotEmpty(t, b.Note, "an unavailable backend must explain why")
		}
	}
}

// TestClient_Principal threads ClientCreateOptions.Principal into the layout
// (default "" stays default; a valid segment parses; an invalid one is a
// *UsageError).
func TestClient_Principal(t *testing.T) {
	root := t.TempDir()

	def, err := NewClient(context.Background(), ClientCreateOptions{DataDir: root, HomeDir: root})
	require.NoError(t, err)
	assert.Equal(t, config.PrincipalSegment(""), def.layout.Principal)

	acme, err := NewClient(context.Background(), ClientCreateOptions{DataDir: root, HomeDir: root, Principal: "acme"})
	require.NoError(t, err)
	assert.Equal(t, config.PrincipalSegment("acme"), acme.layout.Principal)

	_, err = NewClient(context.Background(), ClientCreateOptions{DataDir: root, HomeDir: root, Principal: "way-too-long-and-invalid"})
	require.Error(t, err)
	var usageErr *yoerrors.UsageError
	assert.ErrorAs(t, err, &usageErr)
}

// TestSystemClient_ValidateSandboxName accepts a well-formed name and rejects
// path-traversal, with no host state consulted.
func TestSystemClient_ValidateSandboxName(t *testing.T) {
	c := newTestClient(t)
	assert.NoError(t, c.ValidateSandboxName("my-box"))
	assert.Error(t, c.ValidateSandboxName("../escape"))
}

// TestSandbox_MissingReturnsNotFound returns ErrSandboxNotFound for a sandbox
// whose directory does not exist — obtaining the handle IS the existence check.
func TestSandbox_MissingReturnsNotFound(t *testing.T) {
	dir := t.TempDir()
	c, err := NewClient(context.Background(), ClientCreateOptions{DataDir: dir, HomeDir: dir})
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck
	_, err = c.Sandbox("nope")
	assert.ErrorIs(t, err, ErrSandboxNotFound)
}

// TestSystemClient_ListAcrossBackends_Empty verifies a fresh install (no sandbox
// dirs) lists nothing and probes no backends — no enumeration, no error.
func TestSystemClient_ListAcrossBackends_Empty(t *testing.T) {
	c := newTestClient(t)
	infos, unavailable, err := c.ListAcrossBackends(context.Background())
	require.NoError(t, err)
	assert.Empty(t, infos)
	assert.Empty(t, unavailable)
}

// TestSystemClient_Doctor verifies every registered backend produces at least
// one report row (base-mode or init-failure), and that a non-matching backend
// filter yields nothing.
func TestSystemClient_Doctor(t *testing.T) {
	c := newTestClient(t)

	reports, err := c.Doctor(context.Background(), SystemDoctorOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, reports, "every registered backend produces at least one report row")
	for _, r := range reports {
		assert.NotEmpty(t, r.Backend, "each report names its backend")
	}

	none, err := c.Doctor(context.Background(), SystemDoctorOptions{BackendFilter: "does-not-exist"})
	require.NoError(t, err)
	assert.Empty(t, none, "a non-matching backend filter reports nothing")
}

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

	res, err := c.Prune(context.Background(), SystemPruneOptions{DryRun: true})
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

	res, err := c.Prune(context.Background(), SystemPruneOptions{DryRun: false})
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
