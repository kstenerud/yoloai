package tart

// ABOUTME: Platform detection helpers, testable via variable overrides.

import (
	"runtime"
	"time"
)

// goos and goarch are variables so tests can override them.
var (
	goos   = func() string { return runtime.GOOS }
	goarch = func() string { return runtime.GOARCH }
)

// bootPollInterval controls how often waitForBoot polls the VM.
var bootPollInterval = 2 * time.Second

// waitTick returns a channel that fires after bootPollInterval.
func waitTick() <-chan time.Time {
	return time.After(bootPollInterval)
}
