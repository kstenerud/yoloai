> **ABOUTME:** Build plan for making the `--network-isolated` firewall tamper-proof against a
> hostile agent with sudo, by installing it from a privileged sidecar outside the agent's netns
> reach. Bridges the best-effort ip-filter and the later hostile-grade SNI proxy.

# Tamper-resistant network isolation (egress containment "step 1.5")

- **Status:** IN-PROGRESS — implemented 2026-06-28 for docker + the agent-free launch path.
  **Per-backend audit added 2026-08-02** (§ Where every backend actually stands); the remaining
  work is gated on [host-controlled-agent-launch.md](host-controlled-agent-launch.md)
  (§§1–6 below), validated on real Docker (agent can't flush; non-allowlisted stays blocked;
  injector stays reachable; live `network allow` patches the per-netns ipset from the sidecar).
  Deferred: containerd/Kata, the legacy launch path, macOS (see Scope). One boundary worth
  noting — see "Provisioning egress" below.
- **Depends on:** host-controlled-agent-launch.md

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

## The property, stated before any mechanism

**The sandboxed agent cannot disable or evade its own network allowlist.** That is the requirement.
A netns sidecar holding `CAP_NET_ADMIN` the agent lacks is *one* way to get it, and the one that
shipped; backends may satisfy it differently as long as they satisfy it observably.

The realizations available, by where the enforcement point sits relative to the agent:

- **Shared netns, privileged installer** (docker, podman): rules live in the netns the agent's
  container shares, installed by a sidecar with `NET_ADMIN` the agent does not have.
- **Host netns the guest cannot reach at all** (containerd/Kata, gVisor): the guest is a VM or a
  userspace netstack; the enforcement point is on the far side of that boundary by construction.
  This is *stronger* than the sidecar, not weaker.
- **Host packet filter** (macOS backends, if it works): `pf` rules on the host, outside every guest.
  Undesigned — see the research brief below.

**The interface that keeps this safe is the conformance assertion, not the Go type**, exactly as for
[host-controlled-agent-launch.md](host-controlled-agent-launch.md): from inside the guest, attempt to
defeat the allowlist (flush rules, add a permissive one), then verify egress to a non-allowlisted
destination is *still* refused. One assertion, mechanism-blind, with a mandatory stated reason for
any backend that skips it. And it needs a positive control beside it — a permitted destination that
must still succeed — or a sandbox with no network at all passes ([A22](../../agent-failures.md)).

## Where the enforcement point actually has to sit (measured 2026-08-02)

The shipped mechanism installs the allowlist **in the sandbox's own network namespace**, from a
sidecar holding `CAP_NET_ADMIN` the agent lacks. That is correct for a normal runc container and
**silently ineffective for two of the isolation modes yoloAI offers**. Measured on this host, not
reasoned:

| Where the rule is installed | runc container | gVisor (`runsc`) | Kata VM |
| --- | --- | --- | --- |
| Sandbox's own netns (**the shipped sidecar**) | **enforces** | **bypassed** | **bypassed** |
| Host root netns, keyed on the sandbox IP | enforces | **enforces** | **enforces** |

- **gVisor.** A container's netns carrying `-A OUTPUT -p tcp --dport 80 -j DROP` *and* the same rule
  in `FORWARD` still reached `http://example.com`. The Sentry runs its own userspace netstack and
  injects frames on the veth at L2, below netfilter's IP hooks, so rules in that netns never see the
  traffic. A `DOCKER-USER` rule in the **host root netns** matching the container IP blocked it
  immediately.
- **Kata.** Same result by a different mechanism, and one this repo already documented without
  drawing the consequence: `tap0_kata` ↔ `eth0` is bridged by a **TC mirred filter** (DF8), which is
  also L2 and also below netfilter. A `FORWARD` DROP inside the CNI netns did not stop the guest; a
  `FORWARD` DROP in the host root netns keyed on `10.89.0.2` did.
- **Rootless podman.** The sidecar mechanism works exactly as designed, which was the open question:
  a sidecar joined via `--network container:<id>` with `--cap-add NET_ADMIN` installed the rule, the
  target container was **blocked**, and the target **could not flush it** (`iptables -F` refused —
  no `NET_ADMIN`). Rootless is not an obstacle.

**Two consequences worth stating plainly.**

1. **The host-root-netns enforcement point is the stronger mechanism, not a fallback.** The sidecar
   depends on the agent lacking a capability inside a namespace it shares; the host netns is
   somewhere the guest has no route to at all — a different kernel (Kata) or outside the sandbox
   boundary (gVisor). It also needs no sidecar container, which is the machinery this plan built.
2. **It costs what the sidecar was designed to avoid.** Rules in the host's own tables are global
   state keyed on a sandbox IP: they must be reaped when the sandbox dies (including after a crash),
   and an IP reused by a later sandbox inherits whatever was left behind. The sidecar's rules die
   with the netns. That trade — per-sandbox lifetime versus actually working on VM-ish modes — is
   the design decision this plan now has to make, and it may well be "both, chosen per backend".

## Where every backend actually stands (researched 2026-08-02)

The first increment shipped for docker. This is the audit the owner asked for — for each backend,
either the allowlist is enforced **outside** the agent's reach, or the mechanism-level reason it is
not. Read from the code, not from this plan's earlier assumptions, and one earlier assumption was
wrong.

| Backend / mode | `--network=isolated` | Enforced where | Gap |
| --- | --- | --- | --- |
| docker, runc | supported | **sidecar, host netns** | none — shipped |
| docker, gVisor | **refused at creation** | n/a | needs the host-netns point, **not** the sidecar (measured) |
| podman | supported | **in-guest** `entrypoint.py` | agent-free opt-in only — **rootless sidecar verified working** |
| containerd / Kata | supported | **in-guest** (guest kernel iptables) | needs the host-netns point; **its CNI netns is bypassed too** (measured) |
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
which [host-controlled-agent-launch.md](host-controlled-agent-launch.md) removes.

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


## The macOS half — a research brief

tart, apple and seatbelt cannot be evaluated from a Linux host. These are the questions, with the
evidence that would settle them; none should be costed before they are answered on hardware.

0. **Start from the Linux result, because it reframes every question below.** Measured here: the
   sidecar's in-netns rules are *bypassed* by both gVisor and Kata, and the mechanism that works for
   them is filtering in the **host's own** network stack, keyed on the sandbox's address. Every macOS
   backend is VM-or-host-process — i.e. structurally closer to Kata than to a runc container — so the
   question to ask first is **"where does this backend's traffic become visible to the host stack, and
   can `pf` match it there?"** rather than "can we run a sidecar".
1. **apple: is there any enforcement point outside the guest?** Each sandbox is a real VM with its
   own kernel, and there is no host network namespace to share, so the sidecar mechanism does not
   apply. The open question is what the `container` framework exposes at the vmnet layer — whether
   host-side filtering of a specific VM's traffic is possible at all. A negative answer here is a
   legitimate "cannot", and is worth writing down as one.
2. **tart and seatbelt: is `pf` viable, and at what granularity?** `network-isolation.md` § Out of
   Scope has said "need a `pf`-based equivalent design" since the original design and nobody has
   written it. The specific unknowns: can `pf` rules be scoped to one VM's vmnet interface (tart),
   and to one process group rather than the whole host (seatbelt)? Seatbelt is the harder case — the
   sandbox shares the host's network stack, so per-sandbox filtering means per-process filtering.
3. **Does the guest keep the ability to defeat it?** For any mechanism proposed, run the conformance
   assertion above: from inside, try to break out; require the block to hold. That is the only
   evidence that distinguishes "enforced outside" from "enforced somewhere the agent happens not to
   have looked".
4. **If a backend cannot, does it currently claim it can?** tart and seatbelt declare
   `NetworkIsolation: false`, so they refuse honestly today. apple declares `true` and enforces
   in-guest — so if apple turns out to have no out-of-guest option, the honest outcome is a
   documented limitation, not silence.

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
