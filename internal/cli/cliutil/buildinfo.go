// ABOUTME: Build-time CLI version/commit/date — set once in Execute() and
// ABOUTME: read by subpackages (sandbox bugreport, version subcommand) that
// ABOUTME: need it without threading it through every cobra runfunc.

package cliutil

import "github.com/kstenerud/yoloai/internal/buildinfo"

// SetBuildInfo records the version/commit/date passed in from main.
// Called once from Execute() before any command runs. Delegates to the
// internal/buildinfo leaf package, which is the canonical store so that
// lower-level packages (e.g. runtime/tart) can read build info without
// importing cli/cliutil.
func SetBuildInfo(version, commit, date string) {
	buildinfo.Set(version, commit, date)
}
