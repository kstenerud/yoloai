// ABOUTME: The whole-tree migration flock — one live lock over the yoloai home
// ABOUTME: held for a whole run; a crash releases it, so it never wedges.
package migrate

import (
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/locking"
)

// HomeLockName is the lock file guarding a migration run.
const HomeLockName = ".migration.lock"

// AcquireHomeLock takes the exclusive whole-tree migration lock under home,
// non-blocking (crash-safe-migration decision 5). It is held by `system
// migrate` for the entire run even though a given migration touches only part
// of the tree; while held, every other yoloai command refuses and a second
// migrate refuses too. It is a live flock — released on process death — so a
// crash never leaves a permanent lock. Returns a release func the caller must
// invoke when the run ends; an ErrWouldBlock-class error means another run
// holds it.
func AcquireHomeLock(home string) (release func(), err error) {
	return locking.AcquireNonBlocking(filepath.Join(home, HomeLockName))
}
