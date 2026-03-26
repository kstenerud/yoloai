//go:build windows

package tart

import "fmt"

// AcquireBaseLock is not implemented on Windows (Tart is macOS-only)
func AcquireBaseLock(baseName string) (func(), error) {
	return nil, fmt.Errorf("tart runtime not supported on Windows")
}
