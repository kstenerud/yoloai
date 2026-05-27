// ABOUTME: Backend/agent/model/profile resolution helpers and the withClient /
// ABOUTME: wrappers shared by all Cobra command handlers in internal/cli.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/config"
	"github.com/kstenerud/yoloai/runtime"
	_ "github.com/kstenerud/yoloai/runtime/docker"   // register backend
	_ "github.com/kstenerud/yoloai/runtime/podman"   // register backend
	_ "github.com/kstenerud/yoloai/runtime/seatbelt" // register backend
	_ "github.com/kstenerud/yoloai/runtime/tart"     // register backend
	"github.com/kstenerud/yoloai/sandbox"
	"github.com/kstenerud/yoloai/sandbox/store"
	"github.com/spf13/cobra"
)

// newRuntime creates a runtime.Runtime for the given backend name.
// Returns an error if the backend is not available on this platform.
func newRuntime(ctx context.Context, backend string) (runtime.Runtime, error) {
	// Default to docker if no backend specified
	if backend == "" {
		backend = "docker"
	}
	return runtime.New(ctx, backend, cliLayout())
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
	cfg, _ := config.LoadDefaultsConfig(cliLayout())
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

	// Isolation-based routing: vm/vm-enhanced prefer containerd, but fall back
	// if not available (e.g., on macOS where containerd is Linux-only).
	if isolation == "vm" || isolation == "vm-enhanced" {
		if runtime.IsAvailable("containerd") {
			return "containerd"
		}
		// Fall through to container backend detection
	}

	// container/container-enhanced: prefer config, then auto-detect.
	backend, warn := runtime.SelectContainerBackend(cmd.Context(), resolveContainerBackendConfig())
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	return backend
}

// flagStr returns the value of a string flag if it was set, or "" if not available.
func flagStr(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

// resolveContainerBackendConfig reads the container_backend config preference.
func resolveContainerBackendConfig() string {
	cfg, err := config.LoadDefaultsConfig(cliLayout())
	if err == nil {
		return cfg.ContainerBackend
	}
	return ""
}

// resolveBackendForSandbox reads the backend from a sandbox's meta.json.
// Falls back to config default if meta.json can't be read.
// Used by lifecycle commands that operate on an existing sandbox.
func resolveBackendForSandbox(name string) string {
	meta, err := store.LoadMeta(cliLayout().SandboxDir(name))
	if err == nil && meta.Backend != "" {
		return meta.Backend
	}
	// Probe is stat-only so an empty context is fine here; full ctx threading
	// for the rare "meta corrupt" fallback is out of scope for W-L4.
	backend, warn := runtime.SelectContainerBackend(context.Background(), resolveContainerBackendConfig())
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	return backend
}

// withClient constructs a yoloai.Client for the given backend, calls fn, and
// closes the client. Preferred entry point for command handlers that only need
// orchestration-level operations (Stop, Destroy, List, Inspect, Diff, Apply,
// Run). The Client wraps a runtime + sandbox.Manager with §12-clean Layout
// derived from cliLayout(). See internal/cli/CONVENTIONS.md.
func withClient(cmd *cobra.Command, backend string, fn func(ctx context.Context, c *yoloai.Client) error) error {
	ctx := cmd.Context()
	c, err := yoloai.NewWithOptions(ctx, yoloai.Options{
		DataDir: cliLayout().DataDir,
		Backend: backend,
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

// systemClient constructs a backend-agnostic yoloai.SystemClient from
// the CLI's layout. Use for `yoloai system …` command handlers that
// operate across all backends (disk, prune, build --all) or need no
// runtime at all (info, agents). For commands tied to one backend,
// use withClient instead.
func systemClient() *yoloai.SystemClient {
	return yoloai.NewSystemClient(cliLayout())
}

// attachToSandboxByName attaches the calling process's terminal to the
// named sandbox, opening its own Client. Used by lifecycle commands
// (clone, reset, restart, new with --attach) that have already performed
// their lifecycle action and now need to attach.
//
// W-L8d: now routes through yoloai.Client.Attach so all attach paths go
// through one library implementation. Terminal title remains here because
// it's CLI UI; Client.Attach handles status check / wait-for-tmux / PTY.
func attachToSandboxByName(cmd *cobra.Command, name string) error {
	setTerminalTitle(name)
	defer setTerminalTitle("")

	backend := resolveBackendForSandbox(name)
	return withClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		return c.Attach(ctx, name, cliIOStreams())
	})
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
	cfg, err := config.LoadDefaultsConfig(cliLayout())
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
	cfg, err := config.LoadDefaultsConfig(cliLayout())
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

// sandboxErrorHint wraps an error with the sandbox directory path and a hint
// to use 'yoloai destroy'. Skips the hint for ErrSandboxNotFound (no directory
// to point at).
func sandboxErrorHint(name string, err error) error {
	if err == nil || errors.Is(err, sandbox.ErrSandboxNotFound) {
		return err
	}
	return fmt.Errorf("%w\n  sandbox dir: %s\n  to remove: yoloai destroy %s", err, cliLayout().SandboxDir(name), name)
}
