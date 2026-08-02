> **ABOUTME:** Build plan for making the `--network-isolated` firewall tamper-proof against a
> hostile agent with sudo, by installing it from a privileged sidecar outside the agent's netns
> reach. Bridges the best-effort ip-filter and the later hostile-grade SNI proxy.

# Tamper-resistant network isolation (egress containment "step 1.5")

- **Status:** IN-PROGRESS — implemented 2026-06-28 for docker + the agent-free launch path.
  **Per-backend audit added 2026-08-02** (§ Where every backend actually stands); the remaining
  work is gated on [agent-free-launch-everywhere.md](agent-free-launch-everywhere.md)
  (§§1–6 below), validated on real Docker (agent can't flush; non-allowlisted stays blocked;
  injector stays reachable; live `network allow` patches the per-netns ipset from the sidecar).
  Deferred: containerd/Kata, the legacy launch path, macOS (see Scope). One boundary worth
  noting — see "Provisioning egress" below.
- **Depends on:** agent-free-launch-everywhere.md

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

## Where every backend actually stands (researched 2026-08-02)

The first increment shipped for docker. This is the audit the owner asked for — for each backend,
either the allowlist is enforced **outside** the agent's reach, or the mechanism-level reason it is
not. Read from the code, not from this plan's earlier assumptions, and one earlier assumption was
wrong.

| Backend / mode | `--network=isolated` | Enforced where | Gap |
| --- | --- | --- | --- |
| docker, runc | supported | **sidecar, host netns** | none — shipped |
| docker, gVisor | **refused at creation** | n/a | see below — the interesting one |
| podman | supported | **in-guest** `entrypoint.py` | agent-free opt-in + rootless `NET_ADMIN` |
| containerd / Kata | supported | **in-guest** (guest kernel iptables) | needs its own sidecar shape |
| apple | supported | **in-guest** (per-VM kernel) | no shareable host netns |
| tart | **not supported** | n/a | needs a `pf` design |
| seatbelt | **not supported** | n/a | needs a `pf` design |

**docker + gVisor is not a weak firewall — it is no firewall, by refusal.** `netpolicy.CanEnforce`
rejects `--network=isolated` under container-enhanced, because gVisor's userspace netstack ignores
in-sandbox iptables and yoloAI declines to claim enforcement it does not have. That refusal is
correct today and is also the *strongest argument for this plan*: a sidecar installs rules in the
**host netns**, which the Sentry cannot ignore, so finishing this work is what would make network
isolation available on gVisor at all. `runtime/isolation.go` already says so — "the redesign moves
enforcement to the host netns, which removes the dependency on the in-sandbox kernel". The blocker
is the agent-free exclusion, which [DF171](../findings-unresolved.md) shows is unnecessary and
which [agent-free-launch-everywhere.md](agent-free-launch-everywhere.md) removes.

**podman was listed in this plan's first-increment scope and did not ship.** The scope line above
says "docker + podman"; only docker did. Podman inherits `Launch` and `RunNetnsSidecar` by embedding
`*docker.Runtime`, so the gap is the agent-free opt-in plus one genuinely open question this plan
never asked: whether a **rootless** podman sidecar can hold `CAP_NET_ADMIN` over the shared netns at
all. Determine that before scoping it — the answer decides whether podman is a one-field change or
a "cannot, and here is why" row.

**containerd/Kata is the case where the shape genuinely differs, and it may be easier than it
looks.** The guest's iptables are the *guest kernel's*, so the agent can flush them — that is the
tamper hole. But the CNI netns the VM taps into is **host-side and already outside the guest**, so a
sidecar there enforces where the agent cannot reach, by construction. This plan deferred it as
"different shape, verify separately"; the research says the shape is *favourable*, not merely
different. It still needs a `Launch` first (see the enabler plan, item 3).

**apple is the first plausible genuine "cannot".** Each sandbox is a real VM with its own kernel and
no host network namespace to share, so there is no netns for a sidecar to join. Enforcing outside
the guest would mean filtering at the vmnet layer on the host — a different mechanism, not this one.
Do not claim it is impossible until someone has looked at what the `container` framework exposes;
do claim that the sidecar mechanism does not apply.

**tart and seatbelt do not support isolation at all, and that is documented rather than accidental.**
[network-isolation.md](../network-isolation.md) § Out of Scope: "macOS backends (Seatbelt, Tart).
Need a `pf`-based equivalent design." Seatbelt is the sharper of the two — the sandbox is a host
process group sharing the host's network stack, so there is no boundary to filter at without
per-process host firewalling. Both are macOS work and neither can be evaluated from a Linux host.

**What this means for scoping.** Two of the seven rows are cheap and Linux-verifiable (gVisor via
DF171; podman pending one experiment). One is favourable but needs a prerequisite (containerd). Four
are macOS or genuine-mechanism work. A release that claims "outside the environment everywhere"
cannot be honest without the macOS half, so the honest v0.11.0 claim is narrower — see the enabler
plan's "What done means".

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
