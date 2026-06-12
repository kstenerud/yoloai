// ABOUTME: DirMode — typed enum for sandbox directory mount modes
// ABOUTME: (copy / overlay / rw / ro). Lives in store so persisted
// ABOUTME: meta types (DirEnvironment) can hold typed values
// ABOUTME: instead of bare strings.

package store

// DirMode names how a directory is exposed to the sandbox. Closed set:
// the four constants below are the only valid values. JSON round-trip
// works via the underlying string type (no MarshalText needed).
//
// Lives in store rather than the parent sandbox package because
// store/environment.go's persisted DirEnvironment type holds Mode
// values. Parent-package re-export (sandbox.DirMode = store.DirMode)
// keeps existing internal/sandbox callers working without churn.
type DirMode string

const (
	DirModeCopy    DirMode = "copy"    // full copy; changes tracked via git (workdir only)
	DirModeOverlay DirMode = "overlay" // overlayfs; original untouched (workdir only)
	DirModeRW      DirMode = "rw"      // live bind-mount; changes immediate
	DirModeRO      DirMode = "ro"      // read-only bind-mount (aux dirs only)
)
