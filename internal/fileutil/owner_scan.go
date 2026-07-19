// ABOUTME: ScanWrongOwner — a bounded, read-only walk that reports tree entries
// not owned by the invoking user (root-owned sudo/backend leftovers), for the
// `yoloai doctor` ownership audit. Skips a wrong-owned dir's subtree so one
// problem is one entry and the walk stays bounded.

package fileutil

import (
	"context"
	"io/fs"
	"path/filepath"
)

// WrongOwnerScan reports directory-tree entries not owned by the expected uid.
type WrongOwnerScan struct {
	// Count is every wrong-owned entry found; a wrong-owned directory counts
	// once (its whole subtree is one problem), not once per file beneath it.
	Count int
	// Sample holds up to the caller's cap of offending paths, top-most first.
	Sample []string
}

// ScanWrongOwner walks root and reports entries whose owning uid != wantUID —
// the root-owned leftovers (a `sudo yoloai …` run, backend VM/overlay state)
// that block the invoking user from deleting, pruning, or rebuilding. A
// wrong-owned directory is recorded and NOT descended: the whole subtree is one
// problem, and skipping it also bounds the walk. At most maxSample paths are
// retained; Count still reflects every offender found.
//
// It is a best-effort diagnostic: a missing root is not an error (zero result),
// and an unreadable subdirectory is skipped rather than failing the audit. On a
// platform without POSIX ownership (Windows) OwnerUID reports nothing, so the
// scan is empty. Honors ctx cancellation.
func ScanWrongOwner(ctx context.Context, root string, wantUID, maxSample int) (WrongOwnerScan, error) {
	var scan WrongOwnerScan
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			// Missing root (d == nil) → nothing to audit; unreadable subdir →
			// skip its subtree. Neither fails the audit.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // best-effort audit: an entry that vanished mid-walk is skipped, not a failure
		}
		uid, ok := OwnerUID(info)
		if !ok || uid == wantUID {
			return nil
		}
		scan.Count++
		if len(scan.Sample) < maxSample {
			scan.Sample = append(scan.Sample, path)
		}
		if d.IsDir() {
			return fs.SkipDir // one problem; don't descend or double-count the subtree
		}
		return nil
	})
	return scan, err
}
