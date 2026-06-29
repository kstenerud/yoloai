// ABOUTME: CopyProjectDir copies a user project directory while honoring
// ABOUTME: .gitignore, so files a user deliberately excluded from their repo
// ABOUTME: (secrets like .env/*.pem, .aws/, local config) never get copied into a
// ABOUTME: sandbox where the agent could read them. The gitignore enumeration is
// ABOUTME: injected (git.ListProjectFiles) so this package stays git-free.
package workspace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// CopyProjectDir copies a user project directory at src into the sandbox copy
// area dst, dispatching on the :copy-all opt-out:
//
//   - includeIgnored == false (the :copy default): honor .gitignore. The caller
//     supplies listProjectFiles — git's view of the project (tracked plus
//     untracked-but-not-ignored, ignored files excluded; see
//     git.ListProjectFiles). isRepo == false means src isn't a git work tree, so
//     there's no gitignore to honor and we fall back to a full copy.
//   - includeIgnored == true (:copy-all): copy everything via CopyDir.
//
// The same dispatch runs at initial setup and at reset, so a re-copy reproduces
// the same file set.
func CopyProjectDir(src, dst string, includeIgnored bool, listProjectFiles func() (files []string, isRepo bool, err error)) error {
	if includeIgnored {
		return CopyDir(src, dst)
	}
	files, isRepo, err := listProjectFiles()
	if err != nil {
		return err
	}
	if !isRepo {
		return CopyDir(src, dst)
	}
	return copyFileList(src, dst, files)
}

// copyFileList copies each src-relative path in files from src to dst, creating
// parent directories as needed and preserving file permissions, modification
// times, and symlinks. Paths that no longer exist on disk (tracked-but-deleted),
// submodule gitlink directories, build artifacts, and bugreport files are
// skipped — so the result matches CopyDir's exclusions plus the gitignore set.
func copyFileList(src, dst string, files []string) error {
	if err := fileutil.MkdirAll(dst, 0o750); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	for _, rel := range files {
		if isBugreportFile(filepath.Base(rel)) || isBuildArtifact(rel, false) {
			continue
		}
		srcPath := filepath.Join(src, rel)
		info, err := os.Lstat(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // tracked but deleted from the work tree — nothing to copy
			}
			return fmt.Errorf("stat %s: %w", srcPath, err)
		}
		target := filepath.Join(dst, rel)
		if err := fileutil.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return fmt.Errorf("create parent of %s: %w", rel, err)
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			if err := copySymlink(srcPath, target); err != nil {
				return err
			}
		case info.IsDir():
			// A submodule appears as a single gitlink path; ls-files doesn't list
			// the files inside it, so there's nothing to copy here. Skip rather
			// than wholesale-copy the submodule work tree (which would reintroduce
			// its own ignored files). Submodule contents are a known limitation.
			continue
		default:
			if err := copyFile(srcPath, target, info); err != nil {
				return err
			}
		}
	}
	return nil
}
