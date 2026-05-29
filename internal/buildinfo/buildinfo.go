// ABOUTME: Build-time version/commit/date set once at startup and read by any
// ABOUTME: package that needs it (CLI, runtime backends) without import cycles.

package buildinfo

// Build-time metadata stamped via -ldflags at compile time and recorded here
// by Set() at the start of Execute(). This is the lowest-level home for the
// values so leaf packages like runtime/tart can read them without importing
// cli/cliutil (which would invert the dependency direction).
var (
	Version string
	Commit  string
	Date    string
)

// Set records the version/commit/date passed in from main. Called once before
// any command runs.
func Set(version, commit, date string) {
	Version, Commit, Date = version, commit, date
}
