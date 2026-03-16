package workspace

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CopyDir copies a directory tree preserving symlinks, permissions, and
// modification times. Unlike shelling out to cp, this handles broken
// symlinks correctly and avoids platform-specific quirks (e.g. macOS
// cp -r following symlinks).
func CopyDir(src, dst string) error {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("source is not a directory: %s", src)
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}

	// Try fast clone (APFS on macOS). Falls back on unsupported platforms/filesystems.
	if err := cloneDir(src, dst); err == nil {
		// Fast clone succeeded, but we need to remove bugreport files.
		return removeBugreportFiles(dst)
	}

	// Regular file-by-file copy.
	return copyDirWalk(src, dst, srcInfo)
}

// copyDirWalk copies a directory tree by walking the source and recreating
// each entry in the destination, preserving symlinks, permissions, and
// modification times.
func copyDirWalk(src, dst string, srcInfo os.FileInfo) error {
	if err := os.MkdirAll(dst, srcInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip yoloai-generated bugreport files
		if !d.IsDir() && isBugreportFile(d.Name()) {
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("rel path: %w", err)
		}
		target := filepath.Join(dst, rel)

		// Handle symlinks before anything else — d.Type() doesn't follow them.
		if d.Type()&fs.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", path, err)
			}
			return os.Symlink(link, target) //nolint:gosec // G122: target is derived from a controlled destination root, TOCTOU not applicable here
		}

		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return fmt.Errorf("dir info %s: %w", path, err)
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}

		// Regular file.
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("file info %s: %w", path, err)
		}
		return copyFile(path, target, info)
	})
}

// copyFile copies a regular file preserving permissions and modification time.
func copyFile(src, dst string, srcInfo fs.FileInfo) error {
	in, err := os.Open(src) //nolint:gosec // G304: paths come from WalkDir of validated sandbox paths
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close() //nolint:errcheck // read-only file, close error is harmless

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm()) //nolint:gosec // G304: paths come from WalkDir of validated sandbox paths
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close() //nolint:errcheck,gosec // best-effort cleanup on copy failure
		return fmt.Errorf("copy %s: %w", src, err)
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
	}

	return os.Chtimes(dst, srcInfo.ModTime(), srcInfo.ModTime())
}

// isBugreportFile returns true if the filename matches the bugreport pattern.
// Matches both final (.md) and temporary (.md.tmp) bugreport files.
func isBugreportFile(name string) bool {
	if !strings.HasPrefix(name, "yoloai-bugreport-") {
		return false
	}
	return strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".md.tmp")
}

// removeBugreportFiles recursively removes all bugreport files from the directory.
func removeBugreportFiles(root string) error {
	var toRemove []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && isBugreportFile(d.Name()) {
			toRemove = append(toRemove, path)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk for bugreport files: %w", err)
	}

	for _, path := range toRemove {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}

// RemoveGitDirs recursively removes all .git entries (files and directories)
// from root. This strips git metadata from a copied working tree so that
// hooks, LFS filters, submodule links, and worktree links don't interfere
// with yoloAI's internal git operations.
func RemoveGitDirs(root string) error {
	var toRemove []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			toRemove = append(toRemove, path)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk for .git entries: %w", err)
	}

	// Remove in reverse order so nested entries are removed before parents.
	for i := len(toRemove) - 1; i >= 0; i-- {
		if err := os.RemoveAll(toRemove[i]); err != nil {
			return fmt.Errorf("remove %s: %w", toRemove[i], err)
		}
	}
	return nil
}
