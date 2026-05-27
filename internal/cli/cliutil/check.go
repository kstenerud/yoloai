// ABOUTME: CheckBackend — probe a backend for availability by spinning up a
// ABOUTME: runtime, closing it, and reporting any construction error.

package cliutil

import (
	"context"
)

// CheckBackend attempts to create a runtime for the given backend
// name. Returns availability and a short note on failure. Used by
// command handlers that need to assert a backend is reachable
// before invoking it (e.g., `yoloai ls` enumerating all backends,
// `yoloai system check`, `yoloai system tart` gating itself behind
// Tart availability).
func CheckBackend(ctx context.Context, name string) (available bool, note string) {
	rt, err := NewRuntime(ctx, name)
	if err != nil {
		return false, err.Error()
	}
	_ = rt.Close()
	return true, ""
}
