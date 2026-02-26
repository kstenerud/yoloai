package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/kstenerud/yoloai/internal/runtime"
	dockerrt "github.com/kstenerud/yoloai/internal/runtime/docker"
	tartrt "github.com/kstenerud/yoloai/internal/runtime/tart"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// newRuntime creates a runtime.Runtime for the given backend name.
// Currently only Docker is supported; future backends (Tart, etc.) will
// be dispatched here.
func newRuntime(ctx context.Context, backend string) (runtime.Runtime, error) {
	switch backend {
	case "docker", "":
		return dockerrt.New(ctx)
	case "tart":
		return tartrt.New(ctx)
	default:
		return nil, fmt.Errorf("unknown backend: %q (valid: docker, tart)", backend)
	}
}

// resolveBackend determines the backend name from CLI flag, config, or default.
func resolveBackend(cmd *cobra.Command) string {
	if b, _ := cmd.Flags().GetString("backend"); b != "" {
		return b
	}
	cfg, err := sandbox.LoadConfig()
	if err == nil && cfg.Backend != "" {
		return cfg.Backend
	}
	return "docker"
}

// withRuntime creates a runtime, calls fn, and ensures cleanup.
func withRuntime(cmd *cobra.Command, fn func(ctx context.Context, rt runtime.Runtime) error) error {
	ctx := cmd.Context()
	backend := resolveBackend(cmd)
	rt, err := newRuntime(ctx, backend)
	if err != nil {
		return fmt.Errorf("connect to runtime: %w", err)
	}
	defer rt.Close() //nolint:errcheck // best-effort cleanup
	return fn(ctx, rt)
}

// withManager creates a runtime and sandbox manager, calls fn, and ensures cleanup.
func withManager(cmd *cobra.Command, fn func(ctx context.Context, mgr *sandbox.Manager) error) error {
	return withRuntime(cmd, func(ctx context.Context, rt runtime.Runtime) error {
		backend := resolveBackend(cmd)
		mgr := sandbox.NewManager(rt, backend, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
		return fn(ctx, mgr)
	})
}
