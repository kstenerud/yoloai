> **ABOUTME:** The "start here" build brief for host-side per-sandbox egress enforcement (layer 3).
> The design is settled in `enforcement-state-reaping.md` and D132; this is the actionable plan and
> deliberately not a place to re-litigate either.

# Host-side enforcement — build brief

- **Status:** PLANNED — no production code. The mechanism is measured on both platforms; what
  remains is building it, plus the spikes named per part below.
- **Depends on:** enforcement-state-reaping.md, host-controlled-agent-launch.md, macos-pf-privileged-path.md, tamper-resistant-network-isolation.md

  Which is which: `enforcement-state-reaping.md` holds the design and the measurements,
  `host-controlled-agent-launch.md` is the pre-agent hook and the root of the whole chain,
  `macos-pf-privileged-path.md` is the macOS privileged path, and
  `tamper-resistant-network-isolation.md` is the in-guest layer this sits above rather than replaces.
- **Rides:** **any.** It strengthens what `--network-isolated` already promises without changing its
  spelling, and adds no user-visible name. **Two things would change that** and are called out where
  they arise: refusing a sandbox that previously ran is newly-rejected input (breaking, rule 1), and
  any persisted record that lands in the schema-versioned library is a migration (D131, and it opens
  a release branch the moment it is known).

## How this document works

**It points; it does not track.** Every part below names the record that owns its status — a finding,
a decision, or a plan. There is no status column here, on purpose: `post-merge-roadmap.md` has one,
it is *known to lag* (DF103), and its own Status line tells readers to trust the plan rather than the
table. Repeating that mistake in a second program document would be a choice, not an accident.

**Work discovered mid-build goes in § Discovered**, as a line pointing at a record that owns it —
never as a status, and never as a bullet that lives only here. This is the one convention the repo did
not already have: rule 7 covers defects you decide not to fix, rule 8 covers ideas worth building, and
neither covers *a necessary sub-part of the thing you are currently building, found while building it*.
The rule is the same one `next-release.md` applies to itself, for the same reason.

**When you jump, write it down first.** If a part is abandoned mid-flight for something more urgent,
add the line to `next-release.md` § *In flight* before starting the new thing. The items are not what
gets lost — the stack is.

**Every spike below opens a verification round, and rounds have rules now**
([D136](../../decisions/working-notes.md), [`procedures/verification-rounds.md`](../../procedures/verification-rounds.md)).
This is not process for its own sake — it is the direct remedy for the thrash that produced the
design this brief builds from, where 28 of ~29 invalidated runs were the rig rather than the world.
Three things bind before a spike starts:

- **Read the prior art first.** It gates opening the round. The last pass spent three days measuring
  its way to a correction that was already written down.
- **Write a queue file** naming the items, what each decides, and its cost — before the first run.
  The round closes when every item has run or been explicitly dropped, in writing. **The plan is not
  edited until then**; intermediate optima are working state.
- **Harnesses use `scripts/research_harness_v2.py`**, whose mandatory invariant is that a probe must
  be shown reporting failure — baselined with the mechanism absent, answering the other way — before
  any expectation may rest on it.

## What is already settled, so nobody re-derives it

Three decisions, and the evidence behind each is in `enforcement-state-reaping.md`. **Do not rebuild
from the older draft**: it keyed on the guest's IP address and two audits found it wrong.

1. **Key on the host-side interface, never the guest's address.** On Linux the chosen form is a
   **netdev ingress chain bound to the sandbox's own veth** — measured to key per-sandbox with
   `br_netfilter` unloaded (r10), to hold a set-based allowlist with live revocation (r12), to revoke
   a transfer already in flight (r14), and to work unprivileged inside rootless podman's netns (r13).
   On macOS it is the bridge, via a **pinned superset with one slot per bridge index** (D132, G5).
2. **No `ct state established,related accept` fast-path.** It is the direct cause of two bugs this
   workstream found. A netdev chain sits before conntrack, so on Linux this is structural rather than
   a rule to remember.
3. **A rule's identity is the sandbox ID; the interface name is only its match.**

**Two properties that follow and are easy to lose.** Deny with `reject`, not `drop` — measured at
0.06 s against 5.09 s, and a black hole is what makes a severed agent fail confusingly. And a netdev
chain **outlives its device as a stale-but-inert object**: it does not capture a successor (r11, r13),
but it enforces nothing, and reading the ruleset cannot tell you.

## Build order

Each part names what it needs, and what would make it done. **A part is not done because its code
merged; it is done when the thing under *exit* is true.**

### Part 0 — the pre-agent hook

`host-controlled-agent-launch.md` owns this, and it depends on nothing, which is why it is first. It
is also the root of the macOS chain, so it pays three times.

- **Needs:** code. No spike — the plan is researched against the code already.
- **Why it gates everything:** enforcement must be installed *before the agent runs*, and reinstalled
  when a sandbox's device is recreated. Both rootless podman (netns torn down with its last sandbox)
  and the stale-but-inert netdev chain require it, independently.
- **Exit:** a sandbox that stops and starts has enforcement in place before its agent's first packet,
  asserted by a test that fails when the reinstall is removed.

### Part 1 — Linux enforcement, docker first

- **Needs:** code. The mechanism is measured; no spike outstanding for docker.
- **Shape:** one netdev table per sandbox, chain bound to its veth, a named set as the allowlist, a
  gateway accept, a DNS accept, and `reject` as the default for IPv4. ARP and IPv6 fall through the
  chain policy deliberately — dropping ARP costs the sandbox its network and looks like containment.
- **Exit:** two sandboxes on one bridge get independent policy in the same run; the denied
  destination is refused with the deny counter incrementing; revoking a set element stops an
  in-flight transfer. A rate of zero with a zero counter is a free negative and does not count.

### Part 2 — detection, before any response policy

- **Needs:** code, plus one spike (below).
- **Why it is this early:** a `required` sandbox that cannot tell it has been trampled is just a
  best-effort sandbox with a stronger label. Everything in part 5 rests on this.
- **Shape:** host-side only. Compare the chain's counters against the veth's `rx_packets`
  (`/sys/class/net/<veth>/statistics/`), plus `NFNLGRP_NFTABLES` for flush and delete events. **No
  in-guest probe** — DF192 is the finding that says why, and the guest is what we do not trust.
- **Spike:** the idle-sandbox case. `rule 0 + veth rx 0` is indistinguishable from a healthy sandbox
  sending nothing, so the detector must report UNKNOWN there rather than healthy. That is the exact
  free-negative the macOS canary already shipped once; it needs measuring, not reasoning.
- **Exit:** an induced inert rule is reported as inert, and an idle sandbox is *not* reported as
  broken, in the same run.

### Part 3 — reaping and reconcile

- **Needs:** code. The design is settled; the open question is where the record lives.
- **Decision owed on day one:** the reconcile record is intended to stay **outside** the
  schema-versioned library — host-side state rebuildable by reconciling against live sandboxes. If a
  spike shows it must be schema-versioned, that is a migration, and D131 says cut `release-vX.Y.Z`
  and move this work there **before** writing the persistence.
- **Shape:** the record is removed *before* the rules it describes, never after (netavark's file-lock
  race, which they hit). Startup diffs records against live sandboxes and tears down the difference.
  Never delete on one identifier alone — Calico shipped an address-keyed reaper that deleted a live
  workload's conntrack entries.
- **Exit:** a sandbox destroyed without clean teardown leaves no rule a later sandbox can inherit,
  asserted by reconciling and then checking a fresh sandbox gets only its own allowlist.

### Part 4 — the other Linux backends

- **Needs:** code. Both mechanisms are measured (r9 for CNI's veths, r13 for rootless podman).
- **containerd:** one shared `yoloai0`, so the per-network bridge key does not apply; the netdev
  chain does. **rootless podman:** the chain goes in the rootless netns via `podman unshare`, needs
  no sudo, and dies with the last sandbox — so part 0's reinstall is mandatory here, not optional.
- **Exit:** the part 1 exit criteria, on each backend, in `runtime/runtimetest` conformance form
  rather than as a fake — rule 10's warning about capability-guarded tests applies directly.

### Part 5 — degrade and disclose

**Decided 2026-08-11: [D135](../../decisions/working-notes.md). yoloAI never refuses a sandbox for
lack of enforcement capability. It degrades to the strongest layer available and says which one that
is.** The reasoning was already in the tree: `design/network-isolation.md` § *Threat Model* ranks the
**errant** agent as primary and says that for it *"any filter that says 'no' to the right destinations
is sufficient"* — which the in-guest layer meets. Refusing to start because the *secondary* defense is
missing withdraws a working defense against the primary threat.

- **Needs:** code, plus the message text. No spike.
- **Disclosure is the mandatory half**, for the reason the same section gives: *"The rules must not
  have silent partial-application modes. A rogue agent benefits from any case where the user believes
  isolation is enforced but it isn't."*
- **Three tiers, and the message must distinguish them** — they are different guarantees, not
  different verbosity:

  | Tier | What it is | What it stops |
  | --- | --- | --- |
  | in-guest only | `iptables`+`ipset` inside the sandbox | an errant agent; **an agent that gains root can flush it** |
  | in-guest, tamper-resistant | installed from a privileged sidecar, agent holds no `CAP_NET_ADMIN` | the agent cannot flush its own rules |
  | host-side | rules outside the sandbox, keyed on the host interface | measured not defeasible by a guest with `CAP_NET_ADMIN` |

- **Where it must appear:** at create, and in the sandbox's ongoing status — not only at create,
  because the tier can drop at runtime (trampling, drift) and a create-time line has scrolled away
  by then.
- **Enforcement is fixed at creation and only ever moves down** ([D135 amendment](../../decisions/working-notes.md)).
  Never-refuse governs what a sandbox can be *given* at first create; it does not govern
  *withdrawing* what this sandbox already had. Every start applies exactly the recorded set — a host
  that could give more does not give more, because a placement upgrade is invisible while a coverage
  upgrade (IPv6) blocks traffic that flowed yesterday, and never upgrading deletes that distinction
  rather than requiring it be judged correctly for every axis ever added. Stronger protection means
  **a new sandbox**, which is a fair answer in a tool whose first line of self-description is
  *disposable sandboxes*.
- **The one question the user is asked:** the system can no longer deliver what this sandbox was
  created with — is it still acceptable to run it with less? **Reboot is the ordinary path into
  this**, since elevation is not sticky, so the check belongs on every start rather than on an
  explicit restart.
- **The check is one-directional and collects everything.** *Is anything recorded now missing?* —
  a subset test needing no notion of "better". It gathers the full set before refusing and never
  fails fast, because a truncated list makes the user fix one thing to discover the next.
- **`--allow-weaker-enforcement`** starts anyway, prints the same list, and rewrites the record
  downward **after a successful start**, never before. Printing on both paths is what keeps the flag
  safe with no state: nothing has to remember what the user was previously shown. Name chosen over
  `--allow-downgrade` because `system migrate` already tells users to *downgrade*, meaning run an
  older release.
- **The recorded tier must live in the host tier.** `netpolicy.json` already does. A tier recorded
  anywhere guest-writable would let a hostile agent downgrade its own sandbox by editing the file —
  DF193's class, and the reason this is a placement requirement rather than a detail.
- **On Linux, no user elevation is needed for docker** (`r15-unelevated-install.txt`): a helper
  container in the host netns with **`NET_ADMIN` alone** — not `--privileged` — installs the chain,
  and enforcement survives the helper being destroyed because the chain lives in the host namespace.
  **Say which privilege is being borrowed rather than implying none is:** it is the docker daemon's,
  reached through the user's docker-group membership, which is already root-equivalent on any host.
  rootless podman needs nothing at all (r13); containerd already runs yoloAI as root for CNI.
- **Runtime response stays sever, not kill.** Measured (r7): host-destined traffic never enters the
  forward hook, so a brokered agent keeps its API path and sees only that some destinations now
  refuse. Deny with `reject` (0.06 s vs 5.09 s). **On macOS this does not hold** — the rules sit on
  the bridge and block both directions, so the gateway is inside the enforced surface and must be
  allowlisted explicitly.
- **Deferred, not rejected:** an opt-in strict mode that fails rather than degrades, for CI and
  shared hosts. Nobody has asked; adding it later is additive; and whenever it arrives, refusing a
  sandbox that previously ran is newly-rejected input and therefore breaking.
- **Exit:** a sandbox that cannot get host-side enforcement still runs, still filters, and its status
  names the tier — asserted by a test that fails when the disclosure is removed, not merely when the
  enforcement is.

### Part 6 — macOS

- **Needs:** `macos-pf-privileged-path.md` first, and it is blocked behind part 0.
- **Two things this brief adds to that plan:** the pool must be inverted to one slot per bridge index
  or interface keying does not fit D132's grant at all (G4/G5); and the grant should be measured for
  a `-vvs rules` widening in the same matrix run, which would retire the guest-dependent canary
  (DF192).
- **Exit:** owned by that plan.

## Riding along — small, independent, worth the same release

None of these depends on the parts above, and each has a record that owns it. Each needs a `Rides:`
field before it is staged.

- **DF190's workaround** — delete the network when its last sandbox goes. An Apple-side defect with a
  yoloAI-side remedy; unscoped, so size unknown.
- **DF189** — move the CNI subnet off `10.89.0.0/16`, which is byte-identical to podman's default
  allocator pool. `10.0.0.0`–`10.87.255.255` is outside every podman and docker local pool.
- **DF188** — `resolve_domains` accepts sinkholed answers, installing a rule that matches nothing.
- **DF193** — the guest can pre-create the on-create-done marker. Its own sweep (the other
  read-write-tier files the host reads) is the larger half.

## Discovered

*Work found while building, each pointing at the record that owns it. Nothing else — no status.*

*Nothing yet.*

## Out of scope, deliberately

- **tart.** Not a missing answer: each VM does get its own `vmenetN`, and macOS pf cannot key on a
  bridge member — a rule on the member blocked nothing while the same rule on the bridge blocked both
  VMs. Its own `--net-softnet-allow`/`-block` lists are the only surface it has, and nobody has
  tested them.
- **seatbelt.** Parked in `seatbelt-host-pf-enforcement.md`.
- **The in-guest layer.** `tamper-resistant-network-isolation.md` owns it and this does not replace
  it.
- **Making a blocked agent recover gracefully.** A stalled or refused connection is the agent's
  problem to handle; if "the agent just reconnects" needs to be true, that is separate work and it
  applies to either shape.

## Known-unmeasured, carried forward

Named here because a build brief that reads as though everything is settled is the failure this
workstream keeps producing. Fuller lists live in each results file's own bounding section.

- **Cost.** Linux allowlist size 1/1000/10000 is *not separable* by the proxy used — that is not the
  same as equal. macOS is unpriced at any size, and its pinned superset makes the question bigger.
- **UDP**, on both platforms, throughout. DNS rides on it.
- **IPv6.** Deliberately let past the chain policy on Linux; owned by `ipv6-network-isolation.md`.
- **A hostile guest against any of this.** Every probe in the corpus is a cooperative `curl`.
- **Concurrency.** One or two sandboxes, sequential, on an idle host, everywhere.
