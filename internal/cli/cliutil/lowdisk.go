// ABOUTME: Low-disk advisory check — best-effort free-space warning printed
// ABOUTME: before commands that allocate significant disk (new, clone, build).

package cliutil

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// LowDiskWarnThresholdBytes is the free-space level below which
// WarnIfLowDisk prints an advisory. Threshold rationale: a freshly
// pulled base image is ~1 GB, a typical workdir copy is sub-GB but
// can balloon with node_modules / pip caches / pre-built binaries.
// 2 GiB gives the user a chance to react (run prune) before an
// out-of-space failure. Intentionally aggressive enough to fire on
// "common" issues without being noisy on healthy systems.
//
// This is advisory, not blocking. Users with workdirs > 2 GiB will
// see the warning even on plenty-of-space machines; that's
// acceptable because the operation is the same shape (could still
// fail).
const LowDiskWarnThresholdBytes int64 = 2 * 1024 * 1024 * 1024

// freeBytesAt returns bytes free on the filesystem backing path. If
// path doesn't exist yet (typical on first run, before ~/.yoloai/ is
// created), walks up to the nearest existing ancestor. Loop terminates
// at "/" since filepath.Dir("/") == "/" — checking that path == parent
// after Dir() catches the fixed point.
//
// Returns (-1, err) only if no ancestor up to and including "/"
// exists or Statfs fails.
func freeBytesAt(path string) (int64, error) {
	for {
		if _, err := os.Stat(path); err == nil {
			var stat syscall.Statfs_t
			if err := syscall.Statfs(path, &stat); err != nil {
				return -1, err
			}
			// Bavail is unprivileged-user-visible free blocks.
			// Bsize is the optimal transfer block size (== fs block size
			// for ext4/xfs/btrfs/zfs); use that, not Frsize, since they
			// match for the filesystems we care about and Bsize is the
			// portable choice.
			return int64(stat.Bavail) * int64(stat.Bsize), nil //nolint:gosec // G115: ext4/xfs filesystem sizes fit in int64
		}
		parent := filepath.Dir(path)
		if parent == path {
			// Fixed point: "/" doesn't stat AND has no ancestor.
			// Effectively impossible on a healthy Linux system.
			return -1, fmt.Errorf("no existing ancestor for path")
		}
		path = parent
	}
}

// WarnIfLowDisk prints a one-line warning to stderr if free space on
// the filesystem backing path is below LowDiskWarnThresholdBytes.
// Stat errors are swallowed silently — this is a courtesy check, not
// a precondition, and shouldn't break commands when /proc/mounts is
// momentarily unreadable or similar.
//
// Call from any command that's about to allocate significant disk:
// sandbox creation (new, clone), image builds (system build).
func WarnIfLowDisk(stderr io.Writer, path string) {
	free, err := freeBytesAt(path)
	if err != nil || free < 0 {
		return
	}
	emitLowDiskWarning(stderr, path, free, LowDiskWarnThresholdBytes)
}

// emitLowDiskWarning is the pure helper testable without filesystem
// access: given an already-determined free-bytes value, it writes
// the warning to stderr iff free is below threshold. Returns true
// if a warning was emitted (lets tests assert on side-effect).
func emitLowDiskWarning(stderr io.Writer, path string, free, threshold int64) bool {
	if free >= threshold {
		return false
	}
	fmt.Fprintf(stderr, //nolint:errcheck // best-effort stderr write
		"Warning: only %s free on %s — operation may run out of disk space.\n"+
			"  yoloai system disk             # see what's using space\n"+
			"  yoloai system prune            # reclaim cache, no rebuild\n"+
			"  yoloai system prune --images   # also remove base images (forces rebuild)\n",
		HumanBytes(free), path,
	)
	return true
}

// HumanBytes formats a byte count with binary (1024-based) units.
// Mirrors the docker/podman convention used elsewhere in the CLI.
func HumanBytes(n int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
		gib = 1024 * mib
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.2f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
