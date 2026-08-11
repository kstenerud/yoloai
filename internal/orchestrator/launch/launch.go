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
	"slices"
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
	"github.com/kstenerud/yoloai/internal/orchestrator/profiles"
	"github.com/kstenerud/yoloai/internal/orchestrator/runtimeconfig"
	"github.com/kstenerud/yoloai/internal/orchestrator/state"
	"github.com/kstenerud/yoloai/runtime"
	"github.com/kstenerud/yoloai/runtime/caps"
	"github.com/kstenerud/yoloai/store"
	"github.com/kstenerud/yoloai/yoerrors"
)

// secretsConsumedTimeout bounds how long buildAndStart waits for the
// in-sandbox entrypoint to signal it has read /run/secrets. Generous
// enough to cover a cold Kata VM boot + virtio-fs propagation; on
// timeout the caller removes the secrets dir anyway (we never leak it).
const secretsConsumedTimeout = 30 * time.Second

// WarningPrefix marks a line written to state.State.Output as a warning rather
// than progress. That stream carries both — port-availability warnings from
// filterAvailablePorts alongside a streamed image build from ensureImageLineage —
// so a line's level can only be read from the line.
//
// On the create path Output is a terminal and the prefix simply prints. On the
// restart path it is a noticeWriter, which classifies on this exact constant and
// strips it, so the level survives into a structured Notice instead of the whole
// stream being labelled one way. It is exported for that consumer: sharing the
// literal is what stops the producer and the classifier from drifting apart,
// which is how every line of build progress came to be reported as a warning
// (DF157).
const WarningPrefix = "Warning: "

// LaunchContainer creates a sandbox instance from State, starts it,
// and cleans up credential temp files. Used by both initial creation and
// recreation from environment.json.
func LaunchContainer(ctx context.Context, d state.Deps, st *state.State) (err error) {
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

	// Resolve the secret map once, then broker it BEFORE the delivery split:
	// brokerCredentials starts the host-side injector at the backend's
	// network-level endpoint (knowable pre-create) and rewrites the map (drop the
	// real credential, point the agent at the injector). Brokering is now
	// bring-up-agnostic — the rewritten map is delivered by whichever channel the
	// launch path uses (ProcSpec.Env for agent-free, /run/secrets files for legacy)
	// — so both paths broker identically (D105/D106).
	spec := envspec.BuildEnvSpec(st.Agent)
	secretEnv := envsetup.ResolveSecretEnv(spec, envVars, st.Layout)
	bro, err := brokerCredentials(ctx, d.Runtime, st, secretEnv)
	if err != nil {
		return err
	}

	// Record the workdir as trusted for CLIs that block on a folder-trust prompt
	// (Codex, DF85). Unconditional — independent of brokering — and after the
	// broker patch so it composes with any openai_base_url written above.
	if err := applyWorkdirTrust(st); err != nil {
		return err
	}

	// For a file-auth CLI that was NOT brokered, materialize the real credential
	// into its auth file (Codex, DF84). Runs on secretEnv while it still holds the
	// real key — brokering removed it above, so this is a no-op for brokered launches.
	if err := applyDirectCredential(st, secretEnv); err != nil {
		return err
	}

	// The injector is now live, and the steps below may create/start the container
	// and its CNI netns. Roll the whole partial launch back on any failure from
	// here on, so re-running the launch (yoloai start) isn't balked by a leftover
	// container/netns and the detached injector is never orphaned. This also covers
	// Ctrl-C: the signal cancels ctx, the next ctx-aware call errors, and this defer
	// cleans up on the way out (rollbackPartialLaunch uses a detached context, since
	// the cancelled ctx can't reach the backend).
	defer func() {
		if err != nil {
			rollbackPartialLaunch(ctx, d.Runtime, st)
		}
	}()

	// Deliver the (already-brokered) map. Agent-free hands it to the launched
	// process's env; legacy stages it to host files bind-mounted at /run/secrets.
	var secretsDir string
	if _, agentFree := usesAgentFreeLaunch(d.Runtime, st.Isolation); !agentFree {
		var err error
		secretsDir, err = envsetup.StageSecretEnv(secretEnv, st.Layout, st.Layout.SecretsStagingDir)
		if err != nil {
			return fmt.Errorf("create secrets: %w", err)
		}
		if secretsDir != "" {
			defer os.RemoveAll(secretsDir) //nolint:errcheck // best-effort cleanup
		}
		secretEnv = nil // legacy delivers via the bind-mounted files, not ProcSpec.Env
	}

	mnts := mountspkg.Build(st, secretsDir) // secretsDir=="" on the launch path -> no /run/secrets mount

	ports, err := parsePortBindings(st.Ports)
	if err != nil {
		return err
	}
	ports = filterAvailablePorts(ports, outputOr(st.Output))

	for _, w := range advisoryWarnings(ctx, d.Runtime, st.Isolation) {
		fmt.Fprintf(outputOr(st.Output), WarningPrefix+"%s\n", w) //nolint:errcheck // best-effort output
	}

	// Re-ensure the image right before bringing it up, not only at create (DF156).
	// A sandbox created before a yoloAI upgrade relaunches through here, and its
	// image was built against a base this binary no longer expects — the host
	// drives scripts and CLIs that live in that image, so a mismatch is a
	// host/guest version skew, not cosmetic drift. force=false: the chain
	// checksum already folds in the base, so a moved base rebuilds the profile
	// without forcing a base rebuild that Setup has just established is current.
	baseChecksum, err := ensureImageLineage(ctx, d, st)
	if err != nil {
		return err
	}

	// Deliver the runtime scripts from this binary rather than from whatever the
	// image baked, which is what keeps a yoloAI upgrade from requiring a rebuild
	// of the user's image (DF156). Appended after the image work above so it wins
	// on path collision with anything the image put at /yoloai/bin.
	scriptMount, err := deliverRuntimeScripts(d, st)
	if err != nil {
		return err
	}
	mnts = append(mnts, scriptMount...)

	if err = buildAndStart(ctx, d.Runtime, st, mnts, ports, secretsDir != "", secretEnv, bro, baseChecksum); err != nil {
		return err // the deferred rollbackPartialLaunch reaps the injector + container + netns
	}
	return nil
}

// ensureImageLineage brings the sandbox's image up to date with this binary
// before it is launched (DF156).
//
// The create path already does this, but create is not the only way an image
// gets launched: a stopped sandbox is removed and recreated on `start`, and it
// carries an image built by whatever yoloAI version created it. Checking only at
// create leaves every pre-existing sandbox on the old contract, which for a tool
// whose sandboxes are long-lived is the common case rather than the edge.
//
// Backends with no image concept (tart, seatbelt) have no builder and are a
// no-op — they deliver their scripts at launch already, so the skew this guards
// against cannot arise there.
//
// It returns the base-lineage checksum the now-current image carries, for
// stamping onto the instance. The value is READ off the image rather than taken
// from the binary: what gets recorded should be what the sandbox actually holds,
// so that an image carrying something unexpected is preserved as evidence rather
// than overwritten with what we assume. Empty when there is nothing to record,
// which callers treat as "cannot say".
func ensureImageLineage(ctx context.Context, d state.Deps, st *state.State) (string, error) {
	builder, ok := runtime.ProfileImageBuilderOf(d.Runtime)
	if !ok {
		return "", nil
	}
	out, logger := outputOr(st.Output), slog.Default()

	// No profile means the sandbox runs yoloai-base itself, and Setup is the
	// check for that image. EnsureProfileImage cannot be used here: it resolves a
	// profile chain, and an empty name fails resolution rather than meaning
	// "none" — the create path avoids this by returning early before it is
	// reached, which is why the guard has to be explicit rather than implied.
	if st.Profile == "" {
		baseProfileDir := filepath.Join(st.Layout.ProfilesDir(), "base")
		if err := d.Runtime.Setup(ctx, st.Layout, baseProfileDir, out, logger, false); err != nil {
			return "", fmt.Errorf("ensure base image is current: %w", err)
		}
	} else if err := rebuildProfileImage(ctx, d.Runtime, st.Layout, st.Profile,
		profiles.AutoBuildSecrets(st.Layout.HomeDir), out, logger, false); err != nil {
		return "", fmt.Errorf("ensure image is current: %w", err)
	}

	labels, ok := builder.ImageLabels(ctx, st.ImageRef)
	if !ok {
		return "", nil // unreadable image: record nothing rather than a guess
	}
	return labels[runtime.BaseChecksumLabel], nil
}

// deliverRuntimeScripts materialises this binary's runtime scripts into the
// sandbox directory and returns the mount that puts them at
// runtime.RuntimeScriptDir. Nil for backends that already deliver at launch
// (tart writes them itself, seatbelt runs them as host processes), which is why
// the capability is the gate rather than a backend name.
//
// It runs on EVERY launch, not only at create, because that is the whole point:
// the scripts the sandbox runs are then always the ones the running binary was
// built with, whatever the image contains. A sandbox recreated after an upgrade
// picks them up with no rebuild of anything.
//
// The destination is the sandbox's own bin/ — the directory tart and seatbelt
// already use for exactly these files, already named (config.BinDirName) and
// already created with the sandbox. Nothing new to place, and nothing new to
// reclaim: destroying the sandbox removes it. Per-sandbox rather than shared also
// means a running sandbox keeps the scripts it launched with, instead of having
// them swapped underneath it by an upgrade in another terminal.
//
// Read-only because nothing in the guest has any business writing here; the
// scripts are yoloAI's own implementation, not a user surface. That is hygiene,
// not a security boundary — an agent that wanted to subvert them would kill the
// process and run its own copy, which no mount option prevents (DF156, A16).
func deliverRuntimeScripts(d state.Deps, st *state.State) ([]runtime.MountSpec, error) {
	provider, ok := runtime.RuntimeScriptProviderOf(d.Runtime)
	if !ok {
		return nil, nil
	}
	dir := config.BinPath(st.SandboxDir)
	if err := provider.WriteRuntimeScripts(dir); err != nil {
		return nil, fmt.Errorf("deliver runtime scripts: %w", err)
	}
	return []runtime.MountSpec{{
		HostPath:      dir,
		ContainerPath: runtime.RuntimeScriptDir,
		ReadOnly:      true,
	}}, nil
}

// rollbackPartialLaunch reverses a failed LaunchContainer: it stops+removes the
// container (Remove triggers the backend's netns/CNI teardown) and reaps the
// detached injector, leaving the sandbox cleanly "created, stopped" so re-running
// the launch isn't balked by leftover runtime state. Every step is best-effort and
// idempotent — a no-op when the artifact was never created. It runs on a detached,
// time-bounded context on purpose: the common trigger is Ctrl-C, which has already
// cancelled the caller's ctx, and a cancelled ctx can't reach the backend to clean
// up. The sandbox directory is intentionally kept — a failed start on an existing
// sandbox must stay "created"; composite verbs (new/run) remove the dir themselves.
func rollbackPartialLaunch(ctx context.Context, rt runtime.Backend, st *state.State) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	cname := store.InstanceName(st.Layout.Principal, st.Name)
	// All best-effort: no-ops when the artifact was never created.
	_ = rt.Stop(cleanupCtx, cname)
	_ = rt.Remove(cleanupCtx, cname)
	_ = broker.NewSidecarHost().Stop(cleanupCtx, st.SandboxDir)
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

// UsesSidecarFirewall reports whether a sandbox enforces its network allowlist
// from an out-of-container netns sidecar (tamper-resistant: the agent container is
// denied NET_ADMIN) rather than the in-entrypoint iptables. True only for an
// isolated sandbox on a backend that can run a netns sidecar AND uses the
// agent-free launch path (where the box comes up on a neutral holder, so the
// firewall can be installed before any agent egress). The legacy launch path runs
// the agent inline in the entrypoint, so it keeps the in-container firewall for
// now (see the build plan's deferred scope). This single predicate is the one
// source of truth for both the launch path (withhold NET_ADMIN, run the sidecar)
// and live allowlist patches (route ipset mutations through a sidecar).
func UsesSidecarFirewall(rt runtime.Backend, isolation runtime.IsolationMode, networkMode string) bool {
	if networkMode != "isolated" {
		return false
	}
	if _, ok := runtime.NetnsSidecarRunnerOf(rt); !ok {
		return false
	}
	_, agentFree := usesAgentFreeLaunch(rt, isolation)
	return agentFree
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
func buildAndStart(ctx context.Context, rt runtime.Backend, st *state.State, mnts []runtime.MountSpec, ports []runtime.PortMapping, hasSecrets bool, secretEnv map[string]string, bro brokerOutcome, baseChecksum string) error {
	cname := store.InstanceName(st.Layout.Principal, st.Name)
	sidecarFirewall := UsesSidecarFirewall(rt, st.Isolation, st.NetworkMode)
	instanceCfg, err := buildInstanceConfig(rt.Descriptor(), st, mnts, ports, bro, sidecarFirewall, baseChecksum)
	if err != nil {
		return err
	}

	// Clear any stale marker from a prior boot so the wait below observes
	// only this launch's signal (the marker file lives in the persistent
	// sandbox dir and survives restarts).
	markerPath := store.SecretsConsumedMarkerPath(st.SandboxDir)
	if hasSecrets {
		_ = os.Remove(markerPath)
	}

	// Use the D88 keepalive-holder + Launch bring-up only for backends that opt in
	// (see usesAgentFreeLaunch). Secrets delivery (above) is gated on the same
	// condition, so the two stay in sync — a backend on the legacy bring-up also
	// gets legacy /run/secrets staging.
	if launcher, ok := usesAgentFreeLaunch(rt, st.Isolation); ok {
		if err := startViaLaunch(ctx, rt, launcher, st, cname, instanceCfg, markerPath, hasSecrets, secretEnv, bro, sidecarFirewall); err != nil {
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
func startViaLaunch(ctx context.Context, rt runtime.Backend, launcher runtime.ProcessLauncher, st *state.State, cname string, instanceCfg runtime.InstanceConfig, markerPath string, hasSecrets bool, secretEnv map[string]string, bro brokerOutcome, sidecarFirewall bool) error {
	if err := patchKeepaliveOnly(st.SandboxDir, true); err != nil {
		return fmt.Errorf("patch keepalive_only: %w", err)
	}

	// Signal keepalive_only to the entrypoint via an env var as well, not only the
	// patched runtime-config.json. The file patch lands immediately before Create,
	// and Docker Desktop's gRPC-FUSE serves the stale pre-patch content for that
	// single-file bind mount when the entrypoint reads it at container start — so
	// the box silently takes the legacy inline path and never writes
	// .substrate-ready (waitForReady then times out). An env var is baked into the
	// container config at create, immune to mount-propagation lag, and the
	// entrypoint treats it as authoritative. The file patch remains as the
	// Linux/OrbStack-side record and a backstop. See backend-idiosyncrasies.md.
	//
	// This comment used to attribute the staleness to the patch being "an atomic
	// rename (new inode)". It is not: patchKeepaliveOnly calls fileutil.WriteFile,
	// an in-place truncate that keeps the inode, and it has done so since the
	// function was introduced — so the reproduced symptom was always an in-place
	// write, and the rename premise never described this code (DF173). What the
	// cache actually keys on is not established; do not reason from either story.
	instanceCfg.ContainerEnv = append(instanceCfg.ContainerEnv, "YOLOAI_KEEPALIVE_ONLY=1")

	// Clear any stale readiness marker from a prior boot so the wait below sees
	// only this launch's signal (it lives in the persistent sandbox dir).
	readyPath := store.SubstrateReadyMarkerPath(st.SandboxDir)
	_ = os.Remove(readyPath)

	if err := createWithImageRecovery(ctx, rt, st, instanceCfg); err != nil {
		return err
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

	// Tamper-resistant network isolation: install the firewall from a netns-sharing
	// sidecar BEFORE the agent is launched. The agent container has no CAP_NET_ADMIN
	// (buildInstanceConfig withholds it under the sidecar path), so the agent can't
	// flush a firewall installed from outside its reach. The box is up on the neutral
	// keepalive holder here — no agent egress yet — so installing now is safe; a failed
	// install fails the launch and the agent never runs unguarded.
	if sidecarFirewall {
		if err := installFirewallSidecar(ctx, rt, st, cname, bro); err != nil {
			return err
		}
	}

	// secretEnv is already brokered (LaunchContainer brokers before the delivery
	// split): the real credential is dropped and the agent points at the host-side
	// injector. Here it is just materialized into the launched process's env.
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
		Cwd:      WorkdirMountPath(st.Workdir),
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

// installFirewallSidecar runs the network-isolation firewall installer from an
// ephemeral container that shares the agent container's network namespace and
// holds CAP_NET_ADMIN (which the agent container is denied). The allowlist domains
// come from the sandbox's runtime-config.json (the same source the in-container
// entrypoint used) and the injector endpoint from the broker outcome; both are
// passed to the sidecar via the environment. A non-zero install fails the launch.
// The sidecar gets the same runtime-script mount the agent container does. It runs
// from an image with none of the target's mounts, so once the scripts are
// delivered at launch rather than baked, an unmounted sidecar would read
// install-firewall.py out of whatever the profile image inherited — the exact
// staleness being removed, in the one place that fails the launch outright
// instead of drifting quietly (DF156).
func installFirewallSidecar(ctx context.Context, rt runtime.Backend, st *state.State, cname string, bro brokerOutcome) error {
	runner, ok := runtime.NetnsSidecarRunnerOf(rt)
	if !ok {
		// UsesSidecarFirewall already checked this; defensive only.
		return fmt.Errorf("backend %s cannot run a netns firewall sidecar", rt.Descriptor().Type)
	}

	allowedDomains, err := loadAllowedDomains(st.SandboxDir)
	if err != nil {
		return fmt.Errorf("firewall sidecar: %w", err)
	}

	env := []string{"YOLOAI_FW_ALLOWED_DOMAINS=" + strings.Join(allowedDomains, " ")}
	if bro.InjectorEndpoint != "" {
		env = append(env, "YOLOAI_BROKER_INJECTOR_ENDPOINT="+bro.InjectorEndpoint)
	}

	scriptMount, err := deliverRuntimeScripts(state.Deps{Runtime: rt}, st)
	if err != nil {
		return err
	}

	spec := runtime.NetnsSidecarSpec{
		Target: cname,
		Image:  st.ImageRef,
		Argv:   []string{"python3", runtime.RuntimeScriptDir + "/install-firewall.py"},
		Env:    env,
		CapAdd: []string{"NET_ADMIN"},
		Mounts: scriptMount,
	}
	if err := runNetnsSidecarWithRetry(ctx, runner, spec, st.Name); err != nil {
		return fmt.Errorf("install network-isolation firewall: %w", err)
	}
	slog.Info("installed tamper-resistant network isolation via netns sidecar",
		"event", "sandbox.firewall.sidecar", "sandbox", st.Name, "allowed_domains", len(allowedDomains))
	return nil
}

// firewallSidecarAttempts and firewallSidecarRetryBackoff bound the retry of a
// failed firewall-sidecar install. The install is the only thing standing
// between launch and an agent running with unenforced network isolation, so it
// must fail closed — but the container runtime can transiently fail to
// materialize the ephemeral sidecar under heavy concurrent churn (observed on
// OrbStack when a fresh smoke run starts the instant a prior full-matrix run
// ends: the sidecar's rootfs is briefly incomplete, so python3 reports the
// embedded install-firewall.py as "No such file" even though the image has it).
// install-firewall.py is idempotent (apply_firewall flushes the OUTPUT chain
// and the ipset before re-adding rules), so re-running after a partial or failed
// attempt is safe. A genuinely-broken install still fails closed after the last
// attempt.
const (
	firewallSidecarAttempts     = 3
	firewallSidecarRetryBackoff = 500 * time.Millisecond
)

// runNetnsSidecarWithRetry runs the firewall sidecar, retrying a bounded number
// of times on any failure with a short backoff. The happy path runs once and
// never sleeps; the final attempt's error is returned so a persistent failure
// still fails the launch (closed).
func runNetnsSidecarWithRetry(ctx context.Context, runner runtime.NetnsSidecarRunner, spec runtime.NetnsSidecarSpec, sandbox string) error {
	var err error
	for attempt := 1; attempt <= firewallSidecarAttempts; attempt++ {
		if err = runner.RunNetnsSidecar(ctx, spec); err == nil {
			return nil
		}
		if attempt == firewallSidecarAttempts {
			break
		}
		slog.Warn("firewall sidecar install failed; retrying",
			"event", "sandbox.firewall.sidecar.retry", "sandbox", sandbox,
			"attempt", attempt, "max", firewallSidecarAttempts, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(firewallSidecarRetryBackoff):
		}
	}
	return err
}

// loadAllowedDomains reads the allowed-domains list from a sandbox's
// runtime-config.json — the composed agent-floor + user allowlist written at
// create time and kept current by network allow/deny.
func loadAllowedDomains(sandboxDir string) ([]string, error) {
	configPath := store.RuntimeConfigFilePath(sandboxDir)
	data, err := os.ReadFile(configPath) //nolint:gosec // path is sandbox-controlled
	if err != nil {
		return nil, fmt.Errorf("read runtime-config.json: %w", err)
	}
	var cfg runtimeconfig.ContainerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse runtime-config.json: %w", err)
	}
	return cfg.AllowedDomains, nil
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
// brokerOutcome is what a brokered launch needs downstream of brokerCredentials:
// the InstanceConfig.NetworkMode the sandbox must run (non-empty only when the
// backend needs a dedicated mode — rootless podman → slirp) and the injector's
// agent-facing endpoint (DialHost:port) to allowlist under network isolation. The
// zero value means brokering didn't engage (direct delivery).
type brokerOutcome struct {
	NetworkMode      string
	InjectorEndpoint string
}

func brokerCredentials(ctx context.Context, rt runtime.Backend, st *state.State, secretEnv map[string]string) (brokerOutcome, error) {
	if st.BrokerDisabled || st.Agent == nil || st.Agent.Broker == nil {
		return brokerOutcome{}, nil
	}
	bc := st.Agent.Broker

	cred, realKey, ok := bc.SelectCredential(secretEnv)
	if !ok {
		// No brokerable credential present (neither an API key nor a subscription
		// token). Direct delivery unchanged.
		if st.BrokerCredentials {
			slog.Info("brokering requested but no brokerable credential present; using direct delivery",
				"event", "sandbox.broker.skip", "sandbox", st.Name)
		}
		return brokerOutcome{}, nil
	}

	// --network-none has no egress at all, so the injector is unreachable. (Under
	// --network-isolated brokering DOES compose now — the injector endpoint is
	// allowlisted below.) Auto falls back to direct delivery; an explicit --broker
	// errors rather than silently breaking the agent's API access.
	if st.NetworkMode == "none" {
		if st.BrokerCredentials {
			return brokerOutcome{}, fmt.Errorf("--broker is not supported with --network-none: the sandbox has no egress to reach the host-side injector. Omit --broker, or use open or isolated networking")
		}
		return brokerOutcome{}, nil
	}

	reach, ok, err := resolveBrokerReach(ctx, rt, st)
	if err != nil || !ok {
		return brokerOutcome{}, err
	}

	placeholderToken, err := broker.PlaceholderToken(st.SandboxDir)
	if err != nil {
		return brokerOutcome{}, fmt.Errorf("broker placeholder token: %w", err)
	}

	addr, err := broker.NewSidecarHost().Ensure(ctx, buildInjectorSpec(st.SandboxDir, bc, cred, reach, realKey, placeholderToken))
	if err != nil {
		return brokerOutcome{}, fmt.Errorf("start credential injector: %w", err)
	}

	endpoint, err := applyBrokerEnv(secretEnv, bc, reach, addr, placeholderToken)
	if err != nil {
		return brokerOutcome{}, err
	}
	if err := patchBrokerConfigFiles(st.SandboxDir, bc, endpoint, placeholderToken); err != nil {
		return brokerOutcome{}, err
	}
	slog.Info("brokered agent credential through host-side injector",
		"event", "sandbox.broker.active", "sandbox", st.Name, "upstream", bc.UpstreamURL, "credential", cred.EnvVar)
	return brokerOutcome{NetworkMode: reach.RequiredNetworkMode, InjectorEndpoint: endpoint}, nil
}

// applyWorkdirTrust records the container working directory as trusted in an
// agent's config file (Definition.WorkdirTrust), for CLIs that block on a
// folder-trust prompt before running in a directory (Codex, DF85). It runs on
// every launch, independent of brokering, patching the host-side config file in
// the sandbox's agent-runtime dir (bind-mounted into the sandbox) before the
// container starts. A no-op for agents that declare no WorkdirTrust or that have
// no workdir. Because config.toml is re-seeded from the host each launch, this
// re-applies every time.
func applyWorkdirTrust(st *state.State) error {
	if st.Agent == nil || st.Agent.WorkdirTrust == nil || st.Workdir == nil {
		return nil
	}
	wt := st.Agent.WorkdirTrust
	path := store.AgentRuntimeFilePath(st.SandboxDir, wt.RelPath)
	current, err := os.ReadFile(path) //nolint:gosec // path from the sandbox dir + agent-declared RelPath
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read workdir-trust file %s: %w", wt.RelPath, err)
	}
	patched, err := wt.Patch(current, WorkdirMountPath(st.Workdir))
	if err != nil {
		return fmt.Errorf("patch workdir-trust file %s: %w", wt.RelPath, err)
	}
	if err := fileutil.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("create dir for workdir-trust file %s: %w", wt.RelPath, err)
	}
	if err := fileutil.WriteFile(path, patched, 0600); err != nil {
		return fmt.Errorf("write workdir-trust file %s: %w", wt.RelPath, err)
	}
	return nil
}

// applyDirectCredential materializes an agent's real credential into its auth
// file (Definition.DirectCredentialFile) for the non-brokered delivery path, for
// a CLI that reads its credential only from a file (Codex → auth.json; a bare env
// var doesn't authenticate it — DF84). It writes only when the credential is
// still present in secretEnv: a brokered launch removed it above (and wrote a
// placeholder auth.json), so this is a no-op then; a direct launch (--no-broker,
// unsupported backend, --network-none) still has it. A no-op for agents that
// declare no DirectCredentialFile, or when no credential is present at all.
func applyDirectCredential(st *state.State, secretEnv map[string]string) error {
	if st.Agent == nil || st.Agent.DirectCredentialFile == nil {
		return nil
	}
	dc := st.Agent.DirectCredentialFile
	var secret string
	for _, ev := range dc.EnvVars {
		if v := secretEnv[ev]; v != "" {
			secret = v
			break
		}
	}
	if secret == "" {
		return nil // no credential, or it was brokered away
	}
	data, err := dc.Render(secret)
	if err != nil {
		return fmt.Errorf("render direct-credential file %s: %w", dc.RelPath, err)
	}
	path := store.AgentRuntimeFilePath(st.SandboxDir, dc.RelPath)
	if err := fileutil.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("create dir for direct-credential file %s: %w", dc.RelPath, err)
	}
	if err := fileutil.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write direct-credential file %s: %w", dc.RelPath, err)
	}
	return nil
}

// patchBrokerConfigFiles applies a file-based redirect/placeholder delivery
// (BrokerConfig.ConfigFiles) for agents whose CLI reads its base URL and/or
// credential only from config files (Codex: config.toml for the base URL,
// auth.json for the key). For each file it reads the current bytes from the
// sandbox's agent-runtime dir (empty when none was seeded), lets the agent's
// Patch merge in the injector endpoint and per-sandbox placeholder, and writes it
// back before the container starts. A no-op for env-redirected agents (empty
// ConfigFiles). The files are host-side and bind-mounted into the sandbox, so the
// patch is visible to the agent at launch; the reconcile path rebinds the same
// port, so the persisted values stay valid without a rewrite.
func patchBrokerConfigFiles(sandboxDir string, bc *agent.BrokerConfig, endpoint, placeholderToken string) error {
	for _, cf := range bc.ConfigFiles {
		path := store.AgentRuntimeFilePath(sandboxDir, cf.RelPath)
		current, err := os.ReadFile(path) //nolint:gosec // path from the sandbox dir + agent-declared RelPath
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read broker config file %s: %w", cf.RelPath, err)
		}
		patched, err := cf.Patch(current, "http://"+endpoint, placeholderToken)
		if err != nil {
			return fmt.Errorf("patch broker config file %s: %w", cf.RelPath, err)
		}
		// The agent-runtime dir normally exists from seed-copy, but a brokered agent
		// with no seeded config (no host config.toml, and API-key auth so no
		// auth.json) may have none yet — create it so the file can be written.
		if err := fileutil.MkdirAll(filepath.Dir(path), 0750); err != nil {
			return fmt.Errorf("create dir for broker config file %s: %w", cf.RelPath, err)
		}
		if err := fileutil.WriteFile(path, patched, 0600); err != nil {
			return fmt.Errorf("write broker config file %s: %w", cf.RelPath, err)
		}
	}
	return nil
}

// resolveBrokerReach resolves the injector reachability and applies the
// network-mode gates for a brokered launch. ok==false with err==nil means
// brokering can't proceed and auto falls back to direct delivery; a non-nil err
// is the forced-on (--broker) refusal of that same situation. It refuses two
// cases: a backend that can't host an injector, and --network-isolated on a
// backend that needs its own network mode (rootless podman → slirp) which can't
// compose with the isolation bridge + in-sandbox iptables yet.
func resolveBrokerReach(ctx context.Context, rt runtime.Backend, st *state.State) (runtime.InjectorReach, bool, error) {
	reach, ok, err := resolveInjectorReach(ctx, rt)
	if err != nil {
		return runtime.InjectorReach{}, false, fmt.Errorf("resolve injector reachability: %w", err)
	}
	if !ok {
		if st.BrokerCredentials {
			return runtime.InjectorReach{}, false, fmt.Errorf("--broker requested but the %s backend cannot host a sandbox-reachable injector", rt.Descriptor().Type)
		}
		return runtime.InjectorReach{}, false, nil
	}
	if st.NetworkMode == "isolated" && reach.RequiredNetworkMode != "" {
		if st.BrokerCredentials {
			return runtime.InjectorReach{}, false, fmt.Errorf("--broker is not yet supported with --network-isolated on the %s backend: it needs a dedicated network mode (%s) the isolation allowlist can't compose with yet", rt.Descriptor().Type, reach.RequiredNetworkMode)
		}
		return runtime.InjectorReach{}, false, nil
	}
	return reach, true, nil
}

// resolveInjectorReach asks the backend for its network-level injector endpoint.
// ok is false (with a nil error) when the backend can't host a sandbox-reachable
// injector — either it isn't InjectorReachable at all, or it returns
// ErrInjectorUnsupported (e.g. rootless podman, pending its slirp path) — and the
// caller falls back to direct delivery (or errors under a forced --broker). A
// non-nil error is a genuine reachability failure and is fatal, so the real key
// is never silently delivered when brokering was expected to work.
func resolveInjectorReach(ctx context.Context, rt runtime.Backend) (runtime.InjectorReach, bool, error) {
	reachable, ok := runtime.InjectorReachOf(rt)
	if !ok {
		return runtime.InjectorReach{}, false, nil
	}
	reach, err := reachable.InjectorReach(ctx)
	if errors.Is(err, runtime.ErrInjectorUnsupported) {
		return runtime.InjectorReach{}, false, nil
	}
	if err != nil {
		return runtime.InjectorReach{}, false, err
	}
	return reach, true, nil
}

// buildInjectorSpec assembles the injector spec from an agent's BrokerConfig, the
// selected credential, the backend's reachability, and the resolved real value.
// Shared by the launch-time broker step and ReconcileInjector so the two never
// drift. The agent's placeholder-carrier header (bc.PlaceholderHeader) is
// stripped and searched for the expected token; the selected credential is then
// injected into its own header (x-api-key or Authorization: Bearer for Claude,
// x-goog-api-key for Gemini, Authorization: Bearer for Codex).
func buildInjectorSpec(sandboxDir string, bc *agent.BrokerConfig, cred agent.BrokerCredential, reach runtime.InjectorReach, realKey, placeholderToken string) broker.InjectorSpec {
	return broker.InjectorSpec{
		SandboxDir:    sandboxDir,
		BindHost:      reach.BindHost,
		UpstreamURL:   bc.UpstreamURL,
		StripHeaders:  []string{bc.PlaceholderHeader},
		ExpectedToken: placeholderToken,
		Bindings: []broker.BindingConfig{{
			Destination: bc.Destination,
			Kind:        broker.KindHeaderSet,
			Header:      cred.Header,
			Prefix:      cred.Prefix,
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
	cred, realKey, ok := bc.SelectCredential(secretEnv)
	if !ok {
		slog.Warn("cannot reconcile credential injector: no brokerable credential in the current environment",
			"event", "sandbox.broker.reconcile.nokey", "sandbox", name)
		return nil
	}

	reach, ok, err := resolveInjectorReach(ctx, d.Runtime)
	if err != nil {
		return fmt.Errorf("reconcile injector: resolve reachability: %w", err)
	}
	if !ok {
		return nil // backend can't host an injector (shouldn't happen if it was brokered)
	}

	placeholderToken, err := broker.PlaceholderToken(sandboxDir)
	if err != nil {
		return fmt.Errorf("reconcile injector: placeholder token: %w", err)
	}
	if _, err := broker.NewSidecarHost().Ensure(ctx, buildInjectorSpec(sandboxDir, bc, cred, reach, realKey, placeholderToken)); err != nil {
		return fmt.Errorf("reconcile injector: respawn: %w", err)
	}
	slog.Info("respawned dead credential injector",
		"event", "sandbox.broker.reconcile.active", "sandbox", name)
	return nil
}

// applyBrokerEnv rewrites secretEnv in place for a brokered launch: it drops
// every brokerable credential (so neither the selected one nor an unselected
// sibling — e.g. an OAuth token present alongside the brokered API key — leaks
// into the container), points the agent at the injector via base_url (DialHost +
// the injector's port), and sets the placeholder token. injectorAddr is the
// injector's bound address (BindHost:port); only its port is used, since the
// agent reaches the injector via DialHost (which may differ from BindHost, e.g.
// Docker Desktop). It returns the agent-facing endpoint (DialHost:port) — the
// destination to allowlist under network isolation. It is a pure function so the
// env rewrite is unit-tested.
func applyBrokerEnv(secretEnv map[string]string, bc *agent.BrokerConfig, reach runtime.InjectorReach, injectorAddr, placeholderToken string) (string, error) {
	_, port, err := net.SplitHostPort(injectorAddr)
	if err != nil {
		return "", fmt.Errorf("parse injector address %q: %w", injectorAddr, err)
	}
	for _, c := range bc.Credentials {
		delete(secretEnv, c.EnvVar)
	}
	endpoint := net.JoinHostPort(reach.DialHost, port)
	// Env-redirected agents (Claude, Gemini) get their base URL via an env var;
	// file-redirected agents (Codex) have BaseURLEnvVar=="" and are redirected by
	// patchBrokerConfigFiles instead.
	if bc.BaseURLEnvVar != "" {
		secretEnv[bc.BaseURLEnvVar] = "http://" + endpoint
	}
	// The placeholder the agent presents is the per-sandbox token the injector
	// verifies (not the shared bc.DummyToken constant) — see broker.PlaceholderToken.
	// AuthTokenEnvVar is empty for agents that receive the placeholder via a config
	// file instead (Codex → auth.json), so only set it when named.
	if bc.AuthTokenEnvVar != "" {
		secretEnv[bc.AuthTokenEnvVar] = placeholderToken
	}
	return endpoint, nil
}

// startLegacy is the original bring-up path for backends without
// runtime.ProcessLauncher: create the instance, start it, and wait for the
// entrypoint (which runs sandbox-setup.py inline) to consume secrets.
// No keepalive_only patch; the agent is welded into the entrypoint as before.
func startLegacy(ctx context.Context, rt runtime.Backend, st *state.State, cname string, instanceCfg runtime.InstanceConfig, markerPath string, hasSecrets bool) error {
	if err := createWithImageRecovery(ctx, rt, st, instanceCfg); err != nil {
		return err
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
	configPath := store.RuntimeConfigFilePath(sandboxDir)
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
// bro carries the broker's network-mode override (rootless podman → slirp; only
// set on open networking) and the injector endpoint, which is published to the
// container as YOLOAI_BROKER_INJECTOR_ENDPOINT so the entrypoint can allowlist it
// under isolation. The isolation-mode gating below keys on st.NetworkMode (the
// user-facing isolation), which the override never changes.
// baseChecksum is the lineage of the image this instance is about to be created
// from, stamped onto the instance so it survives the tag being re-pointed.
func buildInstanceConfig(desc runtime.BackendDescriptor, st *state.State, mnts []runtime.MountSpec, ports []runtime.PortMapping, bro brokerOutcome, sidecarFirewall bool, baseChecksum string) (runtime.InstanceConfig, error) {
	cname := store.InstanceName(st.Layout.Principal, st.Name)
	caps := desc.Capabilities
	if err := validateInstanceNetwork(desc, st); err != nil {
		return runtime.InstanceConfig{}, err
	}

	// The container's effective network mode is the user's, unless brokering needs
	// a specific one (rootless podman's slirp host-loopback). The override is only
	// set on open networking, so it never changes an isolated/none sandbox.
	networkMode := st.NetworkMode
	if bro.NetworkMode != "" {
		networkMode = bro.NetworkMode
	}

	// C.UTF-8 is always present without locale-gen; without it apps like Claude Code render ASCII-only.
	containerEnv := []string{"LANG=C.UTF-8"}
	// Publish the injector endpoint so the entrypoint can allowlist it under
	// network isolation (the agent's LLM egress collapses to the injector). Only
	// consumed when network_isolated; harmless on open networking.
	if bro.InjectorEndpoint != "" {
		containerEnv = append(containerEnv, "YOLOAI_BROKER_INJECTOR_ENDPOINT="+bro.InjectorEndpoint)
	}
	// Under the sidecar-firewall path the allowlist is installed out-of-container by
	// a netns-sharing sidecar (the container is denied NET_ADMIN below). Tell the
	// entrypoint to skip its own in-container firewall install — without NET_ADMIN
	// its iptables calls would fail and abort the boot.
	if sidecarFirewall {
		containerEnv = append(containerEnv, "YOLOAI_FIREWALL_EXTERNAL=1")
	}

	instanceCfg := runtime.InstanceConfig{
		Name:         cname,
		Hostname:     config.SanitizeHostname(st.Name),
		ImageRef:     st.ImageRef,
		WorkingDir:   WorkdirMountPath(st.Workdir),
		Mounts:       mnts,
		Ports:        ports,
		NetworkMode:  networkMode,
		DNS:          slices.Clone(st.DNS),
		UseInit:      true,
		Labels:       instanceLabels(st.Layout.Principal, st.Name, baseChecksum),
		ContainerEnv: containerEnv,
	}

	if err := applyResourceLimits(st, &instanceCfg); err != nil {
		return runtime.InstanceConfig{}, err
	}

	// The agent container gets NET_ADMIN to install its own in-container firewall —
	// EXCEPT under the sidecar-firewall path, where the firewall is installed from a
	// netns-sharing sidecar and the agent is deliberately denied NET_ADMIN so it
	// cannot flush the rules (tamper-resistant isolation).
	if st.NetworkMode == "isolated" && caps.NetworkIsolation && !sidecarFirewall {
		instanceCfg.CapAdd = append(instanceCfg.CapAdd, "NET_ADMIN")
	}

	if err := applyCaps(st, caps, &instanceCfg, desc.Type); err != nil {
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

// validateInstanceNetwork repeats creation policy at the runtime boundary so
// lifecycle and internal callers cannot launch an unsupported combination.
func validateInstanceNetwork(desc runtime.BackendDescriptor, st *state.State) error {
	caps := desc.Capabilities
	if desc.RejectsNetworkNone() && st.NetworkMode == "none" {
		return yoerrors.NewUsageError("Apple Container backend does not support --network-none")
	}
	if st.NetworkMode == "isolated" {
		// Whether the allowlist can actually be enforced is a netpolicy decision:
		// it composes the backend's capability with the isolation mode's in-sandbox
		// iptables honoring (gVisor refuses). See netpolicy.CanEnforce.
		if ok, reason := netpolicy.CanEnforce(netpolicy.StrategyIPFilter, caps, desc.Type, st.Isolation); !ok {
			return errors.New(reason)
		}
	}
	if len(st.DNS) > 0 && (!caps.CustomDNS || st.NetworkMode == "none") {
		return yoerrors.NewUsageError("custom DNS is not supported by this network/backend combination")
	}
	return nil
}

// instanceLabels builds the runtime instance labels recording sandbox identity
// and the owning principal. Both are always set: every principal is non-empty
// (D126), so there is no default to elide and no unlabelled instance for a sweep
// to have to guess about (runtime.IsOrphanCandidate, D62).
//
// baseChecksum records which yoloai-base the instance's image descends from, so
// a sandbox that is relaunched rather than recreated can still be judged against
// the running binary (DF156). It is stamped here, at the one moment the answer is
// known for certain: the image has just been made current and the tag has not yet
// had a chance to move. Omitted when empty, because the absence of the label is
// itself meaningful — callers read it as "out of date", and writing "" would
// assert a lineage of nothing.
//
// On docker this is belt and braces: the daemon folds an image's labels into its
// containers' at create, so the value would arrive inherited. containerd and
// apple do not, so there this stamp is the only source.
func instanceLabels(principal config.PrincipalSegment, name, baseChecksum string) map[string]string {
	labels := map[string]string{
		runtime.LabelSandbox:   name,
		runtime.LabelPrincipal: string(principal),
	}
	if baseChecksum != "" {
		labels[runtime.BaseChecksumLabel] = baseChecksum
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

// applyCaps validates and applies capability requirements to the instance config.
func applyCaps(st *state.State, caps runtime.BackendCaps, instanceCfg *runtime.InstanceConfig, runtimeName runtime.BackendType) error {
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

// isMissingImageErr reports whether err is a Create failure caused by imageRef
// being absent from this backend's image store.
//
// It requires the message to name this exact image ref AND carry a not-found
// marker, because "not found" alone appears in unrelated failures. The docker
// marker is verified; the containerd and apple forms are informed guesses at CLI
// text this host cannot exercise, and a miss is harmless — the caller falls back
// to the behaviour it had before this classifier existed.
func isMissingImageErr(imageRef string, err error) bool {
	if err == nil || imageRef == "" || !strings.Contains(err.Error(), imageRef) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "No such image") || // docker (verified)
		strings.Contains(msg, "failed to resolve reference") || // containerd
		strings.Contains(msg, "manifest unknown") ||
		strings.Contains(msg, "pull access denied") ||
		strings.Contains(msg, "image not found")
}

// rebuildProfileImage is the profile-chain rebuild used to recover from a
// missing image. Variable for testing (same seam idiom as docker's
// dockerInfoOutput); production always calls profiles.EnsureProfileImage.
var rebuildProfileImage = profiles.EnsureProfileImage

// createWithImageRecovery creates the instance and, when that fails because the
// profile image is not in this backend's store, rebuilds it and retries exactly
// once (DF154).
//
// Recovery rather than a pre-flight check is deliberate. Whether the image
// exists cannot be established in advance — any such check is true when it runs
// and the image is used later — so the sound shape is to attempt the operation
// and handle the failure. This is that handler.
//
// Scoped to sandboxes with a profile. A missing *base* image is a different
// path: `Setup` decides base staleness with a checksum stamped on the image
// itself, so an absent base is already detected rather than assumed away.
//
// The rebuild is forced, which also revalidates the base image. That is usually
// what is wanted, not waste: whatever removed the profile image — a prune, a
// backend switch — commonly removed the base with it.
//
// Exactly one retry, and only when the classifier fires. A rebuild loop on a
// genuinely broken Dockerfile has to fail rather than spin, and both failure
// paths below say a rebuild was already attempted, because "it failed twice for
// the same reason" is a materially different diagnosis from the first failure.
func createWithImageRecovery(ctx context.Context, rt runtime.Backend, st *state.State, instanceCfg runtime.InstanceConfig) error {
	err := gvisorStartHint(st.Isolation, rt.Create(ctx, instanceCfg))
	if err == nil {
		return nil
	}
	if st.Profile == "" || !isMissingImageErr(instanceCfg.ImageRef, err) {
		return missingImageHint(instanceCfg.ImageRef, st.Profile, err)
	}

	out := outputOr(st.Output)
	fmt.Fprintf(out, "Profile image %s is recorded as built but missing from this backend's store; rebuilding it...\n", //nolint:errcheck // best-effort progress
		instanceCfg.ImageRef)

	if buildErr := rebuildProfileImage(ctx, rt, st.Layout, st.Profile,
		profiles.AutoBuildSecrets(st.Layout.HomeDir), out, slog.Default(), true); buildErr != nil {
		return fmt.Errorf("%w\n\nThis was an automatic rebuild of %q, triggered because creating "+
			"the instance failed with: %v", buildErr, instanceCfg.ImageRef, err)
	}

	if retryErr := gvisorStartHint(st.Isolation, rt.Create(ctx, instanceCfg)); retryErr != nil {
		return fmt.Errorf("%w\n\n%s was rebuilt automatically after the first attempt reported it "+
			"missing, and creating the instance still failed", retryErr, instanceCfg.ImageRef)
	}
	return nil
}

// missingImageHint names the real cause when instance creation fails because the
// image is not in this backend's store (DF154).
//
// Staleness is decided entirely by a checksum marker in the profile directory —
// nothing verifies the image still exists, and nothing can: any such check is
// check-then-act, and the image can be pruned between the check and this call.
// So the marker means "a build once ran", not "the image is here", and when the
// two disagree the backend is asked to create from a tag it does not have. It
// then does the only thing it can with an unqualified tag and tries a registry,
// producing a pull or manifest error for an image that was never in a registry —
// a message that sends the reader to their network and their credentials, which
// are both fine.
//
// The remedy is the one the user cannot guess: rebuild. `--rebuild` and
// `yoloai system build` both bypass the marker, so recovery is one command; it
// is knowing which command that is missing.
//
// Detection requires the message to name this exact image ref AND carry a
// not-found marker, because "not found" alone appears in unrelated failures.
// The docker marker is verified; the containerd/apple forms are informed
// guesses at CLI text this host cannot exercise, and a miss is harmless — the
// error passes through exactly as it does today.
func missingImageHint(imageRef, profile string, err error) error {
	if !isMissingImageErr(imageRef, err) {
		return err
	}

	rebuild := "yoloai system build"
	if profile != "" {
		rebuild = "yoloai system build " + profile
	}
	return fmt.Errorf("%w\n\nThe image %q is recorded as built but is not in this backend's "+
		"image store, so creating the instance fell through to a registry pull — it was never "+
		"in a registry. This happens when the image is removed (a manual delete, a prune, or a "+
		"backend switch) while the build record in the profile directory survives, since that "+
		"record tracks the Dockerfile's checksum and not the image's existence. Rebuild it with "+
		"%q, or re-create the sandbox with --rebuild", err, imageRef, rebuild)
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
	if tail := readLogTail(store.SandboxJSONLPath(st.SandboxDir), 20); tail != "" {
		parts = append(parts, tail)
	} else if tail := readLogTail(store.AgentLogPath(st.SandboxDir), 20); tail != "" {
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
			fmt.Fprintf(output, WarningPrefix+"skipping port %d:%d — host port %d is already in use\n", //nolint:errcheck // best-effort output
				p.HostPort, p.ContainerPort, p.HostPort)
			continue
		}
		_ = l.Close()
		available = append(available, p)
	}
	return available
}

// advisoryWarnings returns one-line warnings for advisory capability checks
// that failed for the given isolation mode (e.g. a below-floor runc/crun
// version — see runtime/caps.HostCapability.Advisory). Advisory failures
// never block launch and never prompt; this is the passive alternative,
// printed alongside filterAvailablePorts's warning in the same style.
func advisoryWarnings(ctx context.Context, rt runtime.Backend, isolation runtime.IsolationMode) []string {
	env := caps.DetectEnvironment()
	results := caps.RunChecks(ctx, runtime.RequiredCapabilitiesFor(rt, isolation), env)
	var warnings []string
	for _, r := range results {
		if r.Err != nil && r.Cap.Advisory {
			warnings = append(warnings, r.Cap.Summary+": "+r.Err.Error())
		}
	}
	return warnings
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

// WorkdirMountPath returns the container working directory path for a directory.
func WorkdirMountPath(d *state.DirSpec) string {
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
