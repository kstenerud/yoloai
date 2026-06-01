// ABOUTME: CheckBackend — probe a backend for availability by spinning up a
// ABOUTME: runtime, closing it, and reporting any construction error.

package cliutil

import (
	"context"

	"github.com/kstenerud/yoloai"
)

// CheckBackend attempts to create a runtime for the given backend
// name. Returns availability and a short note on failure. Used by
// command handlers that need to assert a backend is reachable
// before invoking it (e.g., `yoloai ls` enumerating all backends,
// `yoloai system check`, `yoloai system tart` gating itself behind
// Tart availability). Routes through the public SystemClient verb so
// the CLI stays free of internal/runtime construction.
func CheckBackend(ctx context.Context, name yoloai.BackendName) (available bool, note string) {
	return NewSystemClient().CheckBackend(ctx, name)
}
