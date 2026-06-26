package orchestrator

// ABOUTME: Cleans up stale yoloai-* temporary directories under the yoloai data dir.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
)

// TempPruneFailure is a stale temp dir that matched but could not be removed —
// typically a root-owned dir left by a sudo run, which a regular-user prune
// can't delete. It carries the reason so the caller can tell the user why the
// dir persists (rather than silently claiming it was removed).
type TempPruneFailure struct {
	Path string
	Err  error
}

// PruneTempFiles removes stale yoloai-* directories older than maxAge from
// layout.TempDir() (DataDir/tmp). It returns the paths actually removed (or,
// in dryRun, that would be removed) and — for non-dry-run runs — any matching
// dirs that could not be removed. The removed list reflects what truly happened:
// a dir whose os.RemoveAll fails lands in failed, never in pruned, so callers
// must not report it as removed.
func PruneTempFiles(layout config.Layout, dryRun bool, maxAge time.Duration) (pruned []string, failed []TempPruneFailure, err error) {
	return pruneTempDir(layout.TempDir(), dryRun, maxAge)
}

// pruneTempDir scans root for stale yoloai-* directories, removing those older
// than maxAge. A missing root is silently ignored (nothing to prune).
func pruneTempDir(root string, dryRun bool, maxAge time.Duration) (pruned []string, failed []TempPruneFailure, err error) {
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return nil, nil, nil // nothing to prune
		}
		return nil, nil, fmt.Errorf("read %s: %w", root, readErr)
	}

	cutoff := time.Now().Add(-maxAge)

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "yoloai-") {
			continue
		}
		if !entry.IsDir() {
			continue
		}

		info, statErr := entry.Info()
		if statErr != nil {
			continue // skip entries we can't stat
		}
		if info.ModTime().After(cutoff) {
			continue // too recent
		}

		path := filepath.Join(root, entry.Name())

		if !dryRun {
			if rmErr := os.RemoveAll(path); rmErr != nil {
				// Surface, don't swallow: a root-owned dir from a sudo run
				// can't be removed by a regular-user prune. Reporting it as
				// removed would be a lie (the dir is still on disk).
				failed = append(failed, TempPruneFailure{Path: path, Err: rmErr})
				continue
			}
		}
		pruned = append(pruned, path)
	}

	return pruned, failed, nil
}
