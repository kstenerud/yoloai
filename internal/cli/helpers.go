package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/kstenerud/yoloai/internal/runtime"
	dockerrt "github.com/kstenerud/yoloai/internal/runtime/docker"
	seatbeltrt "github.com/kstenerud/yoloai/internal/runtime/seatbelt"
	tartrt "github.com/kstenerud/yoloai/internal/runtime/tart"
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/spf13/cobra"
)

// newRuntime creates a runtime.Runtime for the given backend name.
func newRuntime(ctx context.Context, backend string) (runtime.Runtime, error) {
	switch backend {
	case "docker", "":
		return dockerrt.New(ctx)
	case "tart":
		return tartrt.New(ctx)
	case "seatbelt":
		return seatbeltrt.New(ctx)
	default:
		return nil, fmt.Errorf("unknown backend: %q (valid: docker, tart, seatbelt)", backend)
	}
}

// resolveBackend determines the backend name from --backend flag, then config,
// then default. Used by commands that accept a --backend flag (new, build, setup).
func resolveBackend(cmd *cobra.Command) string {
	if b, _ := cmd.Flags().GetString("backend"); b != "" {
		return b
	}
	return resolveBackendFromConfig()
}

// resolveBackendForSandbox reads the backend from a sandbox's meta.json.
// Falls back to config default if meta.json can't be read.
// Used by lifecycle commands that operate on an existing sandbox.
func resolveBackendForSandbox(name string) string {
	meta, err := sandbox.LoadMeta(sandbox.Dir(name))
	if err == nil && meta.Backend != "" {
		return meta.Backend
	}
	return resolveBackendFromConfig()
}

// resolveBackendFromConfig reads the backend from config.yaml, falling back
// to "docker". Used by commands that don't have a specific sandbox context
// (e.g., list, stop --all).
func resolveBackendFromConfig() string {
	cfg, err := sandbox.LoadConfig()
	if err == nil && cfg.Backend != "" {
		return cfg.Backend
	}
	return "docker"
}

// withRuntime creates a runtime for the given backend, calls fn, and ensures cleanup.
func withRuntime(ctx context.Context, backend string, fn func(ctx context.Context, rt runtime.Runtime) error) error {
	rt, err := newRuntime(ctx, backend)
	if err != nil {
		return fmt.Errorf("connect to runtime: %w", err)
	}
	defer rt.Close() //nolint:errcheck // best-effort cleanup
	return fn(ctx, rt)
}

// withManager creates a runtime and sandbox manager, calls fn, and ensures cleanup.
func withManager(cmd *cobra.Command, backend string, fn func(ctx context.Context, mgr *sandbox.Manager) error) error {
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		mgr := sandbox.NewManager(rt, backend, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
		return fn(ctx, mgr)
	})
}
