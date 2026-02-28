package seatbelt

// ABOUTME: No-op Prune implementation for seatbelt backend (no central registry).

import (
	"context"
	"io"

	"github.com/kstenerud/yoloai/internal/runtime"
)

// Prune implements runtime.Runtime. Seatbelt has no central instance registry,
// so this is a no-op.
func (r *Runtime) Prune(_ context.Context, _ []string, _ bool, _ io.Writer) (runtime.PruneResult, error) {
	return runtime.PruneResult{}, nil
}
