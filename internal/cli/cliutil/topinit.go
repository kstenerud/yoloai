// ABOUTME: TOP/.initializing — the sentinel that brackets a fresh TOP build so
// ABOUTME: a crash between the two realms is a recorded fact, not a guess (DF128).

package cliutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/kstenerud/yoloai/internal/fileutil"
)

// MarkInitializing writes TOP/.initializing durably. Call this before either
// realm exists — it is the dual of D110's crash-safe migration stamp, which
// is written *last* so it can never precede the data it certifies; this one
// is written *first*, so that a crash between building the CLI and library
// realms leaves a fact on disk instead of a state the startup gate has to
// guess at.
//
// This is not a lock. Nothing takes it, and two `yoloai` processes racing to
// initialize the same TOP concurrently is unhandled, exactly as it was before
// this existed — its only job is to make an interrupted build detectable,
// never to prevent one.
func MarkInitializing() error {
	return fileutil.AtomicWriteFile(TopInitializingSentinelPath(), nil, 0600)
}

// ClearInitializing removes TOP/.initializing once both realms are built and
// current. A missing sentinel is not an error, so this is safe to call
// unconditionally at the end of a build that may not have needed to mark one.
func ClearInitializing() error {
	if err := os.Remove(TopInitializingSentinelPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove initializing sentinel: %w", err)
	}
	return nil
}

// IsInitializing reports whether TOP/.initializing is present: a fresh TOP
// build was started and has not been observed to finish. That is the whole
// claim — it is a fact about an event, not about what the realms contain.
//
// In particular it does NOT mean "the realms are skeletal, so a rebuild is
// safe". The sentinel outlives its build whenever the final remove fails, and
// a stale one can be arbitrarily old — old enough for the realms beneath it to
// hold real work and to need migrating. Treating it as permission to re-create
// or re-stamp a realm re-stamps data that was never converted, which is D110's
// truth invariant inverted (the migration stamp is written last so it can never
// precede the data it certifies; this one is written first, so it certifies
// nothing at all). Callers must check what is actually on disk before acting.
func IsInitializing() bool {
	_, err := os.Stat(TopInitializingSentinelPath())
	return err == nil
}
