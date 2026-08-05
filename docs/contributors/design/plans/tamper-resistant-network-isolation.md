> **ABOUTME:** Build plan for making the `--network-isolated` firewall tamper-proof against a
> hostile agent with sudo, by installing it from a privileged sidecar outside the agent's netns
> reach. Bridges the best-effort ip-filter and the later hostile-grade SNI proxy.

# Tamper-resistant network isolation (egress containment "step 1.5")

- **Status:** IN-PROGRESS — implemented 2026-06-28 for docker + the agent-free launch path.
  **Per-backend audit added 2026-08-02** (§ Where every backend actually stands); the remaining
  work is gated on [host-controlled-agent-launch.md](host-controlled-agent-launch.md)
  (§§1–6 below), validated on real Docker (agent can't flush; non-allowlisted stays blocked;
  injector stays reachable; live `network allow` patches the per-netns ipset from the sidecar).
  **macOS half measured on hardware 2026-08-02** (§ The macOS half): host `pf` enforces for all
  three macOS backends, so none of them is a "cannot" — but all three remain **deferred**, because
  each needs a privileged path yoloAI does not have on macOS today. **That privileged path now has
  a decision and a plan of its own** (opt-in `sudo`, not an installed helper):
  [macos-pf-privileged-path.md](macos-pf-privileged-path.md). Deferred: containerd/Kata, the
  legacy launch path, macOS (now measured rather than unknown). One boundary worth noting — see
  "Provisioning egress" below.
- **Depends on:** host-controlled-agent-launch.md
- **Rides:** **any.** Every increment so far is additive — a backend either gains enforcement
  outside the agent's reach or keeps today's behaviour. The breaking half would be flipping a
  backend's advertised `NetworkIsolation`, which nothing here does yet.

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
- **Host packet filter** (macOS backends): `pf` rules on the host, outside every guest. **Measured
  working on all three macOS backends 2026-08-02** — for the two VM backends keyed on the sandbox's
  vmnet address, for seatbelt keyed on the *gid owning the socket*. Undesigned, but no longer
  unknown; see § The macOS half.

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
| apple | supported | **in-guest** (per-VM kernel) | no shareable host netns; **host `pf` works — measured** |
| tart | **not supported** | n/a | **host `pf` works — measured** |
| seatbelt | **not supported** | n/a | **host `pf` per-gid works — measured** |

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

**apple looked like the first plausible genuine "cannot", and is not one.** Each sandbox is a real
VM with its own kernel and no host network namespace to share, so there is genuinely no netns for a
sidecar to join — that half of the claim stands. But filtering at the vmnet layer on the host *does*
work, per-VM, and was measured 2026-08-02 (§ The macOS half). The instruction not to claim
impossibility before looking was the right one, and looking is what overturned it.

**tart and seatbelt do not support isolation at all, and that is documented rather than accidental.**
[network-isolation.md](../network-isolation.md) § Out of Scope: "macOS backends (Seatbelt, Tart).
Need a `pf`-based equivalent design." Seatbelt looked like the sharper of the two — the sandbox is a
host process group sharing the host's network stack, so per-sandbox filtering means per-process
filtering. Measured: that is expressible, because `pf` matches on the gid owning the socket. The
`pf` design these lines have been asking for since the original design is now unblocked on evidence,
though still unwritten.

**What this means for scoping**, counting the seven rows of the table above. Two are cheap and
Linux-verifiable (docker/gVisor via DF171; podman verified). One is favourable but needs a
prerequisite (containerd/Kata). One is already shipped (docker/runc). The three macOS rows are no
longer unknowns — each has a measured mechanism — but none is cheap: all three need a privileged
path yoloAI does not have today (for `pf`, and on seatbelt for the dedicated gid too), plus rule
reaping. A release that claims "outside the environment everywhere" still cannot be honest without
that work, so the honest v0.11.0 claim remains narrower — see the enabler plan's "What done means".


## The macOS half — measured on hardware 2026-08-02

Run on macOS 26.5.1 / arm64, tart 2.32.1, `container` 1.0.0. **There is no "cannot" here.** The
Linux result generalises: the enforcement point that works is the host's own packet filter, keyed
on something the guest cannot change. All three macOS backends have such a key.

**Method, because the first two attempts produced false negatives.** Every case pairs its
block assertion with a positive control (a permitted destination that must still succeed), and the
harness verifies the rules are actually *present in the anchor* before believing a measurement —
[A22](../../agent-failures.md) in its exact form. Two harness generations were discarded for
failing that bar, and both would have read as a clean "pf cannot do this":

1. `pfctl -f` **replaces the main ruleset**, which is where vmnet's NAT lives. The first harness
   flushed it, silently destroying egress for *every* VM on the host, and then measured "blocked".
   `/etc/pf.conf` warns about this in its own header. Reloading `/etc/pf.conf` does **not** put the
   NAT back; only restarting the vmnet service does.
2. The second harness wrote `block drop quick in on <if> …` — `quick` must follow the direction
   keyword, so the rules never parsed and the traffic read as unblockable.

**The harness is retained** at [macos-isolation-spike](../research/macos-isolation-spike/README.md),
which is where the raw runs and the exact invocations live. Re-deriving still means rebuilding the
rig (two macOS VM sandboxes on different backends, a `pf` anchor under `com.apple/`, root), and
every result here is **n=1 run on one host** unless it says otherwise. Treat it as a research
report, not a regression guard — the guard is the `runtimetest` conformance case this plan and the
enabler plan both call for, and it does not exist yet. Two claims *are* re-checkable without
root and were re-confirmed independently: the `/etc/pf.conf` main-ruleset warning, and that
`block drop quick in on …` fails to parse while `block drop in quick on …` parses
(`pfctl -n -f -` needs no privilege).

### Where each backend's traffic meets the host stack

| Backend | Enforcement key | Measured |
| --- | --- | --- |
| apple | source address on the vmnet bridge | blocked; second VM on the same bridge unaffected |
| tart | source address on the vmnet bridge | blocked; host unaffected |
| seatbelt | **gid owning the socket** | blocked; other gids unaffected |

**apple — the sidecar does not apply, but `pf` does.** Each sandbox is a real VM, and its traffic
enters the host on a vmnet bridge (`bridge101`, gateway `192.168.64.1`) with the VM's own address.
`block drop in quick on bridge101 from <vm-ip> to any` blocked the sandbox (`000`) while a **second
apple VM on the same bridge kept working** (`301`) — that second VM is the discriminator that makes
this per-sandbox rather than per-backend. The allowlist shape holds too: pass to one destination,
block the rest, and the sandbox reached the allowed address (`301`) and nothing else (`000`), with
the second VM unaffected throughout.

**tart — same shape, same result.** VM on its vmnet bridge; blocked (`000`) with the host's own
egress unaffected (`301`), and the allowlist shape held (allowed `301`, denied `000`). **The tart tamper is unproven, twice, and should not be cited.** Attempt 1 truncated `pfctl`
output and never established the guest's pf was enforcing. Attempt 2 fixed that — a control address
was reachable at baseline (`301`) and went to `000` once the guest loaded its own pf rule, so the
guest's pf demonstrably worked — but after `sudo pfctl -d` the control address stayed blocked, so
the tamper had no observable effect and the verdict was INCONCLUSIVE rather than a claimed success.
Likely `-E`/`-d` refcounting inside the guest; not chased, because it changes nothing: the property
is established on **apple** (below) against a genuinely privileged guest, and tart's case rests on
that plus the structural argument that the rule lives in a different kernel entirely.

**The tamper property is now demonstrated on apple too, against a genuinely privileged guest.**
This is the strongest evidence the plan has for any backend, and it is worth stating in full
because a weaker version of it was reported twice before it meant anything. Against an
`--network-isolated` apple sandbox whose guest holds `NET_ADMIN`: a host `pf` rule blocked a
destination the guest's *own* allowlist permits; the guest then ran `iptables -F` and set its
policies to `ACCEPT`, which **demonstrably worked** — an address its own firewall had been
blocking became reachable (`000` → `301`) — and in the same breath the host-blocked destination
stayed `000`. So the agent is shown to have destroyed the policy it controls while failing to
touch the one it does not. That pairing, not the bare "still blocked", is the evidence; the two
earlier attempts lacked it and proved nothing (a guest without `NET_ADMIN` cannot tamper, and a
destination its own firewall already blocks cannot show anything about `pf`).

**seatbelt — the hard case, and it is solved, but not by the sandbox profile.** Two separate
findings:

- **SBPL cannot express an IP allowlist.** `(allow network-outbound (remote ip "1.1.1.1:443"))` is
  rejected outright: `sandbox-exec: host must be * or localhost in network address`. CIDR likewise.
  Port scoping *does* work (`*:443` → 443 reached, 80 refused). yoloAI emits a blanket
  `(allow network*)` today (`runtime/seatbelt/profile.go:370`, conditional on
  `networkMode != "none"`), and no finer destination policy is reachable through the profile.
- **`pf` discriminates per-process by credential, which is what seatbelt needs.** With
  `block drop … to <denied> group admin` loaded: gid `admin` was blocked (`000`), gid `staff` was
  unaffected (`301`), and gid `admin` still reached the allowed destination (`301`). The full
  allowlist shape held for one gid, with other gids untouched. So a seatbelt sandbox **launched
  under a dedicated gid** can carry a host-enforced allowlist, and the rule lives on the host,
  outside the sandbox entirely.

**The design was measured end to end 2026-08-02, not just the borrowed-groups version.** A
throwaway group at gid 700 that the user is *not* a member of: with a gid-keyed allowlist loaded,
that gid reached the allowed destination (`301`) and nothing else (`000`), while `staff` was
unaffected (`301`). A process placed in that gid could not leave it — `setgid(20)`, `setegid(20)`,
`setgid(0)`, `setegid(0)` all returned `EPERM`. So the agent cannot shed the credential its policy
is keyed on.

**Three caveats on the gid mechanism, none of them fatal but all of them load-bearing.**

1. **Setting the gid needs privilege.** An unprivileged process cannot move itself into another
   group: `setegid(80)` and `setgid(80)` both return `EPERM`, measured. Only a privileged parent (or
   a setuid-root helper) can launch a child under a dedicated gid, so this needs the *same*
   privileged path as installing the `pf` rules — it is not merely "new machinery".
2. **The gid genuinely cannot be shed once set** — measured against the throwaway gid 700, entered
   by a root parent that calls `setgid(700)`, `setgroups([700])`, then `setuid(501)` (not a
   setuid-root helper). From there `setgid(20)`, `setegid(20)`, `setgid(0)` and `setegid(0)` all
   returned `EPERM`. That is the property the design wants, and it follows from real=effective=saved
   gid all being 700 with euid≠0 — so a launcher that leaves any of those three unset has not
   bought it.
3. **`pf`'s `user`/`group` selectors match TCP and UDP only, so a gid allowlist leaks every other
   IP protocol.** `man 5 pf.conf`: "Only TCP and UDP packets can be associated with users; for other
   protocols these parameters are ignored" — ambiguous between "the rule matches everyone" and "the
   rule matches no one". One experiment discriminates, and it was run: with
   `block ... proto icmp ... group <gid>` loaded, **both** the target gid and an unrelated gid still
   reached the host (n=1). So the rule never matches ICMP at all — the second reading. A seatbelt gid
   allowlist is therefore TCP/UDP-only: tolerable for an IP-allowlist increment, a hole for anything
   claiming hostile-grade containment, and it must be closed another way. The two VM backends have no
   such restriction; their rules key on an address and match all protocols.
   **Now settled with the protocol-agnostic rule and an over-block control** (2026-08-03). With
   `block drop quick inet from any to any group <gid>` — no protocol qualifier, the shape a real
   allowlist would use — TCP to a non-allowlisted destination was blocked (`000`), TCP to the
   allowlisted one still passed (`301`), **an unrelated gid was unaffected (`301`)**, and ICMP to
   the blocked destination still **REACHED**. So `group` is correctly scoped rather than ignored —
   it does not over-block, which was the outcome that would have made the mechanism unusable — and
   the ICMP leak is real. Two earlier attempts at this were circular (the block rule carried
   `proto tcp`, making "ICMP escapes" a restatement of the qualifier).

**SBPL confinement: the sandbox cannot be changed from inside, which is stronger than "cannot be
loosened" — and this is already documented.** [macOS `sandbox-exec` doesn't
nest](../../backend-idiosyncrasies.md#macos-sandbox-exec-doesnt-nest--swift-pm-needs-the-swift-wrapper-sourced)
records it, and it reproduces here: a confined process gets
`sandbox_apply: Operation not permitted`. **Correcting an earlier draft of this section**, which
called it "one-way": it is not one-way. A permissive outer profile with a *tightening* inner profile
is refused with the identical error, so `sandbox_apply` fails on any profile **change**, in either
direction, and succeeds only when the second profile is a semantic no-op. The draft's "positive
control" — permissive outer, permissive inner — passed for that reason, not because loosening is
specially forbidden; it varied two things at once and controlled nothing.

The security consequence is unchanged and good: a seatbelt agent cannot re-sandbox itself to escape.
But there is a second consequence the one-way framing hid — **yoloAI cannot tighten a live seatbelt
sandbox either.** Whatever profile is applied at spawn is final for that process tree, so §6's live
`allow`/`deny` patching has no SBPL analogue on seatbelt. Host `pf` rules, being outside the
sandbox, can still be patched live; that is now the only live-mutation route on this backend.

### Two constraints any implementation inherits

1. **Write into a nested anchor, never the main ruleset.** `pfctl -a com.apple/yoloai_research -f -`
   works and is evaluated (verified by a control rule that blocked the host's own traffic and
   released cleanly). `pfctl -f` would take down Docker Desktop, tart and apple simultaneously, and
   the symptom is "the sandbox has no network" with nothing pointing at pf.
2. **Key rules on the sandbox address, not the interface name.** Not because the two backends
   fight over a bridge — they do not; measured, tart and apple held `bridge101`/`bridge102`
   simultaneously through a restart of each, and an earlier claim of exclusivity in
   [DF172](../findings-unresolved.md) is retracted. The reason is narrower and still holds, and is
   visible within this research's own runs: `bridge101` carried apple's `192.168.64.x` in one run
   and tart's `192.168.65.x` half an hour later. A bridge that is **torn down and re-created**
   (every tart VM start/stop does this) can return on a different subnet, so an interface-keyed
   rule can outlive the sandbox it was written for and attach to unrelated traffic. **The address
   is not a perfect key either** — the reaping result shows an address-keyed rule outliving its
   sandbox, and addresses are re-issued; whatever key is chosen needs an explicit lifetime. DF172 also records the vacuity hazard that makes
   this a test-design problem, not only an implementation one.

### What is still undesigned

Reaping. A `pf` rule keyed on a sandbox address is global host state that outlives the sandbox
if it crashes, and an address reused by a later sandbox inherits it — the same trade this plan
already names for the Linux host-netns point, now confirmed for macOS as well. Also unaddressed:
`pf` needs root, so installing these rules needs a privileged path yoloAI does not currently have
on macOS; and the seatbelt gid mechanism requires allocating and launching under a dedicated gid,
which is new machinery.

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
