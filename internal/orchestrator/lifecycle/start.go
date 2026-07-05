// ABOUTME: Start verb and its supporting helpers — idempotent sandbox start,
// ABOUTME: isolation/vscode-tunnel option application, prompt preparation,
// ABOUTME: and per-status branch handlers (terminal, stopped, suspended).
package lifecycle

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/envsetup"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/orchestrator/envspec"
	"github.com/kstenerud/yoloai/internal/orchestrator/invocation"
	"github.com/kstenerud/yoloai/internal/orchestrator/launch"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/internal/orchestrator/status"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

const resumePreamble = "You were previously working on the following task and were interrupted. " +
	"The work directory contains your progress so far. Continue where you left off:\n\n"

// StartOptions holds parameters for the start command.
type StartOptions struct {
	Resume       bool                  // re-feed original prompt with continuation preamble
	Prompt       string                // if set, overwrite prompt.txt and send directly (no preamble)
	PromptFile   string                // if set, read from file, overwrite prompt.txt, send directly
	Isolation    runtime.IsolationMode // if set, override the isolation mode stored in environment.json
	VscodeTunnel bool                  // if true, enable VS Code Remote Tunnel (persisted to meta)
	// Broker forces credential brokering on (--broker): the agent's API key is
	// injected host-side and never enters the sandbox (D105/D106). It is an error
	// if the backend can't host an injector — the user explicitly asked for the
	// key to be withheld. Brokering is already the default for supported backends,
	// so this is only needed to turn an error-on-unsupported into a hard
	// requirement. NoBroker forces it off (--no-broker): the key is delivered
	// directly, suppressing the default. At most one may be set. Either posture is
	// persisted to meta and sticky across restart/start (applyBrokerOption).
	Broker   bool
	NoBroker bool
	// Env is the per-sandbox environment overlay, merged over the resolved
	// config+profile env at container (re)creation. It is never persisted —
	// the caller must re-supply it on each launch that needs it (secrets are
	// the caller's concern).
	Env map[string]string
	// Recreating indicates the caller already intends a recreate (e.g. reset
	// deliberately destroyed the container), so the provider-switch advisory
	// (DF22) — which assumes the container vanished unexpectedly — is suppressed.
	Recreating bool
}

// Start ensures a sandbox is running — idempotent.
func Start(ctx context.Context, d state.Deps, name string, opts StartOptions) (*StartResult, error) {
	unlock, err := store.AcquireLock(d.Layout, name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	var n notices
	startErr := start(ctx, d, name, opts, &n)
	return &StartResult{Notices: n.list}, startErr
}

// applyIsolationOverride applies the isolation mode override from opts to meta
// if it differs from the current value. Validates mode, checks backend support,
// and saves meta. No-op when opts.Isolation is empty or unchanged.
func applyIsolationOverride(ctx context.Context, d state.Deps, opts StartOptions, sandboxDir string, meta *store.Environment, n *notices) error {
	if opts.Isolation == "" || opts.Isolation == meta.Isolation {
		return nil
	}
	if err := config.ValidateIsolationMode(string(opts.Isolation)); err != nil {
		return err
	}
	desc := d.Runtime.Descriptor()
	supported := desc.SupportedIsolationModes
	if opts.Isolation != runtime.IsolationModeContainer && len(supported) > 0 {
		ok := slices.Contains(supported, opts.Isolation)
		if !ok {
			return yoerrors.NewUsageError("isolation mode %q is not supported by the %s backend", opts.Isolation, desc.Type)
		}
	}
	if err := launch.CheckIsolationPrerequisites(ctx, d.Runtime, opts.Isolation); err != nil {
		return err
	}
	meta.Isolation = opts.Isolation
	if err := store.SaveEnvironment(sandboxDir, meta); err != nil {
		return fmt.Errorf("save meta: %w", err)
	}
	n.infof("Isolation mode updated to %s", opts.Isolation)
	return nil
}

// applyBrokerOption persists the credential-brokering posture to meta when
// --broker is requested and not already enabled (D106). It is sticky: once a
// sandbox is brokered, restart/start re-broker from meta so the real key is never
// silently re-delivered into the container on a later launch. Unlike the vscode
// option there is no runtime-config patch — brokering lives entirely in the
// host-side launch path, not the entrypoint. (Opting back out is the future
// --no-broker; not wired here.)
func applyBrokerOption(d state.Deps, opts StartOptions, sandboxDir string, meta *store.Environment, n *notices) error {
	// Resolve the explicit posture: --broker forces on, --no-broker forces off,
	// neither leaves the persisted posture untouched (sticky). The two flags are
	// mutually exclusive (validated at the CLI). The posture is persisted so a
	// later restart/start re-applies the same decision rather than silently
	// reverting to the default (D106).
	var on, off bool
	switch {
	case opts.Broker:
		on = true
	case opts.NoBroker:
		off = true
	default:
		return nil // auto: don't disturb any persisted choice
	}
	if meta.BrokerCredentials == on && meta.BrokerDisabled == off {
		return nil // already in the requested posture
	}
	meta.BrokerCredentials = on
	meta.BrokerDisabled = off
	if err := store.SaveEnvironment(sandboxDir, meta); err != nil {
		return fmt.Errorf("save meta: %w", err)
	}
	if on {
		n.infof("Credential brokering enabled (the agent's API key stays host-side)")
	} else {
		n.infof("Credential brokering disabled (the agent's API key is delivered directly)")
	}
	return nil
}

// applyVscodeTunnelOption enables the VS Code Remote Tunnel in meta and
// runtime-config.json when opts.VscodeTunnel is true and not already enabled.
func applyVscodeTunnelOption(d state.Deps, opts StartOptions, sandboxDir, name string, meta *store.Environment, n *notices) error {
	if !opts.VscodeTunnel || meta.VscodeTunnel {
		return nil
	}
	meta.VscodeTunnel = true
	if err := store.SaveEnvironment(sandboxDir, meta); err != nil {
		return fmt.Errorf("save meta: %w", err)
	}
	if err := patchConfigVscodeTunnel(sandboxDir, name); err != nil {
		return fmt.Errorf("patch runtime-config.json for vscode-tunnel: %w", err)
	}
	n.infof("VS Code tunnel enabled")
	return nil
}

// preparePromptForStart validates prompt options, reads the custom prompt text
// if provided, and persists it to prompt.txt + meta. Returns the prompt text
// and whether a custom prompt is in use.
// homeDir is used to expand leading "~" in the promptFile path.
func preparePromptForStart(opts StartOptions, sandboxDir string, meta *store.Environment, homeDir string, env map[string]string, stdin io.Reader) (promptText string, customPrompt bool, err error) {
	customPrompt = opts.Prompt != "" || opts.PromptFile != ""
	if opts.Resume && customPrompt {
		return "", false, fmt.Errorf("--resume and --prompt/--prompt-file are mutually exclusive")
	}
	if opts.Resume && !meta.HasPrompt {
		return "", false, fmt.Errorf("--resume requires a sandbox created with --prompt")
	}
	if !customPrompt {
		return "", false, nil
	}

	promptText, err = invocation.ReadPrompt(opts.Prompt, opts.PromptFile, homeDir, env, stdin)
	if err != nil {
		return "", false, err
	}
	if promptText == "" {
		return "", false, fmt.Errorf("--prompt/--prompt-file produced empty text")
	}

	// Overwrite prompt.txt with new prompt; save old content for rollback.
	promptPath := filepath.Join(sandboxDir, "prompt.txt")
	oldPrompt, _ := os.ReadFile(promptPath) //nolint:gosec // G304: promptPath is constructed from a validated sandbox name
	if writeErr := fileutil.WriteFile(promptPath, []byte(promptText), 0600); writeErr != nil {
		return "", false, fmt.Errorf("write prompt.txt: %w", writeErr)
	}
	meta.HasPrompt = true
	if saveErr := store.SaveEnvironment(sandboxDir, meta); saveErr != nil {
		// Roll back prompt.txt so disk state remains consistent with environment.json.
		if oldPrompt != nil {
			_ = fileutil.WriteFile(promptPath, oldPrompt, 0600)
		} else {
			_ = os.Remove(promptPath)
		}
		return "", false, fmt.Errorf("save meta: %w", saveErr)
	}
	return promptText, true, nil
}

// handleTerminalStatus relaunches the agent after it has exited (Done/Failed).
func handleTerminalStatus(ctx context.Context, d state.Deps, name string, meta *store.Environment, opts StartOptions, promptText string, customPrompt bool, n *notices) error {
	slog.Info("relaunching agent", "event", "sandbox.start.agent.relaunch", "sandbox", name)
	switch {
	case customPrompt:
		if err := relaunchAgentWithCustomPrompt(ctx, d, name, meta, promptText); err != nil {
			return err
		}
	case opts.Resume:
		if err := relaunchAgentWithResume(ctx, d, name, meta); err != nil {
			return err
		}
	default:
		if err := relaunchAgent(ctx, d, name, meta); err != nil {
			return err
		}
	}
	n.infof("Agent relaunched in sandbox %s", name)
	return nil
}

// handleStoppedOrRemovedStatus recreates the container for a sandbox whose
// container is stopped or removed. removeStopped indicates the container still
// exists and must be removed first. successMsg is printed on success.
func handleStoppedOrRemovedStatus(ctx context.Context, d state.Deps, cname, name string, meta *store.Environment, opts StartOptions, promptText string, customPrompt, removeStopped bool, successMsg string, n *notices) error {
	if removeStopped && !d.Runtime.Descriptor().Capabilities.HostFilesystem {
		// Container backends (Docker, Podman, containerd): the sandbox directory
		// lives on the host separately from the container, so Remove only deletes
		// the stopped container and the sandbox directory is preserved.
		//
		// Host-filesystem backends (Seatbelt): the sandbox directory IS the
		// container state. Remove would destroy the work copy, prompt.txt, and
		// other sandbox files. Skip Remove — the process is already dead after
		// Stop, and Create+Start will refresh scripts and credentials in place.
		if err := d.Runtime.Remove(ctx, cname); err != nil {
			return fmt.Errorf("remove stopped instance: %w", err)
		}
	}
	slog.Info("recreating container", "event", "sandbox.start.container.recreate", "sandbox", name)
	switch {
	case customPrompt:
		if err := prepareRelaunchFiles(d, name, meta, promptText); err != nil {
			return err
		}
		defer cleanupResumeFiles(d, name)
	case opts.Resume:
		if err := prepareResumeFiles(d, name, meta); err != nil {
			return err
		}
		defer cleanupResumeFiles(d, name)
	}
	if err := recreateContainer(ctx, d, name, meta, opts.Resume, opts.Env, n); err != nil {
		return err
	}
	n.infof("%s", successMsg)
	return nil
}

// handleSuspendedResume resumes a suspended VM and starts a fresh agent session.
// Credentials are refreshed, the VM is resumed via runtime.Start (which kills
// the stale tmux session and runs the setup script), and executeVMWorkDirSetup
// is skipped because the work directory is already present from the suspend.
func handleSuspendedResume(ctx context.Context, d state.Deps, cname, name string, meta *store.Environment, opts StartOptions, promptText string, customPrompt bool, n *notices) error {
	slog.Info("resuming suspended sandbox", "event", "sandbox.start.resume", "sandbox", name)
	sandboxDir := d.Layout.SandboxDir(name)

	agentDef, _, err := requireAgent(d, name)
	if err != nil {
		return err
	}

	// Refresh credentials and settings from host (handles token refresh between
	// sessions), re-apply container settings, and re-inject folder trust for every
	// mount path — a bare CopySeedFiles here would clobber the trust pre-accept.
	spec := envspec.BuildEnvSpec(agentDef)
	hasAPIKey := envsetup.HasAnyAPIKey(spec, d.Layout)
	if _, err := envsetup.RefreshHomeSeed(spec, sandboxDir, hasAPIKey, d.Layout.HomeDir, d.Layout, meta.MountPaths()); err != nil {
		return fmt.Errorf("refresh seed files: %w", err)
	}

	switch {
	case customPrompt:
		if err := prepareRelaunchFiles(d, name, meta, promptText); err != nil {
			return err
		}
		defer cleanupResumeFiles(d, name)
	case opts.Resume:
		if err := prepareResumeFiles(d, name, meta); err != nil {
			return err
		}
		defer cleanupResumeFiles(d, name)
	}

	// Resume the VM: tart run resumes from suspended state, kills the stale
	// tmux session, and runs the setup script for a fresh agent.
	if err := d.Runtime.Start(ctx, cname); err != nil {
		// Apple VZ framework cannot restore VMs that had VirtioFS (--dir) mounts
		// from a suspend snapshot (VZErrorDomain Code=12). Fall back to destroying
		// the suspended VM and recreating from the host staging area.
		slog.Warn("suspended VM resume failed, falling back to recreate", "sandbox", name, "err", err)
		_ = d.Runtime.Remove(ctx, cname)
		return handleStoppedOrRemovedStatus(ctx, d, cname, name, meta, opts, promptText, customPrompt, false, fmt.Sprintf("Sandbox %s recreated and started", name), n)
	}

	// Don't call executeVMWorkDirSetup: the work directory is already present
	// inside the VM from before the suspend.

	n.infof("Sandbox %s resumed", name)
	return nil
}

// maybeWarnRecreateAdvisory emits the backend's recreate advisory (DF22) when a
// sandbox's container was found missing and the recreate is not deliberate
// (reset sets opts.Recreating). It warns, e.g., that a container missing from
// the current Docker provider may still live in one the user switched away from,
// so recreating here abandons the original. No-op for backends without an
// advisory or when the recreate was expected.
func maybeWarnRecreateAdvisory(ctx context.Context, d state.Deps, opts StartOptions, n *notices) {
	if opts.Recreating {
		return
	}
	if adv := runtime.RecreateAdvisoryFor(ctx, d.Runtime); adv != "" {
		n.warnf("%s", adv)
	}
}

func start(ctx context.Context, d state.Deps, name string, opts StartOptions, n *notices) error {
	slog.Info("starting sandbox", "event", "sandbox.start", "sandbox", name)
	sandboxDir := d.Layout.SandboxDir(name)
	if err := store.RequireSandboxDir(sandboxDir); err != nil {
		return err
	}

	meta, err := store.LoadEnvironment(sandboxDir)
	if err != nil {
		return err
	}

	// Sync lifecycle on-create-done marker to sandbox state.
	// Python writes a marker file after successful on-create commands; Go
	// reads it on next start and persists the flag to sandbox-state.json so
	// subsequent runtime-config.json writes can set on_create_done: true.
	syncLifecycleMarker(sandboxDir)

	// Apply isolation override before recreating the container.
	if err := applyIsolationOverride(ctx, d, opts, sandboxDir, meta, n); err != nil {
		return err
	}

	// Persist the credential-brokering posture if requested (sticky across launches).
	if err := applyBrokerOption(d, opts, sandboxDir, meta, n); err != nil {
		return err
	}

	// Enable VS Code Remote Tunnel if requested and not already enabled.
	if err := applyVscodeTunnelOption(d, opts, sandboxDir, name, meta, n); err != nil {
		return err
	}

	cname := store.InstanceName(d.Layout.Principal, name)
	st, err := status.DetectStatus(ctx, d.Runtime, cname, sandboxDir)
	if err != nil {
		return fmt.Errorf("detect status: %w", err)
	}
	slog.Debug("container status", "event", "sandbox.start.status", "sandbox", name, "status", string(st))

	promptText, customPrompt, err := preparePromptForStart(opts, sandboxDir, meta, d.Layout.HomeDir, d.Layout.Env().EnvForConfigInterpolation(), d.Input)
	if err != nil {
		return err
	}

	switch st {
	case status.StatusActive, status.StatusIdle:
		n.infof("Sandbox %s is already running", name)
		return nil

	case status.StatusDone, status.StatusFailed:
		return handleTerminalStatus(ctx, d, name, meta, opts, promptText, customPrompt, n)

	case status.StatusSuspended:
		return handleSuspendedResume(ctx, d, cname, name, meta, opts, promptText, customPrompt, n)

	case status.StatusStopped:
		return handleStoppedOrRemovedStatus(ctx, d, cname, name, meta, opts, promptText, customPrompt, true, fmt.Sprintf("Sandbox %s started", name), n)

	case status.StatusRemoved:
		maybeWarnRecreateAdvisory(ctx, d, opts, n)
		return handleStoppedOrRemovedStatus(ctx, d, cname, name, meta, opts, promptText, customPrompt, false, fmt.Sprintf("Sandbox %s recreated and started", name), n)

	default:
		return fmt.Errorf("unexpected sandbox status: %s", st)
	}
}
