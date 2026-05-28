package sandbox

// ABOUTME: Cleans up stale yoloai-* temporary directories in the system temp dir.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PruneTempFiles removes stale yoloai-* directories older than maxAge from the
// system temp dir (os.TempDir()). Returns the list of paths removed (or that
// would be removed if dryRun).
//
// It must read os.TempDir(), not a hardcoded /tmp: the dirs are created with
// os.MkdirTemp("", "yoloai-…") which honors $TMPDIR and, on macOS, defaults to
// /var/folders/.../T — so a hardcoded /tmp would never find them there
// (leaving orphaned yoloai-secrets-* credential dirs from killed runs uncleaned).
func PruneTempFiles(dryRun bool, maxAge time.Duration) ([]string, error) {
	tmpDir := os.TempDir()
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", tmpDir, err)
	}

	cutoff := time.Now().Add(-maxAge)
	var pruned []string

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "yoloai-") {
			continue
		}
		if !entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue // skip entries we can't stat
		}
		if info.ModTime().After(cutoff) {
			continue // too recent
		}

		path := filepath.Join(tmpDir, entry.Name())

		if !dryRun {
			if err := os.RemoveAll(path); err != nil {
				continue // skip entries we can't remove (e.g. root-owned from sudo runs)
			}
		}
		pruned = append(pruned, path)
	}

	return pruned, nil
}
