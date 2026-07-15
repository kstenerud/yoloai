// ABOUTME: PruneTempFiles age-based sweeping of ~/.yoloai temp dirs: dry-run
// ABOUTME: vs real removal, non-dir entries skipped, and unremovable dirs
// ABOUTME: reported as failed rather than falsely claimed as pruned.
package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstenerud/yoloai/internal/config"
)

// testLayout returns a Layout whose DataDir (and therefore TempDir) is rooted
// in the test's temp dir so PruneTempFiles never touches the real system.
func testLayout(t *testing.T) config.Layout {
	t.Helper()
	return config.Layout{DataDir: t.TempDir()}
}

func TestPruneTempFiles(t *testing.T) {
	layout := testLayout(t)
	tmpRoot := layout.TempDir()
	require.NoError(t, os.MkdirAll(tmpRoot, 0o700))

	// Create a stale dir (older than maxAge) in the isolated temp dir.
	staleDir, err := os.MkdirTemp(tmpRoot, "yoloai-stale-test-")
	require.NoError(t, err)

	// Age it to 2 hours ago
	past := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(staleDir, past, past))

	// Create a fresh dir (within maxAge)
	freshDir, err := os.MkdirTemp(tmpRoot, "yoloai-fresh-test-")
	require.NoError(t, err)

	// Dry run: should list stale but not remove
	pruned, failed, err := PruneTempFiles(layout, true, 1*time.Hour)
	require.NoError(t, err)
	assert.Empty(t, failed, "dry run never attempts removal, so never fails")
	assert.Contains(t, pruned, staleDir)
	for _, p := range pruned {
		assert.NotEqual(t, freshDir, p)
	}

	// Stale dir should still exist after dry run
	_, err = os.Stat(staleDir)
	assert.NoError(t, err, "stale dir should still exist after dry run")

	// Real prune
	pruned, failed, err = PruneTempFiles(layout, false, 1*time.Hour)
	require.NoError(t, err)
	assert.Empty(t, failed)
	assert.Contains(t, pruned, staleDir)

	// Stale dir should be gone
	_, err = os.Stat(staleDir)
	assert.True(t, os.IsNotExist(err), "stale dir should be removed after prune")

	// Fresh dir should still exist
	_, err = os.Stat(freshDir)
	assert.NoError(t, err, "fresh dir should still exist after prune")
}

func TestPruneTempFiles_NonDir(t *testing.T) {
	layout := testLayout(t)
	tmpRoot := layout.TempDir()
	require.NoError(t, os.MkdirAll(tmpRoot, 0o700))

	// Create a file (not dir) with yoloai- prefix — should be skipped
	f, err := os.CreateTemp(tmpRoot, "yoloai-file-test-")
	require.NoError(t, err)
	f.Close() //nolint:errcheck,gosec // test cleanup

	// Age the file
	past := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(f.Name(), past, past))

	pruned, _, err := PruneTempFiles(layout, true, 1*time.Hour)
	require.NoError(t, err)

	// The file should not appear in pruned (only dirs are pruned)
	for _, p := range pruned {
		assert.NotEqual(t, f.Name(), p)
		// Also check by basename in case of path differences
		assert.NotEqual(t, filepath.Base(f.Name()), filepath.Base(p))
	}
}

// TestPruneTempFiles_UnremovableReportedAsFailed verifies the core honesty
// guarantee: a stale dir that can't be removed is reported in failed and never
// in pruned, so the caller can't falsely claim it was removed. This mirrors the
// real-world case of a root-owned temp dir left by a sudo run.
func TestPruneTempFiles_UnremovableReportedAsFailed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission checks, so removal can't be made to fail this way")
	}
	layout := testLayout(t)
	tmpRoot := layout.TempDir()
	require.NoError(t, os.MkdirAll(tmpRoot, 0o700))

	staleDir, err := os.MkdirTemp(tmpRoot, "yoloai-unremovable-test-")
	require.NoError(t, err)

	// A child file makes the dir non-empty; stripping write on the parent means
	// os.RemoveAll can't unlink the child, so removing staleDir fails.
	require.NoError(t, os.WriteFile(filepath.Join(staleDir, "locked"), []byte("x"), 0o600))
	require.NoError(t, os.Chmod(staleDir, 0o500))       //nolint:gosec // dir needs exec bit; read-only is the point — it forces removal to fail
	t.Cleanup(func() { _ = os.Chmod(staleDir, 0o700) }) //nolint:gosec // restore write so t.TempDir cleanup can recurse in

	past := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(staleDir, past, past))

	pruned, failed, err := PruneTempFiles(layout, false, 1*time.Hour)
	require.NoError(t, err)

	assert.NotContains(t, pruned, staleDir, "an unremovable dir must not be reported as removed")
	var failedPaths []string
	for _, f := range failed {
		failedPaths = append(failedPaths, f.Path)
	}
	assert.Contains(t, failedPaths, staleDir, "an unremovable dir must be reported as failed")

	_, statErr := os.Stat(staleDir)
	assert.NoError(t, statErr, "the dir should still exist since removal failed")
}
