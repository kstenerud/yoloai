// ABOUTME: translating resolved State into a runtime.InstanceConfig, starting
// ABOUTME: the container, and verifying it is running.
package launch

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/runtime"
	mountspkg "github.com/kstenerud/yoloai/internal/sandbox/mounts"
	"github.com/kstenerud/yoloai/internal/sandbox/provision"
	"github.com/kstenerud/yoloai/internal/sandbox/state"
	"github.com/kstenerud/yoloai/internal/sandbox/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// secretsConsumedTimeout bounds how long buildAndStart waits for the
// in-sandbox entrypoint to signal it has read /run/secrets. Generous
// enough to cover a cold Kata VM boot + virtio-fs propagation; on
// timeout the caller removes the secrets dir anyway (we never leak it).
const secretsConsumedTimeout = 30 * time.Second

// LaunchContainer creates a sandbox instance from State, starts it,
// and cleans up credential temp files. Used by both initial creation and
// recreation from environment.json.
func LaunchContainer(ctx context.Context, d state.Deps, st *state.State) error {
	slog.Info("launching container", "event", "sandbox.create.container.launch", "sandbox", st.Name, "image", st.ImageRef)
	// Use pre-merged env from state if available, otherwise load from config.
	envVars := st.Env
	if envVars == nil {
		cfg, cfgErr := config.LoadConfig(d.Layout)
		if cfgErr != nil {
			return fmt.Errorf("load config: %w", cfgErr)
		}
		envVars = cfg.Env
	}

	secretsDir, err := provision.CreateSecretsDir(st.Agent, envVars, st.Layout.Env, st.Isolation, st.Layout.SecretsStagingDir)
	if err != nil {
		return fmt.Errorf("create secrets: %w", err)
	}
	if secretsDir != "" {
		defer os.RemoveAll(secretsDir) //nolint:errcheck // best-effort cleanup
	}

	mnts := mountspkg.Build(st, secretsDir)

	ports, err := parsePortBindings(st.Ports)
	if err != nil {
		return err
	}
	ports = filterAvailablePorts(ports, outputOr(st.Output))

	return buildAndStart(ctx, d.Runtime, st, mnts, ports, secretsDir != "")
}

// buildAndStart constructs the runtime InstanceConfig from State and
// starts the instance. hasSecrets indicates whether secrets were injected via
// a temporary directory that the caller will remove after this call returns.
// Extracted from launchContainer().
func buildAndStart(ctx context.Context, rt runtime.Runtime, st *state.State, mnts []runtime.MountSpec, ports []runtime.PortMapping, hasSecrets bool) error {
	cname := store.InstanceName(st.Layout.Principal, st.Name)
	instanceCfg, err := buildInstanceConfig(rt.Descriptor(), st, mnts, ports)
	if err != nil {
		return err
	}

	// Clear any stale marker from a prior boot so the wait below observes
	// only this launch's signal (the marker file lives in the persistent
	// sandbox dir and survives restarts).
	markerPath := filepath.Join(st.SandboxDir, store.SecretsConsumedMarker)
	if hasSecrets {
		_ = os.Remove(markerPath) //nolint:errcheck // best-effort; absent is fine
	}

	if err := rt.Create(ctx, instanceCfg); err != nil {
		return err
	}

	if err := rt.Start(ctx, cname); err != nil {
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
		waitForSecretsConsumed(markerPath, effectiveSecretsConsumedTimeout(rt.Descriptor()))
	}

	return verifyInstanceRunning(ctx, rt, st, cname)
}

// buildInstanceConfig constructs the runtime.InstanceConfig from sandbox state.
func buildInstanceConfig(desc runtime.BackendDescriptor, st *state.State, mnts []runtime.MountSpec, ports []runtime.PortMapping) (runtime.InstanceConfig, error) {
	cname := store.InstanceName(st.Layout.Principal, st.Name)
	caps := desc.Capabilities

	if st.NetworkMode == "isolated" {
		if !caps.NetworkIsolation {
			return runtime.InstanceConfig{}, fmt.Errorf("--network=isolated is not supported by the %s backend", desc.Type)
		}
		// Per-isolation-mode check: some OCI runtimes (notably gVisor / runsc
		// for --isolation=container-enhanced) do not honor iptables rules
		// applied inside the sandbox, so the in-sandbox enforcement is a
		// silent no-op. Refuse rather than lie. See
		// docs/contributors/design/network-isolation.md for the redesign that removes this
		// limitation by moving enforcement to the host netns.
		if !runtime.IsolationEnforcesInSandboxIptables(st.Isolation) {
			return runtime.InstanceConfig{}, fmt.Errorf(
				"--network=isolated cannot be enforced with --isolation=%s: "+
					"gVisor's userspace netstack ignores in-sandbox iptables rules. "+
					"Use --isolation=container (default) or a VM-based isolation mode "+
					"(--isolation=vm or --isolation=vm-enhanced) instead",
				st.Isolation,
			)
		}
	}

	resolvedImage := st.ImageRef
	if resolvedImage == "" {
		resolvedImage = "yoloai-base"
	}

	instanceCfg := runtime.InstanceConfig{
		Name:        cname,
		ImageRef:    resolvedImage,
		WorkingDir:  OverlayOrResolvedMountPath(st.Workdir),
		Mounts:      mnts,
		Ports:       ports,
		NetworkMode: st.NetworkMode,
		UseInit:     true,
		Labels:      instanceLabels(st.Layout.Principal, st.Name),
		// C.UTF-8 is always present without locale-gen; without it apps like Claude Code render ASCII-only.
		ContainerEnv: []string{"LANG=C.UTF-8"},
	}

	if err := applyResourceLimits(st, &instanceCfg); err != nil {
		return runtime.InstanceConfig{}, err
	}

	if st.NetworkMode == "isolated" && caps.NetworkIsolation {
		instanceCfg.CapAdd = append(instanceCfg.CapAdd, "NET_ADMIN")
	}

	if err := applyOverlayAndCaps(st, caps, &instanceCfg, desc.Type); err != nil {
		return runtime.InstanceConfig{}, err
	}

	if st.Isolation == "container-privileged" {
		instanceCfg.Privileged = true
	}

	// Set the runtime identifier for both Docker (OCI --runtime name) and containerd (shimv2 type).
	// IsolationContainerRuntime returns "" for container isolation where the default suffices.
	instanceCfg.ContainerRuntime = runtime.IsolationContainerRuntime(st.Isolation)
	instanceCfg.Snapshotter = runtime.IsolationSnapshotter(st.Isolation)

	return instanceCfg, nil
}

// instanceLabels builds the runtime instance labels recording sandbox identity
// and (when non-default) the owning principal. The sandbox label is always set;
// the principal label is omitted for the default ("") principal so single-
// principal instances carry no principal metadata (D62).
func instanceLabels(principal config.PrincipalSegment, name string) map[string]string {
	labels := map[string]string{runtime.LabelSandbox: name}
	if principal != "" {
		labels[runtime.LabelPrincipal] = string(principal)
	}
	return labels
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

// applyResourceLimits converts and applies resource limits to the instance config.
func applyResourceLimits(st *state.State, instanceCfg *runtime.InstanceConfig) error {
	if st.Resources == nil {
		return nil
	}
	rtResources, err := parseResourceLimits(st.Resources)
	if err != nil {
		return err
	}
	instanceCfg.Resources = rtResources
	return nil
}

// applyOverlayAndCaps validates and applies overlay/capability requirements to the instance config.
func applyOverlayAndCaps(st *state.State, caps runtime.BackendCaps, instanceCfg *runtime.InstanceConfig, runtimeName runtime.BackendType) error {
	// Catch isolation-mode/overlay conflicts early before Docker fails with
	// an opaque error. runtime.SupportsOverlayDirs encodes the policy
	// (container-enhanced / gVisor is the rejection case); the message stays
	// here because it's CLI-shaped advice.
	if mountspkg.HasOverlayDirs(st) && !runtime.SupportsOverlayDirs(st.Isolation) {
		return fmt.Errorf(
			":overlay directories require --isolation container; " +
				"--isolation container-enhanced uses gVisor, which does not support overlayfs inside the container")
	}

	// CAP_SYS_ADMIN required for overlay mounts inside the container
	if mountspkg.HasOverlayDirs(st) {
		if !caps.OverlayDirs {
			return fmt.Errorf(":overlay mode requires a container backend that supports overlayfs (not supported with %s)", runtimeName)
		}
		instanceCfg.CapAdd = append(instanceCfg.CapAdd, "SYS_ADMIN")
	}

	// Recipe fields (cap_add, devices, setup) require a backend with CapAdd support
	if !caps.CapAdd && (len(st.CapAdd) > 0 || len(st.Devices) > 0 || len(st.Setup) > 0) {
		return fmt.Errorf("cap_add, devices, and setup require a container backend (not supported with %s)", runtimeName)
	}
	instanceCfg.CapAdd = append(instanceCfg.CapAdd, st.CapAdd...)
	instanceCfg.Devices = st.Devices

	return nil
}

// verifyInstanceRunning checks that the instance is still running after start,
// collecting log output for diagnostics if it has exited.
func verifyInstanceRunning(ctx context.Context, rt runtime.Runtime, st *state.State, cname string) error {
	// Verify instance is still running (catches immediate crashes).
	time.Sleep(1 * time.Second)
	info, err := rt.Inspect(ctx, cname)
	if err != nil {
		return fmt.Errorf("inspect instance after start: %w", err)
	}
	if info.Running {
		return nil
	}

	var parts []string
	// Try sandbox.jsonl first — written by entrypoint.sh and entrypoint.py.
	if tail := readLogTail(filepath.Join(st.SandboxDir, "logs", "sandbox.jsonl"), 20); tail != "" {
		parts = append(parts, tail)
	} else if tail := readLogTail(filepath.Join(st.SandboxDir, store.AgentLogFile), 20); tail != "" {
		// Try agent log file (written after tmux setup).
		parts = append(parts, tail)
	}
	// Always append container logs — captures stderr output such as Python
	// tracebacks that are not written to sandbox.jsonl.
	if logs := runtime.LogsFor(ctx, rt, cname, 20); logs != "" {
		parts = append(parts, logs)
	}
	if len(parts) > 0 {
		return fmt.Errorf("instance exited immediately:\n%s", strings.Join(parts, "\n"))
	}
	return fmt.Errorf("instance exited immediately — %s", rt.DiagHint(cname))
}

// filterAvailablePorts removes any port mappings where the host port is already
// in use, printing a warning for each skipped entry. Best-effort: a TOCTOU race
// is possible but Docker's own error is the fallback for that case.
func filterAvailablePorts(ports []runtime.PortMapping, output io.Writer) []runtime.PortMapping {
	var available []runtime.PortMapping
	for _, p := range ports {
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", p.HostPort))
		if err != nil {
			fmt.Fprintf(output, "Warning: skipping port %d:%d — host port %d is already in use\n", //nolint:errcheck // best-effort output
				p.HostPort, p.ContainerPort, p.HostPort)
			continue
		}
		_ = l.Close()
		available = append(available, p)
	}
	return available
}

// parsePortBindings converts ["host:container", ...] to runtime port mappings.
func parsePortBindings(ports []string) ([]runtime.PortMapping, error) {
	if len(ports) == 0 {
		return nil, nil
	}

	var result []runtime.PortMapping
	for _, p := range ports {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) != 2 {
			return nil, yoerrors.NewUsageError("invalid port format %q (expected host:container)", p)
		}
		hostPort, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, yoerrors.NewUsageError("invalid host port %q in mapping %q: %v", parts[0], p, err)
		}
		containerPort, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, yoerrors.NewUsageError("invalid container port %q in mapping %q: %v", parts[1], p, err)
		}
		result = append(result, runtime.PortMapping{
			HostPort:      hostPort,
			ContainerPort: containerPort,
			Protocol:      "tcp",
		})
	}

	return result, nil
}

// OverlayOrResolvedMountPath returns the container working directory path for a directory.
// For overlay mode, this is the bind-mounted merged path; otherwise the resolved mount path.
func OverlayOrResolvedMountPath(d *state.DirSpec) string {
	if d.Mode == "overlay" {
		return "/yoloai/overlay/" + store.EncodePath(d.Path) + "/merged"
	}
	return d.ResolvedMountPath()
}

// readLogTail returns the last n lines of the file at path.
// Returns empty string on any error or if the file is empty.
func readLogTail(path string, n int) string {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is constructed from sandbox dir
	if err != nil || len(data) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// parseResourceLimits converts user-facing string resource limits to
// runtime-level int64 values (NanoCPUs, bytes).
func parseResourceLimits(rl *config.ResourceLimits) (*runtime.ResourceLimits, error) {
	result := &runtime.ResourceLimits{}

	if rl.CPUs != "" {
		cpus, err := strconv.ParseFloat(rl.CPUs, 64)
		if err != nil || cpus <= 0 {
			return nil, fmt.Errorf("invalid cpus value %q: must be a positive number (e.g., 4, 2.5)", rl.CPUs)
		}
		result.NanoCPUs = int64(cpus * 1e9)
	}

	if rl.Memory != "" {
		mem, err := parseMemoryString(rl.Memory)
		if err != nil {
			return nil, err
		}
		result.Memory = mem
	}

	if result.NanoCPUs == 0 && result.Memory == 0 {
		return nil, nil
	}
	return result, nil
}

// parseMemoryString parses a Docker-style memory string (e.g., "512m", "8g")
// into bytes. Supported suffixes: b, k, m, g (case-insensitive).
func parseMemoryString(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}

	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return 0, fmt.Errorf("empty memory value")
	}

	// Check for suffix
	lastChar := strings.ToLower(s[len(s)-1:])
	var multiplier int64 = 1
	numStr := s

	switch lastChar {
	case "b":
		numStr = s[:len(s)-1]
	case "k":
		multiplier = 1024
		numStr = s[:len(s)-1]
	case "m":
		multiplier = 1024 * 1024
		numStr = s[:len(s)-1]
	case "g":
		multiplier = 1024 * 1024 * 1024
		numStr = s[:len(s)-1]
	default:
		// No suffix — treat as bytes
	}

	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil || val <= 0 {
		return 0, fmt.Errorf("invalid memory value %q: must be a positive number with optional suffix (b, k, m, g)", s)
	}

	return int64(val * float64(multiplier)), nil
}

// outputOr returns o when non-nil, otherwise io.Discard, so leaf writers never
// see a nil io.Writer. Mirrors the façade Engine.outputFor.
func outputOr(o io.Writer) io.Writer {
	if o != nil {
		return o
	}
	return io.Discard
}
