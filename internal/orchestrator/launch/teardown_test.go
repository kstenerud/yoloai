// ABOUTME: Tests for forceRemoveAll — verifies read-only nested trees are made
// ABOUTME: writable and removed, and that a missing path is a no-op.
package launch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForceRemoveAll_ReadOnlyNested(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")
	require.NoError(t, os.MkdirAll(nested, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "file.txt"), []byte("content"), 0600))

	// Make everything read-only
	require.NoError(t, os.Chmod(nested, 0o555))                             //nolint:gosec // intentionally read-only for test
	require.NoError(t, os.Chmod(filepath.Dir(nested), 0o555))               //nolint:gosec // intentionally read-only for test
	require.NoError(t, os.Chmod(filepath.Dir(filepath.Dir(nested)), 0o555)) //nolint:gosec // intentionally read-only for test

	err := forceRemoveAll(dir)
	require.NoError(t, err)
	assert.NoDirExists(t, dir)
}

func TestForceRemoveAll_NonExistentPath(t *testing.T) {
	err := forceRemoveAll("/tmp/nonexistent-path-" + t.Name())
	assert.NoError(t, err)
}
