package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPruneTempFiles(t *testing.T) {
	// PruneTempFiles scans os.TempDir(), which honors $TMPDIR — point it at a
	// throwaway dir so the test controls exactly what's present and never
	// touches (or deletes real orphans in) the shared system temp dir.
	t.Setenv("TMPDIR", t.TempDir())

	// Create a stale dir (older than maxAge) in the isolated temp dir.
	staleDir, err := os.MkdirTemp("", "yoloai-stale-test-")
	require.NoError(t, err)

	// Age it to 2 hours ago
	past := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(staleDir, past, past))

	// Create a fresh dir (within maxAge)
	freshDir, err := os.MkdirTemp("", "yoloai-fresh-test-")
	require.NoError(t, err)

	// Dry run: should list stale but not remove
	pruned, err := PruneTempFiles(true, 1*time.Hour)
	require.NoError(t, err)
	assert.Contains(t, pruned, staleDir)
	for _, p := range pruned {
		assert.NotEqual(t, freshDir, p)
	}

	// Stale dir should still exist after dry run
	_, err = os.Stat(staleDir)
	assert.NoError(t, err, "stale dir should still exist after dry run")

	// Real prune
	pruned, err = PruneTempFiles(false, 1*time.Hour)
	require.NoError(t, err)
	assert.Contains(t, pruned, staleDir)

	// Stale dir should be gone
	_, err = os.Stat(staleDir)
	assert.True(t, os.IsNotExist(err), "stale dir should be removed after prune")

	// Fresh dir should still exist
	_, err = os.Stat(freshDir)
	assert.NoError(t, err, "fresh dir should still exist after prune")
}

func TestPruneTempFiles_NonDir(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	// Create a file (not dir) with yoloai- prefix — should be skipped
	f, err := os.CreateTemp("", "yoloai-file-test-")
	require.NoError(t, err)
	f.Close() //nolint:errcheck,gosec // test cleanup

	// Age the file
	past := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(f.Name(), past, past))

	pruned, err := PruneTempFiles(true, 1*time.Hour)
	require.NoError(t, err)

	// The file should not appear in pruned (only dirs are pruned)
	for _, p := range pruned {
		assert.NotEqual(t, f.Name(), p)
		// Also check by basename in case of /tmp prefix differences
		assert.NotEqual(t, filepath.Base(f.Name()), filepath.Base(p))
	}
}
