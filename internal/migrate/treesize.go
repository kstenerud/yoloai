// ABOUTME: Measures a tree for the free-space precondition — loudly, because an
// ABOUTME: underestimate here is a migration that runs the disk out mid-build.
package migrate

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

// TreeSize sums the apparent size of every regular file under dir.
//
// It fails on any walk error rather than skipping, which is what separates it
// from the CLI's best-effort dirSize. This number is a precondition: a size that
// silently came back short because a subdirectory could not be read is a
// migration that passes its free-space check and then fills the disk halfway
// through building the copy — the exact failure the check exists to convert
// into a refusal.
//
// Apparent size, not blocks: it deliberately over-estimates for sparse files
// and ignores whatever the destination filesystem's compression or block size
// does, because the check wants a bound, not an accounting.
func TreeSize(dir string) (uint64, error) {
	var total uint64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("measure %s: %w", path, err)
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("measure %s: %w", path, err)
		}
		if size := info.Size(); size > 0 {
			total += uint64(size)
		}
		return nil
	})
	return total, err
}
