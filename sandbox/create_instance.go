// ABOUTME: buildAndStart and buildInstanceConfig translate sandboxState into a
// ABOUTME: runtime.InstanceConfig, start the container, and verify it is running.
package sandbox

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/sandbox/store"
)

// buildAndStart constructs the runtime InstanceConfig from sandboxState and
// starts the instance. hasSecrets indicates whether secrets were injected via
// a temporary directory that the caller will remove after this call returns.
// Extracted from launchContainer().
func (m *Manager) buildAndStart(ctx context.Context, state *sandboxState, mounts []runtime.MountSpec, ports []runtime.PortMapping, hasSecrets bool) error {
	cname := store.InstanceName(state.name)
	instanceCfg, err := m.buildInstanceConfig(state, mounts, ports)
	if err != nil {
		return err
	}

	if err := m.runtime.Create(ctx, instanceCfg); err != nil {
		return err
	}

	if err := m.runtime.Start(ctx, cname); err != nil {
		return fmt.Errorf("start instance: %w", err)
	}

	// Wait briefly for entrypoint to read secrets before the caller removes them.
	if hasSecrets {
		time.Sleep(1 * time.Second)
	}

	return m.verifyInstanceRunning(ctx, state, cname)
}

// buildInstanceConfig constructs the runtime.InstanceConfig from sandbox state.
func (m *Manager) buildInstanceConfig(state *sandboxState, mounts []runtime.MountSpec, ports []runtime.PortMapping) (runtime.InstanceConfig, error) {
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
func applyOverlayAndCaps(state *sandboxState, caps runtime.BackendCaps, instanceCfg *runtime.InstanceConfig, runtimeName string) error {
	// container-enhanced (gVisor) does not support overlayfs inside the container.
	// Catch this combination early before Docker fails with an opaque error.
	if state.isolation == "container-enhanced" && hasOverlayDirs(state) {
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
func (m *Manager) verifyInstanceRunning(ctx context.Context, state *sandboxState, cname string) error {
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
	if logs := m.runtime.Logs(ctx, cname, 20); logs != "" {
		parts = append(parts, logs)
	}
	if len(parts) > 0 {
		return fmt.Errorf("instance exited immediately:\n%s", strings.Join(parts, "\n"))
	}
	return fmt.Errorf("instance exited immediately — %s", m.runtime.DiagHint(cname))
}
