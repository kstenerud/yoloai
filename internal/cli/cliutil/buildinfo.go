// ABOUTME: Build-time CLI version/commit/date — set once in Execute() and
// ABOUTME: read by subpackages (sandbox bugreport, version subcommand) that
// ABOUTME: need it without threading it through every cobra runfunc.

package cliutil

// Build-time CLI metadata stamped via -ldflags at compile time.
// Set by SetBuildInfo at the start of Execute; read by subpackages.
var (
	Version string
	Commit  string
	Date    string
)

// SetBuildInfo records the version/commit/date passed in from main.
// Called once from Execute() before any command runs.
func SetBuildInfo(version, commit, date string) {
	Version, Commit, Date = version, commit, date
}
