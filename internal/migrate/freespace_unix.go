//go:build !windows

// ABOUTME: Free-space preflight — migration duplicates the tree, so it refuses in
// ABOUTME: the plan phase rather than running the disk out mid-build.
package migrate

import (
	"fmt"
	"syscall"
)

// FreeBytes returns the space available to the invoking user on the filesystem
// holding path.
//
// Bavail, not Bfree: the two differ by the reserved blocks only root may consume
// (5% by default on ext4), and a migration runs as an ordinary user. Reporting
// Bfree would promise space the build cannot actually use, which is precisely
// the failure this check exists to convert into a refusal.
func FreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	return st.Bavail * statfsBlockSize(&st), nil
}
