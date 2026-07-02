// ABOUTME: SweepStaleTestHomes reclaims integration-test bootstrap HOMEs that a
// ABOUTME: killed run (SIGKILL/-timeout/OOM — none run deferred cleanup) left in
// ABOUTME: the temp dir. The live run cleans its own HOME via a run(m) int defer;
// ABOUTME: this recovers leftovers from PRIOR killed runs. See `make clean-testtmp`.
package testutil

import (
	"os"
	"path/filepath"
	"time"
)

// staleTestHomeAge is how long a leftover integration-test HOME must sit
// untouched before SweepStaleTestHomes treats it as leaked (rather than a
// concurrently-running test's live HOME). Generous on purpose: a leftover HOME is
// small and harmless until then, and the goal is to never clobber an active run.
const staleTestHomeAge = time.Hour

// SweepStaleTestHomes best-effort removes integration-test bootstrap HOMEs left in
// the temp dir by a PRIOR run that was killed before its deferred cleanup could
// run (SIGKILL, a -timeout kill, or OOM — none of which run defers, so the live
// run's own cleanup never fired). It only touches temp dirs whose name starts
// with the given yoloai-specific prefix and that have been untouched for
// staleTestHomeAge, so it never removes another project's temp files or a
// concurrently-running test's live HOME. Call at the start of an integration
// TestMain. Errors are ignored — this is housekeeping, not correctness.
//
// Caveat: a HOME that contains a rootless-podman store has files owned by
// uid-remapped (userns) ids, so os.RemoveAll can't delete them (the partial dir
// lingers). Post-DF56 the test pre-seed keeps podman from building into the HOME
// so this is rare; `make clean-testtmp` clears those via `podman unshare`.
func SweepStaleTestHomes(prefix string) {
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), prefix+"*"))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleTestHomeAge)
	for _, dir := range matches {
		fi, statErr := os.Stat(dir)
		if statErr != nil || !fi.IsDir() || fi.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(dir)
	}
}
