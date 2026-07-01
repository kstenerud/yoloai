// ABOUTME: Filesystem-type detection and warn-once alerting for network filesystems.
// ABOUTME: Surfaces a one-time warning when yoloAI's data dir is on NFS/CIFS/9P/FUSE.

package store

// Package store detects when the data dir sits on a network filesystem so
// the user is warned once rather than silently experiencing data corruption.
// Background: yoloAI uses POSIX advisory locks (flock(2)) plus atomic rename(2)
// for sandbox mutual exclusion. Both are unreliable on network filesystems:
//   - flock(2) on NFS is not atomic across hosts and is ignored by many servers.
//   - CIFS/SMB servers implement their own byte-range locking, not POSIX advisory.
//   - 9P and FUSE semantics depend on the backing server — no safety guarantee.
//   - AFS uses its own distributed lock manager; POSIX flocks are silently no-ops.
//
// The warning is advisory, not a hard block, so legitimate single-host NFSv4
// users (where intra-host flock often works) are not locked out.

import (
	"log/slog"
	"sync"
)

// netFSCheckedDirs prevents redundant statfs calls: once a directory path
// has been probed, subsequent lock acquisitions on the same path skip it.
var netFSCheckedDirs sync.Map

// netFSWarnedDirs prevents repeated warnings: once a network-FS dir has
// been flagged, warnNetworkFSOnce is a no-op for subsequent acquisitions.
var netFSWarnedDirs sync.Map

// warnNetworkFSOnce calls warnFn(dir, fsName) at most once per distinct
// dir path. sync.Map.LoadOrStore gives atomic first-writer semantics so
// concurrent goroutines cannot double-warn. Accepting warnFn as a parameter
// keeps the dedup logic injectable and unit-testable without a real mount.
func warnNetworkFSOnce(dir, fsName string, warnFn func(dir, fsName string)) {
	if _, alreadyDone := netFSWarnedDirs.LoadOrStore(dir, struct{}{}); alreadyDone {
		return
	}
	warnFn(dir, fsName)
}

// defaultNetFSWarn logs the network-filesystem hazard via slog.Warn.
// slog.Warn reaches the user's stderr at the default log level (LevelWarn)
// without requiring -v; see internal/cli/cliutil/logger.go InitLogger which
// sets stderrLevel=LevelWarn by default. No separate os.Stderr write is needed.
func defaultNetFSWarn(dir, fsName string) {
	slog.Warn(
		"yoloAI data dir is on a network filesystem; advisory file locking (flock) "+
			"can be unreliable there and concurrent yoloai processes on different hosts "+
			"may corrupt sandbox state. Use a local filesystem for ~/.yoloai if possible.",
		"fs", fsName,
		"path", dir,
		"event", "store.lock.network_fs",
	)
}

// checkNetworkFS probes dir's filesystem type once per process lifetime and
// issues a one-time warning if it is a known network or FUSE filesystem where
// flock(2) is unreliable. Statfs errors are silently swallowed — a probe
// failure must never block a lock acquisition.
//
// The two-level dedup (netFSCheckedDirs + netFSWarnedDirs) ensures:
//  1. statfs is called at most once per dir path across all goroutines.
//  2. The user sees the warning at most once per dir path per process lifetime.
func checkNetworkFS(dir string) {
	if _, alreadyDone := netFSCheckedDirs.LoadOrStore(dir, struct{}{}); alreadyDone {
		return
	}
	name, isNet := networkFilesystemName(dir)
	if isNet {
		warnNetworkFSOnce(dir, name, defaultNetFSWarn)
	}
}
