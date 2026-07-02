// ABOUTME: Durable, atomic write primitives (temp+fsync+rename+dir-fsync) plus
// ABOUTME: directory/tree fsync helpers — the crash-safe migration's foundation.
package fileutil

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes data to path durably and atomically. It writes a temp
// file in path's own directory, fsyncs its contents, renames it over path, then
// fsyncs the parent directory. A concurrent reader — or a crash at any point —
// observes either the previous file or the fully-written new one, never a
// truncated or half-written file. The parent-directory fsync makes the rename
// itself survive power loss; without it, the dir entry can linger in cache and
// the write vanishes on a panic. On darwin every fsync is an F_FULLFSYNC (see
// fullSync).
//
// The temp file is created in path's directory (not $TMPDIR) so the rename is
// same-filesystem and therefore atomic. Ownership is fixed for sudo. On any
// failure before the rename, the temp file is removed.
func AtomicWriteFile(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()

	if err := finalizeTemp(tmp, data, perm); err != nil {
		os.Remove(tmpName) //nolint:errcheck,gosec // G104: best-effort cleanup; write error dominates
		return fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := ChownIfSudo(tmpName); err != nil {
		os.Remove(tmpName) //nolint:errcheck,gosec // G104: best-effort cleanup; chown error dominates
		return fmt.Errorf("chown temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName) //nolint:errcheck,gosec // G104: best-effort cleanup; rename error dominates
		return fmt.Errorf("rename temp to %s: %w", path, err)
	}
	if err := FsyncDir(dir); err != nil {
		return fmt.Errorf("fsync dir for %s: %w", path, err)
	}
	return nil
}

// finalizeTemp writes data to the open temp file, sets its permissions
// explicitly (bypassing the umask, matching WriteFilePerm's contract), flushes
// it to stable storage, and closes it. On any error the file is still closed.
func finalizeTemp(f *os.File, data []byte, perm fs.FileMode) error {
	if _, err := f.Write(data); err != nil {
		f.Close() //nolint:errcheck,gosec // G104: original write error dominates
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close() //nolint:errcheck,gosec // G104: original chmod error dominates
		return err
	}
	if err := fullSync(f); err != nil {
		f.Close() //nolint:errcheck,gosec // G104: original sync error dominates
		return err
	}
	return f.Close()
}

// AtomicWriteJSON marshals v as indented JSON and writes it via AtomicWriteFile.
func AtomicWriteJSON(path string, v any, perm fs.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json for %s: %w", path, err)
	}
	return AtomicWriteFile(path, data, perm)
}

// FsyncDir flushes a directory's own metadata — its list of entries — to stable
// storage, so a rename or create within it survives power loss. Opening the
// directory read-only and fsyncing that descriptor is the POSIX-blessed way to
// force the dir entry out of cache.
func FsyncDir(dir string) error {
	f, err := os.Open(dir) //nolint:gosec // G304: dir is a trusted yoloai path
	if err != nil {
		return err
	}
	if err := fullSync(f); err != nil {
		f.Close() //nolint:errcheck,gosec // G104: original sync error dominates
		return err
	}
	return f.Close()
}

// FsyncTree flushes every regular file and directory under root to stable
// storage. The migration build phase calls this on a freshly-built scratch tree
// before writing its build-complete sentinel, so the tree's contents are
// durable even if a later same-filesystem move degrades to a copy. Symlinks and
// special files are skipped: there is no portable descriptor to fsync a symlink
// itself, and a real migration tree holds only regular files and directories
// (the merged-view capture sees a normal POSIX tree, no whiteout devices).
func FsyncTree(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil
		}
		f, err := os.Open(p) //nolint:gosec // G304: p is under a trusted scratch root
		if err != nil {
			return err
		}
		if err := fullSync(f); err != nil {
			f.Close() //nolint:errcheck,gosec // G104: original sync error dominates
			return err
		}
		return f.Close()
	})
}
