//go:build !darwin

// ABOUTME: Non-macOS stub for cloneDir that always returns an error, causing
// ABOUTME: CopyDir to fall through to the portable walk-based implementation.
package workspace

import "errors"

// cloneDir is a no-op on non-macOS platforms, always returning an error to
// trigger fallback to the regular walk-based copy.
func cloneDir(_, _ string) error {
	return errors.New("clonefile not supported")
}
