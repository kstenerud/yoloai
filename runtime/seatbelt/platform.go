package seatbelt

// ABOUTME: Platform detection helpers, testable via variable override.

import "runtime"

// goos is a variable so tests can override it.
var goos = func() string { return runtime.GOOS }

// isMacOS returns true if running on macOS.
func isMacOS() bool {
	return goos() == "darwin"
}
