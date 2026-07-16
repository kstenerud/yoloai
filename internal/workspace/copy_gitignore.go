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
//     there's no gitignore to honor and we fall back to a full copy. When
//     preserveGit is true, the source .git is also cloned so the work copy keeps
//     its history/blame and filter config (the default for :copy; see
//     copy-mode-history.md). git's file enumeration never lists .git itself, so
//     without this step the gitignore-honoring copy would drop history entirely.
//   - includeIgnored == true (:copy-all): copy everything via CopyDir, which
//     already includes .git; preserveGit is not consulted.
//
// The same dispatch runs at initial setup and at reset, so a re-copy reproduces
// the same file set.
//
// However src is copied, the result never keeps a .git link: see RemoveGitLink.
// Only the :copy-all branch can produce one, since the gitignore-honoring branch
// never copies .git at all. The sever is unconditional anyway, so the invariant
// holds for the function rather than for one branch of it (DF116).
func CopyProjectDir(src, dst string, includeIgnored, preserveGit bool, listProjectFiles func() (files []string, isRepo bool, err error)) error {
	if err := copyProjectContent(src, dst, includeIgnored, preserveGit, listProjectFiles); err != nil {
		return err
	}
	if _, err := RemoveGitLink(dst); err != nil {
		return err
	}
	return nil
}

// copyProjectContent performs CopyProjectDir's mode dispatch, leaving the
// work copy's .git invariant to its caller.
func copyProjectContent(src, dst string, includeIgnored, preserveGit bool, listProjectFiles func() (files []string, isRepo bool, err error)) error {
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
	if err := copyFileList(src, dst, files); err != nil {
		return err
	}
	if preserveGit {
		return copyGitDir(src, dst)
	}
	return nil
}

// PreserveGit reports whether a :copy directory should keep the source repo's
// real .git. History is preserved unless the user opted out (stripHistory) or
// the backend does not run work-copy git in confinement (confined == false),
// where a writable agent-controlled .git would widen the host-side RCE surface
// (see confine-host-side-git.md). downgraded is true when history was wanted but
// the backend gate forced a strip — the caller surfaces a one-time notice.
// Call only for :copy dirs; :copy-all always copies .git via CopyDir.
func PreserveGit(stripHistory, confined bool) (preserve, downgraded bool) {
	if stripHistory {
		return false, false
	}
	if !confined {
		return false, true
	}
	return true, false
}

// copyGitDir CoW-clones src/.git into dst so the work copy keeps the source
// repo's history (log/blame/bisect) and filter config. Only a real .git
// *directory* is copied; a gitlink file (linked worktree / submodule) is
// skipped — its objects live in a shared common dir outside src, out of scope
// (see copy-mode-history.md). A missing .git is a no-op.
func copyGitDir(src, dst string) error {
	gitPath := filepath.Join(src, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat .git in %s: %w", src, err)
	}
	if !info.IsDir() {
		return nil // gitlink file (worktree/submodule) — history lives elsewhere
	}
	if err := CopyDir(gitPath, filepath.Join(dst, ".git")); err != nil {
		return fmt.Errorf("copy .git: %w", err)
	}
	return nil
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
