// ABOUTME: CopyPathFaithful — an exact, unfiltered replica of a file/dir/symlink,
// ABOUTME: for migration seeding/repopulation where nothing may be dropped.
package workspace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// CopyPathFaithful copies a single filesystem entry — a regular file,
// directory, or symlink — from src to dst as an exact replica. Unlike CopyDir
// it does NO filtering: build artifacts, bugreport files, and .git dirs are all
// preserved. Migration seeding and repopulation need byte-faithful copies so a
// unit's unchanged data survives a migration untouched.
//
// Directories use the same CoW clone fast-path as CopyDir (near-instant on
// APFS), falling back to a walk-based copy. dst must not already exist.
func CopyPathFaithful(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("stat source %s: %w", src, err)
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return copySymlink(src, dst)
	case info.IsDir():
		return copyTreeFaithful(src, dst, info)
	default:
		return copyFile(src, dst, info)
	}
}

// copyTreeFaithful clones or walk-copies a directory tree with no filtering.
func copyTreeFaithful(src, dst string, srcInfo fs.FileInfo) error {
	if err := fileutil.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}
	// Fast CoW clone of the whole tree (APFS). No artifact removal — faithful.
	if err := cloneDir(src, dst); err == nil {
		return nil
	}
	if err := fileutil.MkdirAll(dst, srcInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("rel path: %w", err)
		}
		if rel == "." {
			return nil // dst root already created with srcInfo perms
		}
		target := filepath.Join(dst, rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return copySymlink(path, target)
		}
		if d.IsDir() {
			return copyEntryDir(path, target, d)
		}
		return copyEntryFile(path, target, d)
	})
}
