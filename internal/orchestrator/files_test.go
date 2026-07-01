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

func TestWriteReadExchangeFile_RoundTrip(t *testing.T) {
	layout, name := filesTestLayout(t)

	// Write creates the exchange dir and any parent dirs on demand.
	require.NoError(t, WriteExchangeFile(layout, name, "sub/answer.txt", []byte("42")))
	assert.FileExists(t, filepath.Join(FilesDir(layout, name), "sub", "answer.txt"))

	got, err := ReadExchangeFile(layout, name, "sub/answer.txt")
	require.NoError(t, err)
	assert.Equal(t, "42", string(got))

	// Overwrite is allowed (content-oriented write, unlike Import).
	require.NoError(t, WriteExchangeFile(layout, name, "sub/answer.txt", []byte("43")))
	got, err = ReadExchangeFile(layout, name, "sub/answer.txt")
	require.NoError(t, err)
	assert.Equal(t, "43", string(got))
}

func TestReadExchangeFile_NotFound(t *testing.T) {
	layout, name := filesTestLayout(t)
	_, err := ReadExchangeFile(layout, name, "missing.txt")
	require.Error(t, err)
}

func TestWriteReadExchangeFile_TraversalBlocked(t *testing.T) {
	layout, name := filesTestLayout(t)

	err := WriteExchangeFile(layout, name, "../../../etc/passwd", []byte("x"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes exchange directory")

	_, err = ReadExchangeFile(layout, name, "../../../etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes exchange directory")

	// An absolute path is contained back into the exchange dir, not honored.
	_, err = ReadExchangeFile(layout, name, "/etc/passwd")
	require.Error(t, err) // not found inside the exchange dir, never /etc/passwd
}

func TestRemoveExchangeFile_TraversalBlocked(t *testing.T) {
	layout, name := filesTestLayout(t)
	err := RemoveExchangeFile(layout, name, "../../../etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes exchange directory")
}

// TestExchange_PlantedSymlinkRefused simulates the untrusted in-container agent
// planting a symlink in the read-write exchange dir that points at a host secret
// outside the sandbox. Every host-side operation must refuse to follow it rather
// than exfiltrate or overwrite the host file.
func TestExchange_PlantedSymlinkRefused(t *testing.T) {
	layout, name := filesTestLayout(t)
	filesDir := FilesDir(layout, name)
	require.NoError(t, os.MkdirAll(filesDir, 0750))

	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("TOPSECRET"), 0600))

	// Agent plants answer.json -> host secret.
	require.NoError(t, os.Symlink(secret, filepath.Join(filesDir, "answer.json")))

	// Read must not follow the symlink (no exfil).
	_, err := ReadExchangeFile(layout, name, "answer.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")

	// Write must not follow the symlink (no host overwrite); secret stays intact.
	err = WriteExchangeFile(layout, name, "answer.json", []byte("clobbered"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
	got, rerr := os.ReadFile(secret) //nolint:gosec // test path
	require.NoError(t, rerr)
	assert.Equal(t, "TOPSECRET", string(got))

	// Export must not follow the symlink.
	err = ExportFile(context.Background(), layout, name, "answer.json", filepath.Join(t.TempDir(), "out"), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")

	// Remove must not traverse the symlink.
	err = RemoveExchangeFile(layout, name, "answer.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
}

// TestExchange_SymlinkedDirComponentRefused covers the intermediate-component
// vector: a symlinked *directory* inside the exchange dir would let a write or
// MkdirAll escape into the symlink target.
func TestExchange_SymlinkedDirComponentRefused(t *testing.T) {
	layout, name := filesTestLayout(t)
	filesDir := FilesDir(layout, name)
	require.NoError(t, os.MkdirAll(filesDir, 0750))

	outsideDir := t.TempDir()
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(filesDir, "sub")))

	err := WriteExchangeFile(layout, name, "sub/pwned.txt", []byte("x"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
	assert.NoFileExists(t, filepath.Join(outsideDir, "pwned.txt"))
}
