// ABOUTME: Tests for Handle/Record/OpenDomain persistence abstraction (D87).
package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/store"
)

// testRecord is a minimal Record and Migrator implementation for Handle tests.
type testRecord struct {
	Version int    `json:"version"`
	Value   string `json:"value"`
}

func (r *testRecord) SchemaVersion() int { return 2 }
func (r *testRecord) MigrateRecord() error {
	if r.Version < 1 {
		r.Value = "migrated-from-v0"
		r.Version = 1
	}
	if r.Version < 2 {
		r.Value += "-then-v2"
		r.Version = 2
	}
	return nil
}

func openTestHandle(t *testing.T) store.Handle {
	t.Helper()
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")
	return store.OpenDomain(dir, "test.json", lockPath)
}

// TestHandle_LoadAbsent: file absent → found=false, nil error.
func TestHandle_LoadAbsent(t *testing.T) {
	h := openTestHandle(t)
	r := &testRecord{}
	found, err := h.Load(r)
	require.NoError(t, err)
	assert.False(t, found)
}

// TestHandle_SaveLoad: Save then Load round-trips the record.
func TestHandle_SaveLoad(t *testing.T) {
	h := openTestHandle(t)
	orig := &testRecord{Version: 2, Value: "hello"}
	require.NoError(t, h.Save(orig))

	loaded := &testRecord{}
	found, err := h.Load(loaded)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, orig.Value, loaded.Value)
	assert.Equal(t, 2, loaded.Version)
}

// TestHandle_LoadVersionMatch: on-disk version == SchemaVersion → found=true.
func TestHandle_LoadVersionMatch(t *testing.T) {
	h := openTestHandle(t)
	require.NoError(t, h.Save(&testRecord{Value: "match"}))

	r := &testRecord{}
	found, err := h.Load(r)
	require.NoError(t, err)
	assert.True(t, found)
}

// TestHandle_LoadVersionOlder: on-disk version < SchemaVersion → ErrNeedsMigration.
func TestHandle_LoadVersionOlder(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")
	// Write a version-1 document directly.
	doc := map[string]any{"version": 1, "value": "old"}
	data, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.json"), data, 0600))

	h := store.OpenDomain(dir, "test.json", lockPath)
	r := &testRecord{} // SchemaVersion() == 2
	found, err := h.Load(r)
	assert.False(t, found)
	assert.ErrorIs(t, err, store.ErrNeedsMigration)
}

// TestHandle_LoadVersionNewer: on-disk version > SchemaVersion → ErrTooNew.
func TestHandle_LoadVersionNewer(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")
	// Write a version-99 document directly.
	doc := map[string]any{"version": 99, "value": "future"}
	data, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.json"), data, 0600))

	h := store.OpenDomain(dir, "test.json", lockPath)
	r := &testRecord{}
	found, err := h.Load(r)
	assert.False(t, found)
	assert.ErrorIs(t, err, store.ErrTooNew)
}

// TestHandle_UpdateCASRecheck: version mismatch detected under lock → ErrNeedsMigration.
func TestHandle_UpdateCASRecheck(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")
	// Write a version-1 document directly (older than SchemaVersion 2).
	doc := map[string]any{"version": 1, "value": "stale"}
	data, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.json"), data, 0600))

	h := store.OpenDomain(dir, "test.json", lockPath)
	r := &testRecord{} // expects version 2
	err = h.Update(r, func() error {
		r.Value = "should not happen"
		return nil
	})
	assert.ErrorIs(t, err, store.ErrNeedsMigration)
}

// TestHandle_MigrateNoOp: Migrate on current version is a no-op.
func TestHandle_MigrateNoOp(t *testing.T) {
	h := openTestHandle(t)
	orig := &testRecord{Value: "current"}
	require.NoError(t, h.Save(orig))

	r := &testRecord{}
	require.NoError(t, h.Migrate(r))

	// Value should remain unchanged (no migration ran).
	loaded := &testRecord{}
	found, err := h.Load(loaded)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, orig.Value, loaded.Value)
}

// TestHandle_MigrateOlder: Migrate on older version calls MigrateRecord and writes.
func TestHandle_MigrateOlder(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")
	// Write a version-0 document.
	doc := map[string]any{"version": 0, "value": "original"}
	data, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.json"), data, 0600))

	h := store.OpenDomain(dir, "test.json", lockPath)
	r := &testRecord{}
	require.NoError(t, h.Migrate(r))

	// After migration the file should be at version 2.
	loaded := &testRecord{}
	found, err := h.Load(loaded)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, 2, loaded.Version)
	assert.Equal(t, "migrated-from-v0-then-v2", loaded.Value)
}

// TestHandle_Sub: Sub creates a child-scoped handle that reads/writes its sub-section.
func TestHandle_Sub(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	// Write a root document with a "sub" section at version 2.
	doc := map[string]any{
		"version": 2,
		"value":   "root",
		"sub": map[string]any{
			"version": 2,
			"value":   "child",
		},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.json"), data, 0600))

	root := store.OpenDomain(dir, "test.json", lockPath)
	sub := root.Sub("sub")

	r := &testRecord{}
	found, err := sub.Load(r)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "child", r.Value)
	assert.Equal(t, 2, r.Version)

	// Root handle should still see root-level data.
	rootR := &testRecord{}
	found, err = root.Load(rootR)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "root", rootR.Value)
}

// TestHandle_SubSave: Sub.Save writes only to the sub-section, preserving the root.
func TestHandle_SubSave(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "test.lock")

	root := store.OpenDomain(dir, "test.json", lockPath)
	// Save the root record first.
	require.NoError(t, root.Save(&testRecord{Value: "root-val"}))

	// Now save a sub-section.
	sub := root.Sub("child")
	require.NoError(t, sub.Save(&testRecord{Value: "child-val"}))

	// Root should still be readable.
	rootR := &testRecord{}
	found, err := root.Load(rootR)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "root-val", rootR.Value)

	// Sub should be readable.
	childR := &testRecord{}
	found, err = sub.Load(childR)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "child-val", childR.Value)
}
