// ABOUTME: Tests for host-side file-exchange functions — traversal containment,
// ABOUTME: glob collection, and the import/export/remove round-trip.
package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/store"
)

func TestValidateExchangePath_Valid(t *testing.T) {
	assert.NoError(t, validateExchangePath("/a/b/files", "/a/b/files/foo.txt"))
	assert.NoError(t, validateExchangePath("/a/b/files", "/a/b/files"))
}

func TestValidateExchangePath_Traversal(t *testing.T) {
	assert.Error(t, validateExchangePath("/a/b/files", "/a/b/files/../secret"))
	assert.Error(t, validateExchangePath("/a/b/files", "/etc/passwd"))
}

func TestCollectExchangeGlobs_Deduplicates(t *testing.T) {
	filesDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(filesDir, "a.txt"), []byte(""), 0600))

	names, err := collectExchangeGlobs(filesDir, []string{"*.txt", "a.*"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a.txt"}, names)
}

func TestCollectExchangeGlobs_EmptyOnNoMatch(t *testing.T) {
	filesDir := t.TempDir()

	names, err := collectExchangeGlobs(filesDir, []string{"*.nope"})
	require.NoError(t, err)
	assert.Empty(t, names)
}

// filesTestLayout creates a sandbox directory and returns the layout + name.
func filesTestLayout(t *testing.T) (config.Layout, string) {
	t.Helper()
	tmp := t.TempDir()
	layout := config.NewLayout(filepath.Join(tmp, ".yoloai"))
	name := "box"
	createTestSandbox(t, tmp, name, filepath.Join(tmp, "host"), store.DirModeCopy)
	return layout, name
}

func TestImportExportRemove_RoundTrip(t *testing.T) {
	layout, name := filesTestLayout(t)
	ctx := context.Background()

	src := filepath.Join(t.TempDir(), "hello.txt")
	require.NoError(t, os.WriteFile(src, []byte("hi"), 0600))

	placed, err := ImportFile(ctx, layout, name, src, false)
	require.NoError(t, err)
	assert.Equal(t, "hello.txt", placed)
	assert.FileExists(t, filepath.Join(FilesDir(layout, name), "hello.txt"))

	// Importing again without force is rejected.
	_, err = ImportFile(ctx, layout, name, src, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// Export back out to the host.
	dst := filepath.Join(t.TempDir(), "out.txt")
	require.NoError(t, ExportFile(ctx, layout, name, "hello.txt", dst, false))
	got, err := os.ReadFile(dst) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Equal(t, "hi", string(got))

	// Remove it from the exchange dir.
	require.NoError(t, RemoveExchangeFile(layout, name, "hello.txt"))
	assert.NoFileExists(t, filepath.Join(FilesDir(layout, name), "hello.txt"))
}

func TestExportFile_TraversalBlocked(t *testing.T) {
	layout, name := filesTestLayout(t)
	err := ExportFile(context.Background(), layout, name, "../../../etc/passwd", filepath.Join(t.TempDir(), "x"), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes exchange directory")
}

func TestRemoveExchangeFile_TraversalBlocked(t *testing.T) {
	layout, name := filesTestLayout(t)
	err := RemoveExchangeFile(layout, name, "../../../etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes exchange directory")
}
