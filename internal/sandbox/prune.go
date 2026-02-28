package sandbox

// ABOUTME: Cleans up stale /tmp/yoloai-* temporary directories.

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// PruneTempFiles removes stale /tmp/yoloai-* directories older than maxAge.
// Returns the list of paths removed (or that would be removed if dryRun).
func PruneTempFiles(dryRun bool, maxAge time.Duration) ([]string, error) {
	entries, err := os.ReadDir("/tmp")
	if err != nil {
		return nil, fmt.Errorf("read /tmp: %w", err)
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

		path := "/tmp/" + entry.Name()
		pruned = append(pruned, path)

		if !dryRun {
			if err := os.RemoveAll(path); err != nil {
				return pruned, fmt.Errorf("remove %s: %w", path, err)
			}
		}
	}

	return pruned, nil
}
