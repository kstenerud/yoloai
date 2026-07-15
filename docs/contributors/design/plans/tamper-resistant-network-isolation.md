> **ABOUTME:** Build plan for making the `--network-isolated` firewall tamper-proof against a
> hostile agent with sudo, by installing it from a privileged sidecar outside the agent's netns
> reach. Bridges the best-effort ip-filter and the later hostile-grade SNI proxy.

# Tamper-resistant network isolation (egress containment "step 1.5")

- **Status:** IN-PROGRESS — implemented 2026-06-28 for docker + the agent-free launch path
  (§§1–6 below), validated on real Docker (agent can't flush; non-allowlisted stays blocked;
  injector stays reachable; live `network allow` patches the per-netns ipset from the sidecar).
  Deferred: containerd/Kata, the legacy launch path, macOS (see Scope). One boundary worth
  noting — see "Provisioning egress" below.
- **Depends on:** —

## The problem (confirmed)

Under `--network-isolated`, `entrypoint.py` installs the iptables/ipset allowlist **inside the agent
container**, which holds `CAP_NET_ADMIN` (added by `buildInstanceConfig` so the entrypoint can install
rules). The agent runs as `yoloai`, which has `NOPASSWD:ALL` sudo. So the agent escapes isolation in
one line — **empirically confirmed**:

```
# in an isolated sandbox, as the agent:
sudo iptables -F OUTPUT     # FLUSH_OK
curl http://1.1.1.1         # reached=301  (was blocked before the flush)
```

So step 1's containment is best-effort: it leashes an honest agent but not a hostile one.

## The fix (validated)

**Deny the agent container `NET_ADMIN`; install the firewall from an ephemeral privileged sidecar
that shares the agent's network namespace.** The agent's netns then has *no* process holding
`NET_ADMIN`, so root-via-sudo gets `Permission denied` on iptables, and the installer container is
gone and was never reachable from inside the sandbox.

Validated end-to-end on real Docker (2026-06-28):

```
# agent container started WITHOUT --cap-add NET_ADMIN, kept alive
docker run --rm --network "container:$CID" --cap-add NET_ADMIN yoloai-base \
  iptables -A OUTPUT -d 1.1.1.1 -j REJECT          # SIDECAR_INSTALLED_RULE
docker exec $CID  curl http://1.1.1.1               # BLOCKED
docker exec $CID  sudo iptables -F OUTPUT           # FLUSH_DENIED  ← the whole point
```

Without `NET_ADMIN`, even root-in-container cannot run iptables (`Permission denied (you must be
root)` despite uid 0 — it's the missing capability). `NET_RAW` doesn't help an attacker: crafted
packets still traverse the OUTPUT chain. Unsharing a fresh netns needs `CAP_SYS_ADMIN`, which a
plain isolated sandbox also lacks.

## Scope (first increment)

- **Backends:** docker + podman (both support `--network container:<id>` netns sharing). **Agent-free
  launch path** (which docker `--network-isolated` uses — default `container` isolation).
- **Defer:** containerd/Kata (the agent runs in a VM; its netns model is the host-side CNI netns the
  VM taps into — a sidecar there filters host-side, different shape, verify separately); the legacy
  launch path (the agent runs inline in the entrypoint, so it needs a firewall-ready barrier — see
  Ordering); macOS (tart/apple VMs, seatbelt host-process — different again).
- Still an **IP allowlist** — this closes the *tamper* hole only. The DNS-exfil hole (port 53 stays
  open to resolve) and stale-IP-on-re-resolution remain `ip-filter` properties; killing those is
  step 2 (the domain-native SNI-splicing forwarder). Step 1.5 is "the same allowlist, enforced
  outside the agent's reach" — the enforcement-point primitive step 2 builds on.

## Design

### 1. Agent container loses `NET_ADMIN`
`buildInstanceConfig` (`internal/orchestrator/launch/launch.go`) currently adds `NET_ADMIN` when
`st.NetworkMode == "isolated" && caps.NetworkIsolation`. Remove that for backends using the sidecar
path; the sidecar gets the cap instead. (Keep the in-container path + cap for any backend NOT on the
sidecar path, gated by a capability — see §5.)

### 2. Firewall installer becomes a standalone script
Extract `isolate_network`'s rule logic (`entrypoint.py:195`) into a standalone installer the sidecar
runs (e.g. `runtime/docker/resources/install-firewall.py`). Inputs come from the **host** (not the
agent's `runtime-config.json`, which the sidecar doesn't mount): the allowlist domains, and the
injector endpoint (`YOLOAI_BROKER_INJECTOR_ENDPOINT` from step 1) — passed as env/args at sidecar
launch. The script: resolve domains → ipset, allow loopback/established/DNS/allowlist/injector,
default-REJECT. Same load-bearing failure semantics: if any rule fails, the sidecar exits non-zero
and the launch **fails** (never run the agent with no firewall).

### 3. New runtime operation: run an ephemeral netns-sharing sidecar
A backend capability — e.g. `RunNetnsSidecar(ctx, target, image, argv, env, capAdd)` — implemented
for docker/podman via `ContainerCreate{HostConfig: {NetworkMode: "container:"+targetID, CapAdd:
["NET_ADMIN"]}}` → Start → Wait → (auto-`--rm`). Returns the exit code/logs so a failed install
fails the launch. (Podman inherits docker's impl; both support the network mode.)

### 4. Ordering — install before the agent runs
- **Agent-free path:** Start (netns exists) → `waitForReady` → **run firewall sidecar, wait for
  success** → broker (already here) → Launch the agent. The agent never runs until the firewall is
  up. Natural fit — slot the sidecar step into `startViaLaunch` after `waitForReady`.
- **Legacy path (deferred):** the agent runs inline in the entrypoint, so the entrypoint must block
  on a "firewall ready" marker the host writes after the sidecar succeeds. Out of scope for the
  first increment.

### 5. Keep the in-container path as a gated fallback
Don't break backends that can't run the sidecar. Add a capability (e.g.
`BackendCaps.NetnsSidecar` or reuse the agent-free gate) that selects: sidecar-installed
(tamper-proof) vs entrypoint-installed (today's best-effort). `entrypoint.py`'s `isolate_network`
stays for the fallback; under the sidecar path the entrypoint does **not** install the firewall
(and the container has no `NET_ADMIN` to do so anyway).

### 6. Live patch (`allow`/`deny`) re-runs the sidecar
`LivePatchNetwork` today execs `dig`+`ipset` **inside** the agent container — impossible once the
container lacks `NET_ADMIN`/can't `ipset`. Under the sidecar path, a live `allow`/`deny` launches a
fresh netns-sharing sidecar that adds/removes IPs in the (per-netns) ipset. This is the
strategy-dispatch seam netpolicy.md predicted ("the *mutation transport* is per-strategy"): the
policy data is unchanged; only the apply transport moves out-of-container.

## Open questions to resolve during build

- **ipset netns scoping — CONFIRMED per-netns.** The live-patch sidecar adds an IP to
  `allowed-domains` and reads it back, operating on the same set the launch sidecar created in the
  agent's netns (`TestIntegration_NetworkIsolation_LivePatchViaSidecar`), and the agent (its own
  netns view, no NET_ADMIN) can't touch it — so the set is per-netns and there's no cross-sandbox
  collision. Per-sandbox naming (`yoloai-<name>`) is unnecessary on this kernel.
- **Runtime interface shape** for the netns sidecar (new method vs. generalizing an existing
  container-run path). Keep it minimal and docker/podman-only at first.
- **Image for the sidecar** = `yoloai-base` (has iptables/ipset/python3). No new image.
- Whether step 1.5 is a new `netpolicy.Strategy` value (`ip-filter-sidecar`) or a property of the
  existing `ip-filter` enforcement point. Likely the latter (same rules, hardened point) — decide
  when wiring `CanEnforce`/dispatch.
- **Restart/reconcile:** the firewall lives in the netns and dies with the container; on restart the
  sidecar re-runs (like the injector reconcile). Ensure the start path always (re)installs.

## Acceptance test (the load-bearing one)

An integration test that asserts the agent **cannot** flush the firewall: in a brokered+isolated
sandbox, `exec sudo iptables -F OUTPUT` is denied (or the rule survives), a non-allowlisted
destination stays REJECTed afterward, and the injector stays reachable. This is the regression guard
for the whole feature — the existing `TestIntegration_CredentialBroker_Isolated` proves reachability
+ blocking; step 1.5 adds the **tamper-resistance** assertion.

## Provisioning egress (a deliberate boundary)

On the agent-free path the entrypoint runs `run_setup_commands` (and overlay mounts, UID
remap) and writes `.substrate-ready` BEFORE the host runs the firewall sidecar — so under
the sidecar path, profile **setup commands run with full network**, not the isolation
allowlist. This mirrors the Dockerfile build, which also has unrestricted network: provisioning
is trusted, user-authored config. The thing isolation contains — the AI agent — launches only
AFTER the firewall is up, so the agent is fully contained. Closing this (setup commands under
the firewall too) needs a host-written "firewall ready" barrier the entrypoint blocks on
before setup, which is the same barrier the deferred legacy path needs; left for that increment.
(The in-container fallback path — non-sidecar backends — still installs the firewall before
setup commands, unchanged.)

## Relationship to the workstream

Refines egress-containment **step 1** (`broker × --network-isolated`, shipped). Precedes
**step 2** (`StrategyEgressProxy`: default-deny netns + host-side SNI-splicing forwarder, domain-
native, kills DNS-exfil). Step 1.5 is the "enforcement outside the agent's reach" primitive for the
IP-allowlist; step 2 swaps the IP allowlist for an L7 proxy on the same out-of-reach principle. See
[egress-proxy-build.md](egress-proxy-build.md) and [netpolicy.md](../netpolicy.md) ("Hostile
containment").
