// ABOUTME: Backend/agent/model/profile resolution helpers and the WithClient /
// ABOUTME: wrappers shared by all Cobra command handlers in internal/cli.
package cliutil

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	// Backend registrations live in client.go (the root package);
	// importing yoloai below pulls in those init() side effects, so a
	// blank-import block here is redundant. Keeping this package free of
	// any internal/runtime import is what lets cli-runtime-scope fence the
	// whole CLI off the runtime layer (tart now goes through the public
	// System.TartBases handle, so there is no backend exemption).
	yoloai "github.com/kstenerud/yoloai"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/spf13/cobra"
)

// Flag resolution pattern: each resolve* pair follows the same priority:
//   flag → config → default
//
// The pattern is intentionally not abstracted into a generic helper because
// each pair has domain-specific variations:
//   - ResolveBackend: accepts a --backend flag; used by new/build/setup.
//   - ResolveBackendForSandbox: reads from environment.json, not a flag; used by
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
//
// The flag/config reads (the CLI's job) stay here; the routing decision itself
// is delegated to runtime.SelectBackend so the CLI and library embedders share
// one routing implementation (F21).
func ResolveBackend(cmd *cobra.Command) yoloai.BackendType {
	// Explicit --backend always wins.
	if b, _ := cmd.Flags().GetString("backend"); b != "" {
		return yoloai.BackendType(b)
	}

	// Read isolation and os from flags, falling back to config.
	cfg, _ := config.LoadDefaultsConfig(Layout())
	var cfgIsolation, cfgOS string
	if cfg != nil {
		cfgIsolation = cfg.Isolation
		cfgOS = cfg.OS
	}
	isolation := yoloai.IsolationMode(Coalesce(FlagStr(cmd, "isolation"), cfgIsolation))
	targetOS := Coalesce(FlagStr(cmd, "os"), cfgOS)

	backend, warn := yoloai.SelectBackend(cmd.Context(), ResolveContainerBackendConfig(), isolation, targetOS, Layout().Env().EnvForDaemonDiscovery())
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
func ResolveContainerBackendConfig() yoloai.BackendType {
	cfg, err := config.LoadDefaultsConfig(Layout())
	if err == nil {
		return yoloai.BackendType(cfg.ContainerBackend)
	}
	return ""
}

// ResolveBackendForSandbox reads the backend from a sandbox's environment.json.
// Falls back to config default if environment.json can't be read.
// Used by lifecycle commands that operate on an existing sandbox.
func ResolveBackendForSandbox(name string) yoloai.BackendType {
	l := Layout()
	c, err := yoloai.NewClient(context.Background(), yoloai.ClientCreateOptions{DataDir: l.DataDir, HomeDir: l.HomeDir, Env: EdgeEnv()})
	if err == nil {
		defer c.Close() //nolint:errcheck // backend-less close is a no-op
		if sb, sbErr := c.Sandbox(name); sbErr == nil {
			if env, mErr := sb.Metadata(); mErr == nil && env.BackendType != "" {
				return env.BackendType
			}
		}
	}
	// Probe is stat-only so an empty context is fine here.
	backend, warn := yoloai.SelectContainerBackend(context.Background(), ResolveContainerBackendConfig(), Layout().Env().EnvForDaemonDiscovery())
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	return backend
}

// WithClient constructs a yoloai.Client for the given backend, calls fn, and
// closes the client. Preferred entry point for command handlers that only need
// orchestration-level operations (Stop, Destroy, List, Inspect, Diff, Apply,
// Run). The Client wraps a runtime + sandbox.Engine with §12-clean Layout
// derived from Layout(). See internal/cli/CONVENTIONS.md.
func WithClient(cmd *cobra.Command, backend yoloai.BackendType, fn func(ctx context.Context, c *yoloai.Client) error) error {
	ctx := cmd.Context()
	l := Layout()
	c, err := yoloai.NewClient(ctx, yoloai.ClientCreateOptions{
		DataDir:     l.DataDir,
		HomeDir:     l.HomeDir,
		BackendType: backend,
		Logger:      slog.Default(),
		Input:       cmd.InOrStdin(),
		Output:      cmd.ErrOrStderr(),
		Env:         EdgeEnv(),
	})
	if err != nil {
		return fmt.Errorf("connect to runtime: %w", err)
	}
	defer c.Close() //nolint:errcheck // best-effort cleanup
	return fn(ctx, c)
}

// WithSandbox folds the per-sandbox command prologue that recurs across the
// workflow/lifecycle/sandbox commands: resolve the sandbox's backend from its
// environment.json, open a Client on it, look up the named Sandbox, and invoke
// fn. Use it instead of hand-rolling ResolveBackendForSandbox + WithClient +
// Client.Sandbox(name). For commands that only touch the changes view, prefer
// the narrower WithWorkdir.
func WithSandbox(cmd *cobra.Command, name string, fn func(ctx context.Context, sb *yoloai.Sandbox) error) error {
	backend := ResolveBackendForSandbox(name)
	return WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, err := c.Sandbox(name)
		if err != nil {
			return SandboxErrorHint(name, err)
		}
		return fn(ctx, sb)
	})
}

// WithWorkdir is WithSandbox narrowed to the sandbox's Workdir sub-handle, for
// the diff/apply/baseline commands that only operate on the changes view.
func WithWorkdir(cmd *cobra.Command, name string, fn func(ctx context.Context, wd *yoloai.Workdir) error) error {
	return WithSandbox(cmd, name, func(ctx context.Context, sb *yoloai.Sandbox) error {
		return fn(ctx, sb.Workdir())
	})
}

// Client constructs a backend-less yoloai.Client from the CLI's layout —
// no backend is selected, so the runtime is never opened. Use it for command
// handlers that only need host-only per-sandbox reads (prompt, agent-log,
// activity stream, terminal snapshot) reached via Client.Sandbox(name).Agent().
// For commands that drive the container (start/stop/exec/attach) use WithClient,
// which passes a resolved backend; for cross-backend admin use System().
//
// The caller is responsible for Close() (a no-op on a backend-less Client).
func Client(cmd *cobra.Command) (*yoloai.Client, error) {
	l := Layout()
	return yoloai.NewClient(cmd.Context(), yoloai.ClientCreateOptions{
		DataDir: l.DataDir,
		HomeDir: l.HomeDir,
		Logger:  slog.Default(),
		Input:   cmd.InOrStdin(),
		Output:  cmd.ErrOrStderr(),
		Env:     EdgeEnv(),
	})
}

// System constructs a backend-agnostic yoloai.System from the CLI's layout. Use
// for `yoloai system …` command handlers that operate across all backends (disk,
// prune, build --all) or need no runtime at all (info, agents). For commands tied
// to one backend, use WithClient instead.
//
// It builds a backend-less Client (no runtime opened) and returns its System
// sub-handle. The caller need not Close — a backend-less Client's Close is a no-op.
func System() (*yoloai.System, error) {
	l := Layout()
	c, err := yoloai.NewClient(context.Background(), yoloai.ClientCreateOptions{
		DataDir: l.DataDir,
		HomeDir: l.HomeDir,
		Env:     EdgeEnv(),
	})
	if err != nil {
		return nil, err
	}
	return c.System(), nil
}

// SandboxMetadata reads a sandbox's persisted read-model (environment.json)
// using a backend-less Client, so no runtime is opened. Command handlers use it
// for the early "load meta to branch on mount mode / overlay / baseline" reads
// that precede the backend-driving WithClient call.
func SandboxMetadata(cmd *cobra.Command, name string) (*yoloai.Environment, error) {
	c, err := Client(cmd)
	if err != nil {
		return nil, err
	}
	defer c.Close() //nolint:errcheck // backend-less close is a no-op
	sb, err := c.Sandbox(name)
	if err != nil {
		return nil, SandboxErrorHint(name, err)
	}
	return sb.Metadata()
}

// AttachToSandboxByName attaches the calling process's terminal to the
// named sandbox, opening its own Client. Used by lifecycle commands
// (clone, reset, restart, new with --attach) that have already performed
// their lifecycle action and now need to attach.
//
// Routes through yoloai.Client.Attach so all attach paths share one library
// implementation. Terminal title remains here because it's CLI UI;
// Client.Attach handles status check / wait-for-tmux / PTY.
func AttachToSandboxByName(cmd *cobra.Command, name string) error {
	SetTerminalTitle(name)
	defer SetTerminalTitle("")

	backend := ResolveBackendForSandbox(name)
	return WithClient(cmd, backend, func(ctx context.Context, c *yoloai.Client) error {
		sb, err := c.Sandbox(name)
		if err != nil {
			return err
		}
		return WithTerminal(func(io yoloai.IOStreams) error {
			return sb.Agent().Attach(ctx, io)
		})
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
	if err == nil || errors.Is(err, yoloai.ErrSandboxNotFound) {
		return err
	}
	return fmt.Errorf("%w\n  sandbox dir: %s\n  to remove: yoloai destroy %s", err, Layout().SandboxDir(name), name)
}
