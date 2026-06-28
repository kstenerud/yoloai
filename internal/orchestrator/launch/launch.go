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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kstenerud/yoloai/internal/agent"
	"github.com/kstenerud/yoloai/internal/broker"
	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/envsetup"
	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/netpolicy"
	"github.com/kstenerud/yoloai/internal/orchestrator/agentcfg"
	"github.com/kstenerud/yoloai/internal/orchestrator/envspec"
	mountspkg "github.com/kstenerud/yoloai/internal/orchestrator/mounts"
	"github.com/kstenerud/yoloai/internal/orchestrator/runtimeconfig"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/store"
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

	spec := envspec.BuildEnvSpec(st.Agent)
	var secretsDir string
	var secretEnv map[string]string
	if _, agentFree := usesAgentFreeLaunch(d.Runtime, st.Isolation); agentFree {
		// Launch path (Docker): deliver secrets via the launched process's env; no host staging, no mount, no marker.
		secretEnv = envsetup.ResolveSecretEnv(spec, envVars, st.Layout)
	} else {
		// Legacy path: stage host files bind-mounted at /run/secrets (unchanged).
		var err error
		secretsDir, err = envsetup.CreateSecretsDir(spec, envVars, st.Layout, st.Layout.SecretsStagingDir)
		if err != nil {
			return fmt.Errorf("create secrets: %w", err)
		}
		if secretsDir != "" {
			defer os.RemoveAll(secretsDir) //nolint:errcheck // best-effort cleanup
		}
	}

	mnts := mountspkg.Build(st, secretsDir) // secretsDir=="" on the launch path -> no /run/secrets mount

	ports, err := parsePortBindings(st.Ports)
	if err != nil {
		return err
	}
	ports = filterAvailablePorts(ports, outputOr(st.Output))

	return buildAndStart(ctx, d.Runtime, st, mnts, ports, secretsDir != "", secretEnv)
}

// usesAgentFreeLaunch reports whether this sandbox uses the D88 agent-free
// keepalive+Launch bring-up: the backend must implement runtime.ProcessLauncher
// AND opt in via Capabilities.AgentFreeLaunch AND the isolation mode must support
// the host-side launch exec (runtime.SupportsAgentFreeLaunch — false for gVisor;
// see that function). Both the secrets-delivery choice and the container bring-up
// gate on this single helper, so they stay in sync — a sandbox on legacy bring-up
// also gets legacy /run/secrets staging. Podman inherits Launch by embedding the
// Docker runtime but does not opt in, so it takes the legacy path; docker under
// container-enhanced (gVisor) opts in at the backend level but is excluded by
// isolation, so it too takes the legacy in-entrypoint weld.
func usesAgentFreeLaunch(rt runtime.Backend, isolation runtime.IsolationMode) (runtime.ProcessLauncher, bool) {
	launcher, ok := runtime.LauncherOf(rt)
	return launcher, ok && rt.Descriptor().Capabilities.AgentFreeLaunch && runtime.SupportsAgentFreeLaunch(isolation)
}

// buildAndStart constructs the runtime InstanceConfig from State and
// starts the instance. hasSecrets indicates whether secrets were injected via
// a temporary directory that the caller will remove after this call returns.
// secretEnv is the secret map delivered via ProcSpec.Env on the Launch path
// (nil on the legacy path). Extracted from launchContainer().
//
// When the backend opts into the agent-free bring-up (usesAgentFreeLaunch) the box
// comes up on a keepalive_only holder and sandbox-setup.py is launched as a
// separate process over it — the S3 re-route. Otherwise it follows the legacy
// path: the agent is welded into the entrypoint as before.
func buildAndStart(ctx context.Context, rt runtime.Backend, st *state.State, mnts []runtime.MountSpec, ports []runtime.PortMapping, hasSecrets bool, secretEnv map[string]string) error {
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

	// Use the D88 keepalive-holder + Launch bring-up only for backends that opt in
	// (see usesAgentFreeLaunch). Secrets delivery (above) is gated on the same
	// condition, so the two stay in sync — a backend on the legacy bring-up also
	// gets legacy /run/secrets staging.
	if launcher, ok := usesAgentFreeLaunch(rt, st.Isolation); ok {
		if err := startViaLaunch(ctx, rt, launcher, st, cname, instanceCfg, markerPath, hasSecrets, secretEnv); err != nil {
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
//  4. Launch sandbox-setup.py — secrets arrive in ProcSpec.Env (YOLOAI_SECRET_KEYS
//     + named vars); there is no /run/secrets read on this path.
//  5. The secrets-consumed marker wait is SKIPPED on the Launch path (hasSecrets is
//     false; secrets were not staged to a host dir so no synchronization is needed).
func startViaLaunch(ctx context.Context, rt runtime.Backend, launcher runtime.ProcessLauncher, st *state.State, cname string, instanceCfg runtime.InstanceConfig, markerPath string, hasSecrets bool, secretEnv map[string]string) error {
	if err := patchKeepaliveOnly(st.SandboxDir, true); err != nil {
		return fmt.Errorf("patch keepalive_only: %w", err)
	}

	// Signal keepalive_only to the entrypoint via an env var as well, not only the
	// patched runtime-config.json. The file patch is an atomic rename (new inode)
	// immediately before Create; Docker Desktop's gRPC-FUSE serves the stale
	// pre-patch content for that single-file bind mount when the entrypoint reads
	// it at container start, so the box silently takes the legacy inline path and
	// never writes .substrate-ready (waitForReady then times out). An env var is
	// baked into the container config at create, immune to mount-propagation lag,
	// and the entrypoint treats it as authoritative. The file patch remains as the
	// Linux/OrbStack-side record and a backstop. See backend-idiosyncrasies.md.
	instanceCfg.ContainerEnv = append(instanceCfg.ContainerEnv, "YOLOAI_KEEPALIVE_ONLY=1")

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

	// Brokering (default for supported backends; --no-broker opts out): start the
	// host-side injector and rewrite secretEnv so the real key stays host-side and
	// the agent is pointed at the injector. Runs after Start (the network gateway
	// is now knowable) and before secretEnv is materialized into the launched
	// process's env.
	if err := brokerCredentials(ctx, rt, st, cname, secretEnv); err != nil {
		return err
	}

	env := []string{"HOME=/home/yoloai", "YOLOAI_DIR=/yoloai"}
	if len(secretEnv) > 0 {
		keys := make([]string, 0, len(secretEnv))
		for k := range secretEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic ordering
		for _, k := range keys {
			env = append(env, k+"="+secretEnv[k])
		}
		env = append(env, "YOLOAI_SECRET_KEYS="+strings.Join(keys, ","))
	}

	_, err := launcher.Launch(ctx, cname, runtime.ProcSpec{
		Argv:     []string{"sh", "-c", "exec python3 /yoloai/bin/sandbox-setup.py docker >> /yoloai/logs/session-runner.log 2>&1"},
		User:     "yoloai",
		Cwd:      OverlayOrResolvedMountPath(st.Workdir),
		Env:      env,
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

// brokerCredentials starts the host-side credential injector for the sandbox and
// rewrites secretEnv so the real API key never enters the container: the key is
// removed and the agent is pointed at the injector (base_url + a dummy
// placeholder), which swaps in the real key host-side.
//
// Brokering is the DEFAULT for a brokerable agent on a supporting backend (D105).
// The posture is tri-state: forced-off (--no-broker, st.BrokerDisabled) skips;
// forced-on (--broker, st.BrokerCredentials) requires it; otherwise it is auto.
// It falls through to direct delivery (a no-op here) when brokering is off, the
// agent isn't brokerable, no brokerable key is present (e.g. a subscription
// login), or — in auto mode only — the backend can't host an injector. The one
// hard error is forced-on against a backend that can't host a sandbox-reachable
// injector: the user explicitly asked for the key to be withheld, so we refuse
// rather than silently leak it via direct delivery.
func brokerCredentials(ctx context.Context, rt runtime.Backend, st *state.State, cname string, secretEnv map[string]string) error {
	if st.BrokerDisabled || st.Agent == nil || st.Agent.Broker == nil {
		return nil
	}
	bc := st.Agent.Broker

	realKey := secretEnv[bc.APIKeyEnvVar]
	if realKey == "" {
		// Nothing brokerable (e.g. subscription/OAuth). Direct delivery unchanged.
		if st.BrokerCredentials {
			slog.Info("brokering requested but no brokerable API key present; using direct delivery",
				"event", "sandbox.broker.skip", "sandbox", st.Name, "key_env", bc.APIKeyEnvVar)
		}
		return nil
	}

	// Restricted networking can't reach the host-side injector yet: the in-sandbox
	// allowlist (iptables) doesn't include the bridge gateway the injector binds,
	// and --network-none has no egress at all. Composing brokering with an egress
	// allowlist (allowlisting the injector) is the containment layer's job, a later
	// phase. Until then auto falls back to direct delivery; an explicit --broker
	// errors rather than silently breaking the agent's API access.
	if st.NetworkMode == "isolated" || st.NetworkMode == "none" {
		if st.BrokerCredentials {
			return fmt.Errorf("--broker is not yet supported with --network-%s: the in-sandbox allowlist can't reach the host-side injector. Omit --broker, or use open networking", st.NetworkMode)
		}
		return nil
	}

	reachable, ok := runtime.InjectorReachOf(rt)
	if !ok {
		if st.BrokerCredentials {
			return fmt.Errorf("--broker requested but the %s backend cannot host a sandbox-reachable injector", rt.Descriptor().Type)
		}
		return nil // auto: this backend can't broker; direct delivery
	}
	reach, err := reachable.InjectorReach(ctx, cname)
	if err != nil {
		return fmt.Errorf("resolve injector reachability: %w", err)
	}

	addr, err := broker.NewSidecarHost().Ensure(ctx, buildInjectorSpec(st.SandboxDir, bc, reach, realKey))
	if err != nil {
		return fmt.Errorf("start credential injector: %w", err)
	}

	if err := applyBrokerEnv(secretEnv, bc, reach, addr); err != nil {
		return err
	}
	slog.Info("brokered agent API key through host-side injector",
		"event", "sandbox.broker.active", "sandbox", st.Name, "upstream", bc.UpstreamURL)
	return nil
}

// buildInjectorSpec assembles the injector spec from an agent's BrokerConfig, the
// backend's reachability, and the resolved real key. Shared by the launch-time
// broker step and ReconcileInjector so the two never drift.
func buildInjectorSpec(sandboxDir string, bc *agent.BrokerConfig, reach runtime.InjectorReach, realKey string) broker.InjectorSpec {
	return broker.InjectorSpec{
		SandboxDir:   sandboxDir,
		BindHost:     reach.BindHost,
		UpstreamURL:  bc.UpstreamURL,
		StripHeaders: []string{"Authorization"},
		Bindings: []broker.BindingConfig{{
			Destination: bc.Destination,
			Kind:        broker.KindHeaderSet,
			Header:      bc.Header,
			Prefix:      bc.Prefix,
			Secret:      realKey,
		}},
	}
}

// ReconcileInjector respawns a sandbox's host-side credential injector if it has
// died (D106 lazy reconcile). It is best-effort and cheap on the common path: a
// sandbox that was never brokered, or whose injector is still alive, returns
// immediately without opening the backend or re-deriving any credential. Only a
// dead injector triggers the heavier respawn — re-deriving the key from the
// CURRENT ambient env (D106: no secret is ever persisted) and rebinding the
// recorded port so the container's base_url stays valid.
func ReconcileInjector(ctx context.Context, d state.Deps, name string) error {
	sandboxDir := d.Layout.SandboxDir(name)
	if !broker.HasRecord(sandboxDir) {
		return nil // not a brokered sandbox
	}
	if broker.InjectorAlive(sandboxDir) {
		return nil // injector healthy — the overwhelmingly common case
	}

	// Dead injector: reconstruct its spec and respawn it.
	acfg, err := agentcfg.Load(sandboxDir)
	if err != nil {
		return fmt.Errorf("reconcile injector: load agent config: %w", err)
	}
	agentDef := agent.GetAgent(acfg.AgentType)
	if agentDef == nil || agentDef.Broker == nil {
		return nil // agent no longer registered or not brokerable
	}
	bc := agentDef.Broker

	cname := store.InstanceName(d.Layout.Principal, name)
	info, err := d.Runtime.Inspect(ctx, cname)
	if err != nil {
		return fmt.Errorf("reconcile injector: inspect %s: %w", cname, err)
	}
	if !info.Running {
		return nil // injector only matters while the container runs
	}

	cfg, err := config.LoadConfig(d.Layout)
	if err != nil {
		return fmt.Errorf("reconcile injector: load config: %w", err)
	}
	secretEnv := envsetup.ResolveSecretEnv(envspec.BuildEnvSpec(agentDef), cfg.Env, d.Layout)
	realKey := secretEnv[bc.APIKeyEnvVar]
	if realKey == "" {
		slog.Warn("cannot reconcile credential injector: no brokerable API key in the current environment",
			"event", "sandbox.broker.reconcile.nokey", "sandbox", name, "key_env", bc.APIKeyEnvVar)
		return nil
	}

	reachable, ok := runtime.InjectorReachOf(d.Runtime)
	if !ok {
		return nil // backend can't host an injector (shouldn't happen if it was brokered)
	}
	reach, err := reachable.InjectorReach(ctx, cname)
	if err != nil {
		return fmt.Errorf("reconcile injector: resolve reachability: %w", err)
	}

	if _, err := broker.NewSidecarHost().Ensure(ctx, buildInjectorSpec(sandboxDir, bc, reach, realKey)); err != nil {
		return fmt.Errorf("reconcile injector: respawn: %w", err)
	}
	slog.Info("respawned dead credential injector",
		"event", "sandbox.broker.reconcile.active", "sandbox", name)
	return nil
}

// applyBrokerEnv rewrites secretEnv in place for a brokered launch: it drops the
// real key, points the agent at the injector via base_url (DialHost + the
// injector's port), and sets the placeholder token. injectorAddr is the
// injector's bound address (BindHost:port); only its port is used, since the
// agent reaches the injector via DialHost (which may differ from BindHost, e.g.
// Docker Desktop). It is a pure function so the env rewrite is unit-tested.
func applyBrokerEnv(secretEnv map[string]string, bc *agent.BrokerConfig, reach runtime.InjectorReach, injectorAddr string) error {
	_, port, err := net.SplitHostPort(injectorAddr)
	if err != nil {
		return fmt.Errorf("parse injector address %q: %w", injectorAddr, err)
	}
	delete(secretEnv, bc.APIKeyEnvVar)
	secretEnv[bc.BaseURLEnvVar] = "http://" + net.JoinHostPort(reach.DialHost, port)
	secretEnv[bc.AuthTokenEnvVar] = bc.DummyToken
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
