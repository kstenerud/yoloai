package sandbox

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/kstenerud/yoloai/runtime"
)

// buildAndStart constructs the runtime InstanceConfig from sandboxState and
// starts the instance. hasSecrets indicates whether secrets were injected via
// a temporary directory that the caller will remove after this call returns.
// Extracted from launchContainer().
func (m *Manager) buildAndStart(ctx context.Context, state *sandboxState, mounts []runtime.MountSpec, ports []runtime.PortMapping, hasSecrets bool) error {
	cname := InstanceName(state.name)

	caps := m.runtime.Capabilities()

	if state.networkMode == "isolated" && !caps.NetworkIsolation {
		return fmt.Errorf("--network=isolated is not supported by the %s backend", m.runtime.Name())
	}

	resolvedImage := state.imageRef
	if resolvedImage == "" {
		resolvedImage = "yoloai-base"
	}

	instanceCfg := runtime.InstanceConfig{
		Name:        cname,
		ImageRef:    resolvedImage,
		WorkingDir:  state.workdir.ResolvedMountPath(),
		Mounts:      mounts,
		Ports:       ports,
		NetworkMode: state.networkMode,
		UseInit:     true,
	}

	// Convert resource limits
	if state.resources != nil {
		rtResources, err := parseResourceLimits(state.resources)
		if err != nil {
			return err
		}
		instanceCfg.Resources = rtResources
	}

	if state.networkMode == "isolated" && caps.NetworkIsolation {
		instanceCfg.CapAdd = append(instanceCfg.CapAdd, "NET_ADMIN")
	}

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
			return fmt.Errorf(":overlay mode requires a container backend that supports overlayfs (not supported with %s)", m.runtime.Name())
		}
		instanceCfg.CapAdd = append(instanceCfg.CapAdd, "SYS_ADMIN")
	}

	// Recipe fields (cap_add, devices, setup) require a backend with CapAdd support
	if !caps.CapAdd && (len(state.capAdd) > 0 || len(state.devices) > 0 || len(state.setup) > 0) {
		return fmt.Errorf("cap_add, devices, and setup require a container backend (not supported with %s)", m.runtime.Name())
	}
	instanceCfg.CapAdd = append(instanceCfg.CapAdd, state.capAdd...)
	instanceCfg.Devices = state.devices

	// Set the runtime identifier for both Docker (OCI --runtime name) and containerd (shimv2 type).
	// IsolationContainerRuntime returns "" for container isolation where the default suffices.
	instanceCfg.ContainerRuntime = runtime.IsolationContainerRuntime(state.isolation)
	instanceCfg.Snapshotter = runtime.IsolationSnapshotter(state.isolation)

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

	// Verify instance is still running (catches immediate crashes).
	time.Sleep(1 * time.Second)
	info, err := m.runtime.Inspect(ctx, cname)
	if err != nil {
		return fmt.Errorf("inspect instance after start: %w", err)
	}
	if !info.Running {
		// Try sandbox.jsonl first — written by entrypoint.sh and entrypoint.py.
		if tail := readLogTail(filepath.Join(state.sandboxDir, "logs", "sandbox.jsonl"), 20); tail != "" {
			return fmt.Errorf("instance exited immediately:\n%s", tail)
		}
		// Try agent log file (written after tmux setup).
		if tail := readLogTail(filepath.Join(state.sandboxDir, AgentLogFile), 20); tail != "" {
			return fmt.Errorf("instance exited immediately:\n%s", tail)
		}
		// Fall back to container logs (captures pre-entrypoint crashes, e.g. gVisor startup errors).
		if logs := m.runtime.Logs(ctx, cname, 50); logs != "" {
			return fmt.Errorf("instance exited immediately:\n%s", logs)
		}
		return fmt.Errorf("instance exited immediately — %s", m.runtime.DiagHint(cname))
	}

	return nil
}
