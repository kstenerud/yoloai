//go:build windows

// ABOUTME: Windows stub for AcquireBaseLock — always errors because Tart is macOS-only.
// ABOUTME: Keeps the tart package compilable on Windows without build-tag exclusions.
package tart

import (
	"fmt"

	"github.com/kstenerud/yoloai/config"
)

// AcquireBaseLock is not implemented on Windows (Tart is macOS-only).
func AcquireBaseLock(_ config.Layout, baseName string) (func(), error) {
	return nil, fmt.Errorf("tart runtime not supported on Windows")
}
