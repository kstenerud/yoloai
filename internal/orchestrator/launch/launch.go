// ABOUTME: translating resolved State into a runtime.InstanceConfig, starting
// ABOUTME: the container, and verifying it is running. When the backend supports
// ABOUTME: runtime.ProcessLauncher the container is brought up agent-free (keepalive
// ABOUTME: holder) and sandbox-setup.py is launched as a separate process over it.
package launch

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/kstenerud/yoloai/internal/envsetup"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/netpolicy"
	mountspkg "github.com/kstenerud/yoloai/internal/orchestrator/mounts"
	"github.com/kstenerud/yoloai/internal/orchestrator/runtimeconfig"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/internal/runtime"
	"github.com/kstenerud/yoloai/internal/store"
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

	secretsDir, err := envsetup.CreateSecretsDir(st.Agent, envVars, st.Layout, st.Layout.SecretsStagingDir)
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
//
// When the backend implements runtime.ProcessLauncher the box is brought up
// agent-free (keepalive_only holder) and sandbox-setup.py is launched as a
// separate process over it — the S3 re-route. Backends without ProcessLauncher
// follow the legacy path: the agent is welded into the entrypoint as before.
func buildAndStart(ctx context.Context, rt runtime.Backend, st *state.State, mnts []runtime.MountSpec, ports []runtime.PortMapping, hasSecrets bool) error {
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

	if launcher, ok := runtime.LauncherOf(rt); ok {
		if err := startViaLaunch(ctx, rt, launcher, st, cname, instanceCfg, markerPath, hasSecrets); err != nil {
			return err
		}
	} else {
		if err := startLegacy(ctx, rt, st, cname, instanceCfg, markerPath, hasSecrets); err != nil {
			return err
		}
	}

	return verifyInstanceRunning(ctx, rt, st, cname)
}

// startViaLaunch brings up the container agent-free (keepalive_only) and then
// launches sandbox-setup.py as a separate process over it. Used when the
// backend implements runtime.ProcessLauncher (currently Docker only).
//
// Ordering that matters:
//  1. Patch runtime-config.json with keepalive_only:true BEFORE Create, so the
//     entrypoint reads it on first boot and takes the holder branch.
//  2. Create + Start (box comes up on the sleep-infinity holder).
//  3. Wait for the .substrate-ready marker — the entrypoint writes it after root
//     provisioning completes, immediately before exec'ing the holder. A runner
//     launched DURING root setup (UID remap etc.) is silently killed (DF44).
//  4. Launch sandbox-setup.py — it reads /run/secrets and writes the
//     .secrets-consumed marker itself.
//  5. waitForSecretsConsumed AFTER Launch, so the marker is written by the
//     launched runner before the host removes the secrets dir.
func startViaLaunch(ctx context.Context, rt runtime.Backend, launcher runtime.ProcessLauncher, st *state.State, cname string, instanceCfg runtime.InstanceConfig, markerPath string, hasSecrets bool) error {
	if err := patchKeepaliveOnly(st.SandboxDir, true); err != nil {
		return fmt.Errorf("patch keepalive_only: %w", err)
	}

	// Clear any stale readiness marker from a prior boot so the wait below sees
	// only this launch's signal (it lives in the persistent sandbox dir).
	readyPath := filepath.Join(st.SandboxDir, store.SubstrateReadyMarker)
	_ = os.Remove(readyPath) //nolint:errcheck // best-effort; absent is fine

	if err := rt.Create(ctx, instanceCfg); err != nil {
		return gvisorStartHint(st.Isolation, err)
	}
	if err := rt.Start(ctx, cname); err != nil {
		return fmt.Errorf("start instance: %w", gvisorStartHint(st.Isolation, err))
	}

	// The box must finish root provisioning before we launch the session-runner
	// over it; otherwise the runner is killed mid-setup (DF44 readiness race).
	// The substrate owns the readiness signal (launcher.Ready); we own the wait
	// policy.
	if err := waitForReady(ctx, launcher, cname, effectiveSecretsConsumedTimeout(rt.Descriptor())); err != nil {
		return err
	}

	_, err := launcher.Launch(ctx, cname, runtime.ProcSpec{
		Argv:     []string{"sh", "-c", "exec python3 /yoloai/bin/sandbox-setup.py docker >> /yoloai/logs/session-runner.log 2>&1"},
		User:     "yoloai",
		Cwd:      OverlayOrResolvedMountPath(st.Workdir),
		Env:      []string{"HOME=/home/yoloai", "YOLOAI_DIR=/yoloai"},
		Detached: true,
	})
	if err != nil {
		return fmt.Errorf("launch session-runner: %w", err)
	}

	// The secrets marker is now written by the launched runner, not the
	// entrypoint — so we wait here, after Launch, not after Start.
	if hasSecrets {
		waitForSecretsConsumed(markerPath, effectiveSecretsConsumedTimeout(rt.Descriptor()))
	}
	return nil
}

// startLegacy is the original bring-up path for backends without
// runtime.ProcessLauncher: create the instance, start it, and wait for the
// entrypoint (which runs sandbox-setup.py inline) to consume secrets.
// No keepalive_only patch; the agent is welded into the entrypoint as before.
func startLegacy(ctx context.Context, rt runtime.Backend, st *state.State, cname string, instanceCfg runtime.InstanceConfig, markerPath string, hasSecrets bool) error {
	if err := rt.Create(ctx, instanceCfg); err != nil {
		return gvisorStartHint(st.Isolation, err)
	}
	if err := rt.Start(ctx, cname); err != nil {
		return fmt.Errorf("start instance: %w", gvisorStartHint(st.Isolation, err))
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
	return nil
}

// patchKeepaliveOnly reads runtime-config.json in sandboxDir, sets (or clears)
// the keepalive_only field, and writes it back atomically. Called before
// rt.Create so the entrypoint reads the updated config on first boot.
func patchKeepaliveOnly(sandboxDir string, keepalive bool) error {
	configPath := filepath.Join(sandboxDir, store.RuntimeConfigFile)
	data, err := os.ReadFile(configPath) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return fmt.Errorf("read runtime-config.json: %w", err)
	}
	var cfg runtimeconfig.ContainerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse runtime-config.json: %w", err)
	}
	cfg.KeepaliveOnly = keepalive
	updated, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime-config.json: %w", err)
	}
	if err := fileutil.WriteFile(configPath, updated, 0600); err != nil {
		return fmt.Errorf("write runtime-config.json: %w", err)
	}
	return nil
}

// buildInstanceConfig constructs the runtime.InstanceConfig from sandbox state.
func buildInstanceConfig(desc runtime.BackendDescriptor, st *state.State, mnts []runtime.MountSpec, ports []runtime.PortMapping) (runtime.InstanceConfig, error) {
	cname := store.InstanceName(st.Layout.Principal, st.Name)
	caps := desc.Capabilities

	if st.NetworkMode == "isolated" {
		// Whether the allowlist can actually be enforced is a netpolicy decision:
		// it composes the backend's capability with the isolation mode's in-sandbox
		// iptables honoring (gVisor refuses). See netpolicy.CanEnforce.
		if ok, reason := netpolicy.CanEnforce(netpolicy.StrategyIPFilter, caps, desc.Type, st.Isolation); !ok {
			return runtime.InstanceConfig{}, errors.New(reason)
		}
	}

	instanceCfg := runtime.InstanceConfig{
		Name:        cname,
		ImageRef:    st.ImageRef,
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

// waitForReady polls the substrate's own readiness signal (launcher.Ready) until
// it reports the box can accept a launched process, or the timeout elapses. The
// substrate owns HOW readiness is determined; this owns the wait policy. Unlike
// the secrets wait, a timeout here is a hard error: launching the session-runner
// before the box is provisioned gets it silently killed (DF44 readiness race),
// so we refuse to launch into a box that never signalled ready. Transient Ready
// errors during boot (the container briefly not accepting execs) are tolerated
// until the deadline.
func waitForReady(ctx context.Context, launcher runtime.ProcessLauncher, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		ready, err := launcher.Ready(ctx, name)
		if err == nil && ready {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("substrate not ready within %s: %w", timeout, lastErr)
			}
			return fmt.Errorf("substrate not ready within %s (root provisioning did not complete)", timeout)
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

// gvisorStartHint augments an opaque gVisor sandbox-start failure with an
// actionable pointer. Only fires for container-enhanced; other errors pass
// through unchanged. Two common macOS failure modes get distinct advice:
//   - runsc is registered with the daemon but the binary isn't actually in the
//     VM ("looking up the specified runtime path ... no such file").
//   - the OrbStack /tmp -> /private/tmp virtiofs symlink collides with gVisor's
//     hard-coded /tmp chroot ("cannot read client sync file: EOF").
func gvisorStartHint(isolation runtime.IsolationMode, err error) error {
	if isolation != runtime.IsolationModeContainerEnhanced || err == nil {
		return err
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "looking up the specified runtime path"),
		strings.Contains(msg, "runsc") && strings.Contains(msg, "no such file"):
		return fmt.Errorf("%w\n\ngVisor (container-enhanced): runsc is registered with the "+
			"daemon but the binary isn't present where the daemon runs. On macOS that's inside "+
			"the Docker VM (Docker Desktop / OrbStack) — install runsc there, not on the host. "+
			"See the gVisor setup notes in docs/GUIDE.md", err)
	case strings.Contains(msg, "cannot read client sync file"),
		strings.Contains(msg, "OCI runtime create"):
		return fmt.Errorf("%w\n\ngVisor (container-enhanced) failed to start the sandbox. "+
			"On macOS this is usually the OrbStack /tmp->/private/tmp symlink colliding with "+
			"gVisor's /tmp chroot; Docker Desktop is unaffected. See "+
			"docs/contributors/backend-idiosyncrasies.md (\"OrbStack: gVisor ... /tmp\") and the "+
			"gVisor setup notes in docs/GUIDE.md", err)
	default:
		return err
	}
}

// verifyInstanceRunning checks that the instance is still running after start,
// collecting log output for diagnostics if it has exited.
func verifyInstanceRunning(ctx context.Context, rt runtime.Backend, st *state.State, cname string) error {
	// Verify instance is still running (catches immediate crashes). A real crash
	// leaves the container inspectable with Running=false (handled below). A
	// transient ErrNotFound right after start is different: under load the daemon
	// API can briefly fail to resolve a just-started container, so retry the
	// inspect for a few seconds before treating not-found as a hard failure.
	// Other inspect errors are returned immediately.
	var info runtime.InstanceInfo
	deadline := time.Now().Add(4 * time.Second)
	for {
		time.Sleep(1 * time.Second)
		var err error
		info, err = rt.Inspect(ctx, cname)
		if err == nil {
			break
		}
		if errors.Is(err, runtime.ErrNotFound) && time.Now().Before(deadline) {
			continue
		}
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
