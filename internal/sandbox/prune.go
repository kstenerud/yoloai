package sandbox

// ABOUTME: Cleans up stale yoloai-* temporary directories in the system temp dir.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TempPruneFailure is a stale temp dir that matched but could not be removed —
// typically a root-owned dir left by a sudo run, which a regular-user prune
// can't delete. It carries the reason so the caller can tell the user why the
// dir persists (rather than silently claiming it was removed).
type TempPruneFailure struct {
	Path string
	Err  error
}

// PruneTempFiles removes stale yoloai-* directories older than maxAge from the
// system temp dir (os.TempDir()). It returns the paths actually removed (or, in
// dryRun, that would be removed) and — for non-dry-run runs — any matching dirs
// that could not be removed. The removed list reflects what truly happened: a
// dir whose os.RemoveAll fails lands in failed, never in pruned, so callers must
// not report it as removed.
//
// It must read os.TempDir(), not a hardcoded /tmp: the dirs are created with
// os.MkdirTemp("", "yoloai-…") which honors $TMPDIR and, on macOS, defaults to
// /var/folders/.../T — so a hardcoded /tmp would never find them there
// (leaving orphaned yoloai-secrets-* credential dirs from killed runs uncleaned).
func PruneTempFiles(dryRun bool, maxAge time.Duration) (pruned []string, failed []TempPruneFailure, err error) {
	tmpDir := os.TempDir()
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", tmpDir, err)
	}

	cutoff := time.Now().Add(-maxAge)

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
