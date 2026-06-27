//go:build windows

// ABOUTME: Windows stub for AcquireBaseLock — no-op since flock(2) is
// ABOUTME: unavailable; the Docker backend's Setup proceeds without the
// ABOUTME: cross-process race protection on Windows (preexisting limitation).
package docker

import "github.com/kstenerud/yoloai/internal/config"

// AcquireBaseLock is a no-op on Windows. Returns a noop release
// function so the call site in docker.go's Setup compiles uniformly.
func AcquireBaseLock(_ config.Layout, _ string) (func(), error) {
	return func() {}, nil
}
