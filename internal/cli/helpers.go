package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/kstenerud/yoloai/runtime"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
	seatbeltrt "github.com/kstenerud/yoloai/runtime/seatbelt"
	tartrt "github.com/kstenerud/yoloai/runtime/tart"
	"github.com/kstenerud/yoloai/sandbox"
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

// Flag resolution pattern: each resolve* pair follows the same priority:
//   flag → config → default
//
// The pattern is intentionally not abstracted into a generic helper because
// each pair has domain-specific variations:
//   - resolveBackend: accepts a --backend flag; used by new/build/setup.
//   - resolveBackendForSandbox: reads from meta.json, not a flag; used by
//     lifecycle commands (start, stop, attach, diff, apply, destroy).
//   - resolveAgent: similar flag→config→default; new command only.
//   - resolveProfile: has a --no-profile bypass that the others don't have.
//
// These differences make a generic abstraction more obscure than the small
// amount of duplicated structure.

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

// resolveAgent determines the agent name from --agent flag, then config,
// then default. Used by the new command.
func resolveAgent(cmd *cobra.Command) string {
	if a, _ := cmd.Flags().GetString("agent"); a != "" {
		return a
	}
	return resolveAgentFromConfig()
}

// resolveAgentFromConfig reads the agent from config.yaml, falling back
// to "claude".
func resolveAgentFromConfig() string {
	cfg, err := sandbox.LoadConfig()
	if err == nil && cfg.Agent != "" {
		return cfg.Agent
	}
	return "claude"
}

// resolveModel determines the model name from --model flag, then config,
// then empty string (agent's default). Used by the new command.
func resolveModel(cmd *cobra.Command) string {
	if m, _ := cmd.Flags().GetString("model"); m != "" {
		return m
	}
	return resolveModelFromConfig()
}

// resolveModelFromConfig reads the model from config.yaml, falling back
// to "" (no default model — agent uses its own).
func resolveModelFromConfig() string {
	cfg, err := sandbox.LoadConfig()
	if err == nil && cfg.Model != "" {
		return cfg.Model
	}
	return ""
}

// resolveProfile determines the profile name from --no-profile, --profile flag,
// then config, then empty string (no default profile). Used by the new command.
func resolveProfile(cmd *cobra.Command) string {
	if noProfile, _ := cmd.Flags().GetBool("no-profile"); noProfile {
		return ""
	}
	if p, _ := cmd.Flags().GetString("profile"); p != "" {
		return p
	}
	return resolveProfileFromConfig()
}

// resolveProfileFromConfig reads the profile from config.yaml, falling back
// to "" (no default profile).
func resolveProfileFromConfig() string {
	cfg, err := sandbox.LoadConfig()
	if err == nil && cfg.Profile != "" {
		return cfg.Profile
	}
	return ""
}

// withManager creates a runtime and sandbox manager, calls fn, and ensures cleanup.
func withManager(cmd *cobra.Command, backend string, fn func(ctx context.Context, mgr *sandbox.Manager) error) error {
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		mgr := sandbox.NewManager(rt, backend, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
		return fn(ctx, mgr)
	})
}
