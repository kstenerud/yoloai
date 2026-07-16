// ABOUTME: CopyDir walks a directory tree preserving permissions and symlinks,
// ABOUTME: trying the fast cloneDir path first then falling back to a file-by-file copy.
package workspace

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
		return copyDirEntry(src, dst, path, d)
	})
}

// copyDirEntry handles a single entry produced by filepath.WalkDir, skipping
// unwanted files and recreating the entry (symlink, directory, or file) under dst.
func copyDirEntry(src, dst, path string, d fs.DirEntry) error {
	rel, err := filepath.Rel(src, path)
	if err != nil {
		return fmt.Errorf("rel path: %w", err)
	}

	if !d.IsDir() && isBugreportFile(d.Name()) {
		return nil
	}
	if isBuildArtifact(rel, d.IsDir()) {
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}

	target := filepath.Join(dst, rel)

	if d.Type()&fs.ModeSymlink != 0 {
		return copySymlink(path, target)
	}
	if d.IsDir() {
		return copyEntryDir(path, target, d)
	}
	return copyEntryFile(path, target, d)
}

// copySymlink recreates a symlink at target pointing to the same destination as path.
func copySymlink(path, target string) error {
	link, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("readlink %s: %w", path, err)
	}
	if err := os.Symlink(link, target); err != nil {
		return err
	}
	return fileutil.ChownIfSudo(target)
}

// copyEntryDir creates the target directory preserving source permissions.
func copyEntryDir(path, target string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return fmt.Errorf("dir info %s: %w", path, err)
	}
	return fileutil.MkdirAll(target, info.Mode().Perm())
}

// copyEntryFile copies a regular file preserving its permissions.
func copyEntryFile(path, target string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return fmt.Errorf("file info %s: %w", path, err)
	}
	return copyFile(path, target, info)
}

// copyFile copies a regular file preserving permissions and modification time.
func copyFile(src, dst string, srcInfo fs.FileInfo) error {
	in, err := os.Open(src) //nolint:gosec // G304: paths come from WalkDir of validated sandbox paths
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close() //nolint:errcheck // read-only file, close error is harmless

	out, err := fileutil.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}

	// io.Copy between two bare *os.File values engages os.(*File).ReadFrom,
	// which on Linux uses copy_file_range(2): a reflink (CoW, no data copied)
	// on filesystems that support it (btrfs, XFS) when src and dst are on the
	// same filesystem. Wrapping either file in a buffered reader/writer would
	// silently downgrade every copy to a userspace read/write loop.
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
	for _, t := range slices.Backward(toRemove) {
		if err := os.RemoveAll(t); err != nil {
			return fmt.Errorf("remove %s: %w", t, err)
		}
	}
	return nil
}

// IsGitLink reports whether dir keeps its git directory somewhere else: dir/.git
// is a gitlink (the `gitdir:` file of a linked worktree or a submodule) or a
// symlink, rather than a real .git directory. Such a directory does not contain
// its own history, so copying it does not copy its repository — the caller is
// expected to tell the user that history was left behind.
//
// The link's target is deliberately not read. It may be absolute (a worktree),
// relative (a submodule, or a worktree under git 2.48+ `worktree.useRelativePaths`),
// or a symlink, and no form of it is usable from a work copy — so the kind of
// the .git entry decides this, never the text inside it.
func IsGitLink(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil && !info.IsDir()
}

// RemoveGitLink removes dir/.git when it is a gitlink or symlink rather than a
// real .git directory, reporting whether one was removed. A missing .git and a
// real .git directory are both left alone.
//
// A work copy must never keep such a link. It names a git directory outside the
// copy, and on the host that target still resolves to the source repository — so
// git run against the copy reads and writes the user's real objects, index and
// branch refs (DF116). Inside the sandbox the same link resolves to nothing.
// Removing it leaves the copy without a repository, which is the state Baseline
// expects and which makes IsGitRepo report false.
func RemoveGitLink(dir string) (removed bool, err error) {
	gitPath := filepath.Join(dir, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat .git in %s: %w", dir, err)
	}
	if info.IsDir() {
		return false, nil
	}
	if err := os.Remove(gitPath); err != nil {
		return false, fmt.Errorf("remove git link %s: %w", gitPath, err)
	}
	return true, nil
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
	for _, t := range slices.Backward(toRemove) {
		if err := os.RemoveAll(t); err != nil {
			return fmt.Errorf("remove %s: %w", t, err)
		}
	}
	return nil
}
