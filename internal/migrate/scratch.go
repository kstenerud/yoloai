// ABOUTME: The disposable, never-resumed migration staging dir at the top of
// ABOUTME: the yoloai home — a crashed build is garbage, tossed and rebuilt fresh.
package migrate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// ScratchDirName is the fixed staging dir under the yoloai home. It is never
// resumed: any yoloai run (when no migration holds the lock) throws out a
// leftover, and the run driver clears it between chain steps. A fixed name (no
// build-id) makes it easy to find; identity is irrelevant because a crashed
// build is discarded and rebuilt, never recovered. It MUST sit on the same
// filesystem as the live dirs (SameFilesystem) so the build's move-in is a true
// atomic rename; a cross-filesystem scratch would make os.Rename fail with
// EXDEV mid-migration (it does not fall back to a copy) rather than move in.
const ScratchDirName = ".migration-scratch"

// ScratchPath returns the scratch dir path under home.
func ScratchPath(home string) string { return filepath.Join(home, ScratchDirName) }

// FreshScratch disposes any leftover scratch dir and creates a new empty one,
// returning its path. Call it at the start of a run so the build phase always
// starts from clean state (a leftover is a crashed build's garbage).
func FreshScratch(home string) (string, error) {
	path := ScratchPath(home)
	if err := os.RemoveAll(path); err != nil {
		return "", fmt.Errorf("dispose leftover scratch: %w", err)
	}
	if err := fileutil.MkdirAll(path, 0o750); err != nil {
		return "", fmt.Errorf("create scratch: %w", err)
	}
	return path, nil
}

// DisposeScratch removes the scratch dir; it is a no-op if absent.
func DisposeScratch(home string) error {
	if err := os.RemoveAll(ScratchPath(home)); err != nil {
		return fmt.Errorf("dispose scratch: %w", err)
	}
	return nil
}
