// ABOUTME: buildAndStart and buildInstanceConfig translate sandboxState into a
// ABOUTME: runtime.InstanceConfig, start the container, and verify it is running.
package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
)

// secretsConsumedTimeout bounds how long buildAndStart waits for the
// in-sandbox entrypoint to signal it has read /run/secrets. Generous
// enough to cover a cold Kata VM boot + virtio-fs propagation; on
// timeout the caller removes the secrets dir anyway (we never leak it).
const secretsConsumedTimeout = 30 * time.Second

// buildAndStart constructs the runtime InstanceConfig from sandboxState and
// starts the instance. hasSecrets indicates whether secrets were injected via
// a temporary directory that the caller will remove after this call returns.
// Extracted from launchContainer().
func (m *Engine) buildAndStart(ctx context.Context, state *sandboxState, mounts []runtime.MountSpec, ports []runtime.PortMapping, hasSecrets bool) error {
	cname := store.InstanceName(state.name)
	instanceCfg, err := m.buildInstanceConfig(state, mounts, ports)
	if err != nil {
		return err
	}

	// Clear any stale marker from a prior boot so the wait below observes
	// only this launch's signal (the marker file lives in the persistent
	// sandbox dir and survives restarts).
	markerPath := filepath.Join(state.sandboxDir, store.SecretsConsumedMarker)
	if hasSecrets {
		_ = os.Remove(markerPath) //nolint:errcheck // best-effort; absent is fine
	}

	if err := m.runtime.Create(ctx, instanceCfg); err != nil {
		return err
	}

	if err := m.runtime.Start(ctx, cname); err != nil {
		return fmt.Errorf("start instance: %w", err)
	}

	// Wait for the entrypoint to signal it has read /run/secrets before the
	// caller removes the host-side secrets temp dir. A fixed sleep used to
	// guard this, but it raced on slow-booting backends (Kata VM via
	// containerd): the guest could still be booting when the dir was removed,
	// so it read an empty /run/secrets and the agent came up unauthenticated.
	// Slow-booting backends (Tart) declare a longer cap via the descriptor so
	// the host observes the marker before removing the dir, rather than timing
	// out mid-boot and relying on VirtioFS deletion lag to dodge the race.
	if hasSecrets {
		waitForSecretsConsumed(markerPath, effectiveSecretsConsumedTimeout(m.runtime.Descriptor()))
	}

	return m.verifyInstanceRunning(ctx, state, cname)
}

// effectiveSecretsConsumedTimeout is the host's cap on waiting for the
// secrets-consumed marker: the backend's declared value when set (slow-booting
// backends like Tart raise it so the host observes the marker before removing
// the secrets dir), otherwise the package default.
func effectiveSecretsConsumedTimeout(desc runtime.BackendDescriptor) time.Duration {
	if desc.SecretsConsumedTimeout > 0 {
		return desc.SecretsConsumedTimeout
	}
	return secretsConsumedTimeout
}

// waitForSecretsConsumed blocks until markerPath exists or timeout elapses.
// The in-sandbox entrypoint creates the marker after reading /run/secrets;
// once it's visible the host can safely remove the secrets temp dir. On
// timeout it returns without error — the caller removes the dir regardless
// so the ephemeral credentials never linger, accepting that a pathologically
// slow boot might still race (far rarer than the previous fixed-1s window).
func waitForSecretsConsumed(markerPath string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(markerPath); err == nil {
			return
		}
		if time.Now().After(deadline) {
			slog.Warn("secrets-consumed marker not observed before timeout; removing secrets dir anyway",
				"marker", markerPath, "timeout", timeout)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// buildInstanceConfig constructs the runtime.InstanceConfig from sandbox state.
func (m *Engine) buildInstanceConfig(state *sandboxState, mounts []runtime.MountSpec, ports []runtime.PortMapping) (runtime.InstanceConfig, error) {
	cname := store.InstanceName(state.name)
	desc := m.runtime.Descriptor()
	caps := desc.Capabilities

	if state.networkMode == "isolated" {
		if !caps.NetworkIsolation {
			return runtime.InstanceConfig{}, fmt.Errorf("--network=isolated is not supported by the %s backend", desc.Name)
		}
		// Per-isolation-mode check: some OCI runtimes (notably gVisor / runsc
		// for --isolation=container-enhanced) do not honor iptables rules
		// applied inside the sandbox, so the in-sandbox enforcement is a
		// silent no-op. Refuse rather than lie. See
		// docs/design/network-isolation.md for the redesign that removes this
		// limitation by moving enforcement to the host netns.
		if !runtime.IsolationEnforcesInSandboxIptables(state.isolation) {
			return runtime.InstanceConfig{}, fmt.Errorf(
				"--network=isolated cannot be enforced with --isolation=%s: "+
					"gVisor's userspace netstack ignores in-sandbox iptables rules. "+
					"Use --isolation=container (default) or a VM-based isolation mode "+
					"(--isolation=vm or --isolation=vm-enhanced) instead",
				state.isolation,
			)
		}
	}

	resolvedImage := state.imageRef
	if resolvedImage == "" {
		resolvedImage = "yoloai-base"
	}

	instanceCfg := runtime.InstanceConfig{
		Name:        cname,
		ImageRef:    resolvedImage,
		WorkingDir:  overlayOrResolvedMountPath(state.workdir),
		Mounts:      mounts,
		Ports:       ports,
		NetworkMode: state.networkMode,
		UseInit:     true,
		// C.UTF-8 is always present without locale-gen; without it apps like Claude Code render ASCII-only.
		ContainerEnv: []string{"LANG=C.UTF-8"},
	}

	if err := applyResourceLimits(state, &instanceCfg); err != nil {
		return runtime.InstanceConfig{}, err
	}

	if state.networkMode == "isolated" && caps.NetworkIsolation {
		instanceCfg.CapAdd = append(instanceCfg.CapAdd, "NET_ADMIN")
	}

	if err := applyOverlayAndCaps(state, caps, &instanceCfg, desc.Name); err != nil {
		return runtime.InstanceConfig{}, err
	}

	if state.isolation == "container-privileged" {
		instanceCfg.Privileged = true
	}

	// Set the runtime identifier for both Docker (OCI --runtime name) and containerd (shimv2 type).
	// IsolationContainerRuntime returns "" for container isolation where the default suffices.
	instanceCfg.ContainerRuntime = runtime.IsolationContainerRuntime(state.isolation)
	instanceCfg.Snapshotter = runtime.IsolationSnapshotter(state.isolation)

	return instanceCfg, nil
}

// applyResourceLimits converts and applies resource limits to the instance config.
func applyResourceLimits(state *sandboxState, instanceCfg *runtime.InstanceConfig) error {
	if state.resources == nil {
		return nil
	}
	rtResources, err := parseResourceLimits(state.resources)
	if err != nil {
		return err
	}
	instanceCfg.Resources = rtResources
	return nil
}

// applyOverlayAndCaps validates and applies overlay/capability requirements to the instance config.
func applyOverlayAndCaps(state *sandboxState, caps runtime.BackendCaps, instanceCfg *runtime.InstanceConfig, runtimeName runtime.BackendName) error {
	// Catch isolation-mode/overlay conflicts early before Docker fails with
	// an opaque error. runtime.SupportsOverlayDirs encodes the policy
	// (container-enhanced / gVisor is the rejection case); the message stays
	// here because it's CLI-shaped advice.
	if hasOverlayDirs(state) && !runtime.SupportsOverlayDirs(state.isolation) {
		return fmt.Errorf(
			":overlay directories require --isolation container; " +
				"--isolation container-enhanced uses gVisor, which does not support overlayfs inside the container")
	}

	// CAP_SYS_ADMIN required for overlay mounts inside the container
	if hasOverlayDirs(state) {
		if !caps.OverlayDirs {
			return fmt.Errorf(":overlay mode requires a container backend that supports overlayfs (not supported with %s)", runtimeName)
		}
		instanceCfg.CapAdd = append(instanceCfg.CapAdd, "SYS_ADMIN")
	}

	// Recipe fields (cap_add, devices, setup) require a backend with CapAdd support
	if !caps.CapAdd && (len(state.capAdd) > 0 || len(state.devices) > 0 || len(state.setup) > 0) {
		return fmt.Errorf("cap_add, devices, and setup require a container backend (not supported with %s)", runtimeName)
	}
	instanceCfg.CapAdd = append(instanceCfg.CapAdd, state.capAdd...)
	instanceCfg.Devices = state.devices

	return nil
}

// verifyInstanceRunning checks that the instance is still running after start,
// collecting log output for diagnostics if it has exited.
func (m *Engine) verifyInstanceRunning(ctx context.Context, state *sandboxState, cname string) error {
	// Verify instance is still running (catches immediate crashes).
	time.Sleep(1 * time.Second)
	info, err := m.runtime.Inspect(ctx, cname)
	if err != nil {
		return fmt.Errorf("inspect instance after start: %w", err)
	}
	if info.Running {
		return nil
	}

	var parts []string
	// Try sandbox.jsonl first — written by entrypoint.sh and entrypoint.py.
	if tail := readLogTail(filepath.Join(state.sandboxDir, "logs", "sandbox.jsonl"), 20); tail != "" {
		parts = append(parts, tail)
	} else if tail := readLogTail(filepath.Join(state.sandboxDir, store.AgentLogFile), 20); tail != "" {
		// Try agent log file (written after tmux setup).
		parts = append(parts, tail)
	}
	// Always append container logs — captures stderr output such as Python
	// tracebacks that are not written to sandbox.jsonl.
	if logs := runtime.LogsFor(ctx, m.runtime, cname, 20); logs != "" {
		parts = append(parts, logs)
	}
	if len(parts) > 0 {
		return fmt.Errorf("instance exited immediately:\n%s", strings.Join(parts, "\n"))
	}
	return fmt.Errorf("instance exited immediately — %s", m.runtime.DiagHint(cname))
}
