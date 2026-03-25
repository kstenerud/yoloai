package workspace

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kstenerud/yoloai/internal/fileutil"
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
	if err := fileutil.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}

	// Try fast clone (APFS on macOS). Falls back on unsupported platforms/filesystems.
	if err := cloneDir(src, dst); err == nil {
		// Fast clone succeeded, clean up unwanted files.
		if err := removeBugreportFiles(dst); err != nil {
			return err
		}
		return removeBuildArtifacts(dst)
	}

	// Regular file-by-file copy.
	return copyDirWalk(src, dst, srcInfo)
}

// copyDirWalk copies a directory tree by walking the source and recreating
// each entry in the destination, preserving symlinks, permissions, and
// modification times.
func copyDirWalk(src, dst string, srcInfo os.FileInfo) error {
	if err := fileutil.MkdirAll(dst, srcInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Compute relative path (used by multiple checks below)
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("rel path: %w", err)
		}

		// Skip yoloai-generated bugreport files
		if !d.IsDir() && isBugreportFile(d.Name()) {
			return nil
		}

		// Skip build artifacts
		if isBuildArtifact(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir // Skip entire directory tree
			}
			return nil
		}

		target := filepath.Join(dst, rel)

		// Handle symlinks before anything else — d.Type() doesn't follow them.
		if d.Type()&fs.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", path, err)
			}
			if err := os.Symlink(link, target); err != nil { //nolint:gosec // G122: target is derived from a controlled destination root, TOCTOU not applicable here
				return err
			}
			return fileutil.ChownIfSudo(target)
		}

		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return fmt.Errorf("dir info %s: %w", path, err)
			}
			return fileutil.MkdirAll(target, info.Mode().Perm())
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

	out, err := fileutil.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm()) //nolint:gosec // G304: paths come from WalkDir of validated sandbox paths
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

// isBuildArtifact returns true if the relative path is a build artifact
// that should be excluded from :copy directories. Matches both directory
// patterns (e.g., "node_modules/", ".build/") and nested patterns
// (e.g., "*.xcworkspace/xcuserdata/").
//
// Only excludes artifacts that:
// 1. Embed hardcoded absolute paths (causing compilation failures)
// 2. Have unambiguous names (no false positive risk)
func isBuildArtifact(relPath string, isDir bool) bool {
	// Split path into components for pattern matching
	parts := strings.Split(filepath.ToSlash(relPath), "/")

	// Check each path component for build artifact directories
	for _, part := range parts {
		// Swift Package Manager build artifacts (PCH files with hardcoded paths)
		if part == ".build" {
			return true
		}
		// Xcode derived data (build caches with hardcoded paths)
		if part == "DerivedData" {
			return true
		}
		// Node.js dependencies (native modules can have hardcoded paths)
		if part == "node_modules" {
			return true
		}
		// Python bytecode cache (always regenerated)
		if part == "__pycache__" {
			return true
		}
	}

	// Check for nested patterns like "*.xcworkspace/xcuserdata/"
	// These require matching multiple consecutive path components
	if matchesNestedPattern(parts, "*.xcworkspace/xcuserdata") {
		return true
	}
	if matchesNestedPattern(parts, "*.xcodeproj/xcuserdata") {
		return true
	}

	return false
}

// matchesNestedPattern checks if path components match a nested pattern
// like "*.xcworkspace/xcuserdata/". Uses a sliding window algorithm to
// find matching sequences in the path.
//
// Pattern format: "pattern1/pattern2" where each part can use * wildcard.
func matchesNestedPattern(pathParts []string, pattern string) bool {
	patternParts := strings.Split(pattern, "/")
	if len(patternParts) > len(pathParts) {
		return false
	}

	// Sliding window: check each possible starting position
	for i := 0; i <= len(pathParts)-len(patternParts); i++ {
		matched := true
		for j, patternPart := range patternParts {
			if !matchesPattern(pathParts[i+j], patternPart) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}

	return false
}

// matchesPattern checks if a path component matches a pattern with * wildcard.
func matchesPattern(component, pattern string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return component == pattern
	}

	// Handle patterns like "*.xcworkspace"
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".xcworkspace"
		return strings.HasSuffix(component, suffix)
	}

	return false
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

// removeBuildArtifacts recursively removes all build artifact directories
// and files from the directory. Used after fast clone (APFS clonefile).
func removeBuildArtifacts(root string) error {
	var toRemove []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("rel path: %w", err)
		}

		if isBuildArtifact(rel, d.IsDir()) {
			toRemove = append(toRemove, path)
			if d.IsDir() {
				return filepath.SkipDir // Skip entire directory tree
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk for build artifacts: %w", err)
	}

	// Remove in reverse order so nested entries are removed before parents.
	for i := len(toRemove) - 1; i >= 0; i-- {
		if err := os.RemoveAll(toRemove[i]); err != nil {
			return fmt.Errorf("remove %s: %w", toRemove[i], err)
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
