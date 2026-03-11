//go:build !darwin

package workspace

import "errors"

// cloneDir is a no-op on non-macOS platforms, always returning an error to
// trigger fallback to the regular walk-based copy.
func cloneDir(_, _ string) error {
	return errors.New("clonefile not supported")
}
