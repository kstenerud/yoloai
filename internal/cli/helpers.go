package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
	containerdrt "github.com/kstenerud/yoloai/runtime/containerd"
	dockerrt "github.com/kstenerud/yoloai/runtime/docker"
	podmanrt "github.com/kstenerud/yoloai/runtime/podman"
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
	case "podman":
		return podmanrt.New(ctx)
	case "tart":
		return tartrt.New(ctx)
	case "seatbelt":
		return seatbeltrt.New(ctx)
	case "containerd":
		return containerdrt.New(ctx)
	default:
		return nil, fmt.Errorf("unknown backend: %q (valid: docker, podman, tart, seatbelt, containerd)", backend)
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

// coalesce returns the first non-empty string.
func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// resolveBackend determines the backend from flags, then isolation/os routing,
// then config preference, then auto-detection. Used by commands with --backend.
func resolveBackend(cmd *cobra.Command) string {
	// Explicit --backend always wins.
	if b, _ := cmd.Flags().GetString("backend"); b != "" {
		return b
	}

	// Read isolation and os from flags, falling back to config.
	cfg, _ := config.LoadDefaultsConfig()
	var cfgIsolation, cfgOS string
	if cfg != nil {
		cfgIsolation = cfg.Isolation
		cfgOS = cfg.OS
	}
	isolation := coalesce(flagStr(cmd, "isolation"), cfgIsolation)
	targetOS := coalesce(flagStr(cmd, "os"), cfgOS)

	// OS-based routing: --os mac routes to seatbelt/tart (checked first so
	// --os mac --isolation vm goes to tart, not containerd).
	if targetOS == "mac" {
		if isolation == "vm" {
			return "tart"
		}
		return "seatbelt"
	}

	// Isolation-based routing: vm/vm-enhanced use containerd on Linux.
	if isolation == "vm" || isolation == "vm-enhanced" {
		return "containerd"
	}

	// container/container-enhanced: prefer config, then auto-detect.
	return detectContainerBackend(resolveContainerBackendConfig())
}

// flagStr returns the value of a string flag if it was set, or "" if not available.
func flagStr(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

// detectContainerBackend picks docker or podman based on a config preference
// and socket availability. Warns to stderr if the preferred backend isn't found.
func detectContainerBackend(preference string) string {
	if preference == "podman" {
		if podmanrt.SocketExists() {
			return "podman"
		}
		fmt.Fprintf(os.Stderr, "Warning: container_backend=podman not found; falling back to docker\n")
	}
	if dockerAvailable() {
		return "docker"
	}
	if preference == "docker" {
		fmt.Fprintf(os.Stderr, "Warning: container_backend=docker not found; falling back to podman\n")
	}
	if podmanrt.SocketExists() {
		return "podman"
	}
	return "docker" // will fail hard in newRuntime() with a clear error
}

// dockerAvailable returns true if the Docker socket is reachable (stat only, no dial).
func dockerAvailable() bool {
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		return true // assume reachable if explicitly configured
	}
	_, err := os.Stat("/var/run/docker.sock")
	return err == nil
}

// resolveContainerBackendConfig reads the container_backend config preference.
func resolveContainerBackendConfig() string {
	cfg, err := config.LoadDefaultsConfig()
	if err == nil {
		return cfg.ContainerBackend
	}
	return ""
}

// resolveBackendForSandbox reads the backend from a sandbox's meta.json.
// Falls back to config default if meta.json can't be read.
// Used by lifecycle commands that operate on an existing sandbox.
func resolveBackendForSandbox(name string) string {
	meta, err := sandbox.LoadMeta(sandbox.Dir(name))
	if err == nil && meta.Backend != "" {
		return meta.Backend
	}
	return detectContainerBackend(resolveContainerBackendConfig())
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

// resolveAgentFromConfig reads the agent from defaults config, falling back
// to "claude".
func resolveAgentFromConfig() string {
	cfg, err := config.LoadDefaultsConfig()
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

// resolveModelFromConfig reads the model from defaults config, falling back
// to "" (no default model — agent uses its own).
func resolveModelFromConfig() string {
	cfg, err := config.LoadDefaultsConfig()
	if err == nil && cfg.Model != "" {
		return cfg.Model
	}
	return ""
}

// resolveProfile determines the profile name from --no-profile, then --profile flag,
// then empty string (no default profile). Used by the new command.
func resolveProfile(cmd *cobra.Command) string {
	if noProfile, _ := cmd.Flags().GetBool("no-profile"); noProfile {
		return ""
	}
	if p, _ := cmd.Flags().GetString("profile"); p != "" {
		return p
	}
	return ""
}

// withManager creates a runtime and sandbox manager, calls fn, and ensures cleanup.
func withManager(cmd *cobra.Command, backend string, fn func(ctx context.Context, mgr *sandbox.Manager) error) error {
	return withRuntime(cmd.Context(), backend, func(ctx context.Context, rt runtime.Runtime) error {
		mgr := sandbox.NewManager(rt, slog.Default(), cmd.InOrStdin(), cmd.ErrOrStderr())
		return fn(ctx, mgr)
	})
}

// sandboxErrorHint wraps an error with the sandbox directory path and a hint
// to use 'yoloai destroy'. Skips the hint for ErrSandboxNotFound (no directory
// to point at).
func sandboxErrorHint(name string, err error) error {
	if err == nil || errors.Is(err, sandbox.ErrSandboxNotFound) {
		return err
	}
	return fmt.Errorf("%w\n  sandbox dir: %s\n  to remove: yoloai destroy %s", err, sandbox.Dir(name), name)
}
