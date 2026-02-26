package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/kstenerud/yoloai/internal/runtime"
	dockerrt "github.com/kstenerud/yoloai/internal/runtime/docker"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// newRuntime creates a runtime.Runtime based on the configured backend.
// Currently only Docker is supported; future backends (Tart, etc.) will
// be dispatched here based on config.
func newRuntime(ctx context.Context) (runtime.Runtime, error) {
	return dockerrt.New(ctx)
}

// withRuntime creates a runtime, calls fn, and ensures cleanup.
func withRuntime(cmd *cobra.Command, fn func(ctx context.Context, rt runtime.Runtime) error) error {
	ctx := cmd.Context()
	rt, err := newRuntime(ctx)
	if err != nil {
		return fmt.Errorf("connect to runtime: %w", err)
	}
	defer rt.Close() //nolint:errcheck // best-effort cleanup
	return fn(ctx, rt)
}

// withManager creates a runtime and sandbox manager, calls fn, and ensures cleanup.
func withManager(cmd *cobra.Command, fn func(ctx context.Context, mgr *sandbox.Manager) error) error {
	return withRuntime(cmd, func(ctx context.Context, rt runtime.Runtime) error {
		mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
		return fn(ctx, mgr)
	})
}
