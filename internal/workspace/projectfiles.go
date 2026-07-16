// ABOUTME: ProjectFileSet enumerates what belongs in a work copy, for callers
// ABOUTME: that must reproduce CopyProjectDir's file set without copying — the
// ABOUTME: in-place reset, which syncs differentially into a live container and
// ABOUTME: so cannot just re-copy the tree (DF117).
package workspace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ProjectFileSet returns the src-relative paths that belong in a work copy of
// src, under the same mode dispatch CopyProjectDir applies: git's project files
// for the :copy default, everything for :copy-all and for a non-repo, and in
// both cases minus the build artifacts and bugreport files CopyProjectDir drops.
//
// `.git` is never included. It is not a project file, it is copied as a unit
// rather than path by path, and the modes disagree about whether it belongs at
// all — so both the copy and the reset decide it separately. See WantsGitDir.
//
// This exists because CopyProjectDir fuses "what belongs" with "put it there",
// and reset needs the first answer without the second: it syncs into a work copy
// that is bind-mounted into a running container, so it cannot wipe and re-copy.
// The two must not drift, which TestProjectFileSet_MatchesCopyProjectDir gates
// by comparing this answer against what CopyProjectDir actually writes.
func ProjectFileSet(src string, includeIgnored bool, listProjectFiles func() (files []string, isRepo bool, err error)) ([]string, error) {
	if includeIgnored {
		return walkProjectFiles(src)
	}
	files, isRepo, err := listProjectFiles()
	if err != nil {
		return nil, err
	}
	if !isRepo {
		return walkProjectFiles(src)
	}
	return listedProjectFiles(src, files)
}

// PruneToFileSet removes everything under dst that files does not account for,
// leaving the work copy holding what CopyProjectDir would have written and
// nothing else. `.git` is left alone: it is not a project file, and its caller
// replaces it as a unit.
//
// This is the half of a reset that copying cannot do. Copying the source over a
// work copy refreshes what the source still has; only a prune removes what the
// agent added, what the source no longer has, and what a laxer sync should never
// have put there in the first place — a `.gitignore`d secret, say (DF117).
func PruneToFileSet(dst string, files []string) error {
	keep := pathsWithAncestors(files)
	var toRemove []string
	err := filepath.WalkDir(dst, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dst, path)
		if relErr != nil {
			return fmt.Errorf("rel path: %w", relErr)
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if keep[rel] {
			return nil
		}
		toRemove = append(toRemove, path)
		if d.IsDir() {
			return filepath.SkipDir // nothing inside a doomed directory needs visiting
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", dst, err)
	}
	for _, path := range toRemove {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("prune %s: %w", path, err)
		}
	}
	return nil
}

// pathsWithAncestors returns every listed path plus every ancestor directory of
// one. The copy creates those parents implicitly rather than listing them (git
// does not track directories), so a prune driven by the bare list would delete
// the very directories the copy just put the files in.
func pathsWithAncestors(files []string) map[string]bool {
	keep := make(map[string]bool, len(files)*2)
	for _, rel := range files {
		for p := rel; p != "." && p != "" && p != string(filepath.Separator); p = filepath.Dir(p) {
			if keep[p] {
				break // this path's ancestors are already accounted for
			}
			keep[p] = true
		}
	}
	return keep
}

// WantsGitDir reports whether a work copy of a dir in this mode keeps a copy of
// the source's `.git`. It mirrors CopyProjectDir: :copy-all takes the source
// wholesale, `.git` included, and :copy clones it when history is preserved.
// Only a real `.git` directory is ever copied — a gitlink names a git dir
// outside the copy and is severed, never followed (DF116).
func WantsGitDir(includeIgnored, preserveGit bool) bool {
	return includeIgnored || preserveGit
}

// walkProjectFiles enumerates src the way CopyDir copies it: everything, minus
// the build artifacts and bugreport files CopyDir strips, minus `.git`.
// Directories are listed in their own right so that empty ones survive, which
// CopyDir preserves and a files-only list would quietly drop.
func walkProjectFiles(src string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("rel path: %w", err)
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
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
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", src, err)
	}
	return out, nil
}

// listedProjectFiles filters git's project-file list the way copyFileList does:
// build artifacts and bugreports out, tracked-but-deleted paths out, and
// submodule gitlink directories out (git lists the submodule as one path and
// nothing inside it, so there is nothing to copy).
func listedProjectFiles(src string, files []string) ([]string, error) {
	out := make([]string, 0, len(files))
	for _, rel := range files {
		if isBugreportFile(filepath.Base(rel)) || isBuildArtifact(rel, false) {
			continue
		}
		info, err := os.Lstat(filepath.Join(src, rel))
		if err != nil {
			if os.IsNotExist(err) {
				continue // tracked but deleted from the work tree
			}
			return nil, fmt.Errorf("stat %s: %w", filepath.Join(src, rel), err)
		}
		if info.IsDir() {
			continue // submodule gitlink
		}
		out = append(out, rel)
	}
	return out, nil
}
