// ABOUTME: Backend/agent/model/profile resolution helpers and the WithClient /
// ABOUTME: wrappers shared by all Cobra command handlers in internal/cli.
package cliutil

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"

	// Backend registrations live in yoloai.go (the root package);
	// importing yoloai above pulls in those init() side effects, so
	// the blank-import block previously here was redundant. W-L13:
	// removing it lets depguard scope `internal/runtime/tart` to the
	// internal/cli/system/tart subpackage exclusively.
	"github.com/kstenerud/yoloai/internal/sandbox"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/spf13/cobra"
)

// NewRuntime creates a runtime.Runtime for the given backend name.
// Returns an error if the backend is not available on this platform.
func NewRuntime(ctx context.Context, backend string) (runtime.Runtime, error) {
	// Default to docker if no backend specified
	if backend == "" {
		backend = "docker"
	}
	return runtime.New(ctx, backend, Layout())
}

// Flag resolution pattern: each resolve* pair follows the same priority:
//   flag → config → default
//
// The pattern is intentionally not abstracted into a generic helper because
// each pair has domain-specific variations:
//   - ResolveBackend: accepts a --backend flag; used by new/build/setup.
//   - ResolveBackendForSandbox: reads from meta.json, not a flag; used by
//     lifecycle commands (start, stop, attach, diff, apply, destroy).
//   - ResolveAgent: similar flag→config→default; new command only.
//   - ResolveProfile: has a --no-profile bypass that the others don't have.
//
// These differences make a generic abstraction more obscure than the small
// amount of duplicated structure.

// Coalesce returns the first non-empty string.
func Coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ResolveBackend determines the backend from flags, then isolation/os routing,
// then config preference, then auto-detection. Used by commands with --backend.
func ResolveBackend(cmd *cobra.Command) string {
	// Explicit --backend always wins.
	if b, _ := cmd.Flags().GetString("backend"); b != "" {
		return b
	}

	// Read isolation and os from flags, falling back to config.
	cfg, _ := config.LoadDefaultsConfig(Layout())
	var cfgIsolation, cfgOS string
	if cfg != nil {
		cfgIsolation = cfg.Isolation
		cfgOS = cfg.OS
	}
	isolation := Coalesce(FlagStr(cmd, "isolation"), cfgIsolation)
	targetOS := Coalesce(FlagStr(cmd, "os"), cfgOS)

	// OS-based routing: --os mac routes to seatbelt/tart (checked first so
	// --os mac --isolation vm goes to tart, not containerd).
	if targetOS == "mac" {
		if isolation == "vm" {
			return "tart"
		}
		return "seatbelt"
	}

	// Isolation-based routing: vm/vm-enhanced prefer containerd, but fall back
	// if not available (e.g., on macOS where containerd is Linux-only).
	if isolation == "vm" || isolation == "vm-enhanced" {
		if runtime.IsAvailable("containerd") {
			return "containerd"
		}
		// Fall through to container backend detection
	}

	// container/container-enhanced: prefer config, then auto-detect.
	backend, warn := runtime.SelectContainerBackend(cmd.Context(), ResolveContainerBackendConfig())
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	return backend
}

// FlagStr returns the value of a string flag if it was set, or "" if not available.
func FlagStr(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

// ResolveContainerBackendConfig reads the container_backend config preference.
func ResolveContainerBackendConfig() string {
	cfg, err := config.LoadDefaultsConfig(Layout())
	if err == nil {
		return cfg.ContainerBackend
	}
	return ""
}

// ResolveBackendForSandbox reads the backend from a sandbox's meta.json.
// Falls back to config default if meta.json can't be read.
// Used by lifecycle commands that operate on an existing sandbox.
func ResolveBackendForSandbox(name string) string {
	meta, err := store.LoadMeta(Layout().SandboxDir(name))
	if err == nil && meta.Backend != "" {
		return meta.Backend
	}
	// Probe is stat-only so an empty context is fine here; full ctx threading
	// for the rare "meta corrupt" fallback is out of scope for W-L4.
	backend, warn := runtime.SelectContainerBackend(context.Background(), ResolveContainerBackendConfig())
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	return backend
}

// WithClient constructs a yoloai.Client for the given backend, calls fn, and
// closes the client. Preferred entry point for command handlers that only need
// orchestration-level operations (Stop, Destroy, List, Inspect, Diff, Apply,
// Run). The Client wraps a runtime + sandbox.Manager with §12-clean Layout
// derived from Layout(). See internal/cli/CONVENTIONS.md.
func WithClient(cmd *cobra.Command, backend string, fn func(ctx context.Context, c *yoloai.Client) error) error {
	ctx := cmd.Context()
	c, err := yoloai.NewWithOptions(ctx, yoloai.Options{
		DataDir: Layout().DataDir,
		Backend: yoloai.BackendName(backend),
		Logger:  slog.Default(),
		Input:   cmd.InOrStdin(),
		Output:  cmd.ErrOrStderr(),
	})
	if err != nil {
		return fmt.Errorf("connect to runtime: %w", err)
	}
	defer c.Close() //nolint:errcheck // best-effort cleanup
	return fn(ctx, c)
}

// NewSystemClient constructs a backend-agnostic yoloai.SystemClient from
// the CLI's layout. Use for `yoloai system …` command handlers that
// operate across all backends (disk, prune, build --all) or need no
// runtime at all (info, agents). For commands tied to one backend,
// use WithClient instead.
func NewSystemClient() *yoloai.SystemClient {
	return yoloai.NewSystemClient(Layout())
}

// AttachToSandboxByName attaches the calling process's terminal to the
// named sandbox, opening its own Client. Used by lifecycle commands
// (clone, reset, restart, new with --attach) that have already performed
// their lifecycle action and now need to attach.
//
// W-L8d: now routes through yoloai.Client.Attach so all attach paths go
// through one library implementation. Terminal title remains here because
// it's CLI UI; Client.Attach handles status check / wait-for-tmux / PTY.
func AttachToSandboxByName(cmd *cobra.Command, name string) error {
	SetTerminalTitle(name)
	defer SetTerminalTitle("")

	backend := ResolveBackendForSandbox(name)
	return WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		return c.Attach(ctx, name, IOStreams())
	})
}

// ResolveAgent determines the agent name from --agent flag, then config,
// then default. Used by the new command.
func ResolveAgent(cmd *cobra.Command) string {
	if a, _ := cmd.Flags().GetString("agent"); a != "" {
		return a
	}
	return ResolveAgentFromConfig()
}

// ResolveAgentFromConfig reads the agent from defaults config, falling back
// to "claude".
func ResolveAgentFromConfig() string {
	cfg, err := config.LoadDefaultsConfig(Layout())
	if err == nil && cfg.Agent != "" {
		return cfg.Agent
	}
	return "claude"
}

// ResolveModel determines the model name from --model flag, then config,
// then empty string (agent's default). Used by the new command.
func ResolveModel(cmd *cobra.Command) string {
	if m, _ := cmd.Flags().GetString("model"); m != "" {
		return m
	}
	return ResolveModelFromConfig()
}

// ResolveModelFromConfig reads the model from defaults config, falling back
// to "" (no default model — agent uses its own).
func ResolveModelFromConfig() string {
	cfg, err := config.LoadDefaultsConfig(Layout())
	if err == nil && cfg.Model != "" {
		return cfg.Model
	}
	return ""
}

// ResolveProfile determines the profile name from --no-profile, then --profile flag,
// then empty string (no default profile). Used by the new command.
func ResolveProfile(cmd *cobra.Command) string {
	if noProfile, _ := cmd.Flags().GetBool("no-profile"); noProfile {
		return ""
	}
	if p, _ := cmd.Flags().GetString("profile"); p != "" {
		return p
	}
	return ""
}

// SandboxErrorHint wraps an error with the sandbox directory path and a hint
// to use 'yoloai destroy'. Skips the hint for ErrSandboxNotFound (no directory
// to point at).
func SandboxErrorHint(name string, err error) error {
	if err == nil || errors.Is(err, sandbox.ErrSandboxNotFound) {
		return err
	}
	return fmt.Errorf("%w\n  sandbox dir: %s\n  to remove: yoloai destroy %s", err, Layout().SandboxDir(name), name)
}
