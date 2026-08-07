> **ABOUTME:** One design for reclaiming address-keyed network-enforcement state on both platforms,
> because a stale entry does not merely block a new sandbox — it can hand it another sandbox's
> allowlist. Covers the Linux host-netns point and the macOS `pf` point as one problem.

# Plan: reaping address-keyed enforcement state

- **Status:** PLANNED — designed 2026-08-06 against measured address behaviour on both platforms. No
  production code written. The macOS half was already designed inside
  [macos-pf-privileged-path.md](macos-pf-privileged-path.md); this generalises it and settles the
  Linux half, which was named as a cost and never worked out.
- **Depends on:** tamper-resistant-network-isolation.md, macos-pf-privileged-path.md
- **Rides:** **any.** It adds reclamation to a mechanism that does not ship yet; nothing user-visible
  is withdrawn. It is not optional *within* that mechanism — see "Why this is not a tidy-up".

## Why this is not a tidy-up

Moving enforcement out of the guest replaces per-sandbox state that dies with the sandbox
(in-container `iptables`, a per-netns ipset) with **global host state keyed on an address**. Global
state outlives the thing it describes. That is the entire cost of the move, and it is worth paying
only if the state is reclaimed.

**A stale entry does not merely block — it grants reach the sandbox was never configured for.**
Measured on macOS: with a stale entry naming its address in another slot, a sandbox **lost its own
allowlist and inherited the stale slot's** (`pf-assumptions.txt` D3, with a control establishing it
had exactly its own policy immediately before). `quick` is first-match over rules keyed on a recycled
address. This is a property of address-keyed enforcement, not of any particular shape: an orphaned
*sub-anchor* does the identical thing (`pf-stale-a.txt` SA2).

So the failure mode is silent and in the fail-open direction. A sandbox that is blocked complains; a
sandbox that quietly holds a wider allowlist than it asked for does not.

## Address recycling is the trigger, and it is routine on both platforms

The hazard needs a recycled address. Both platforms supply one, for different reasons and on
different timescales — which is why one design has to accommodate both allocators rather than assume
either.

**Linux/docker recycles immediately, by construction.** Measured 2026-08-06 on this host: three
sequential create→destroy cycles each produced `172.17.0.2`. Holding `.2` and `.3`, freeing `.2`, and
creating again returned `.2` — docker allocates the **lowest free address**, so a freed address is
the *next* one handed out. Recycling is not a long-lived-host edge case here; it is the default first
case. Destroy a sandbox without reclaiming its rules and the very next sandbox inherits them.

**macOS recycles on pool wrap, which a long-lived host reaches and then never leaves.** Addresses
advance per *start* (apple `.22`→`.23`, tart `.2`→`.4` with a fresh MAC), because `tart run`
regenerates the MAC itself on every start and vmnet's DHCP sees a new host each time — so leases burn
per start and `/var/db/dhcpd_leases` grows monotonically. On the test host that pool is **already
exhausted**: 253 records covering `192.168.65.2`–`.254`, zero free, and the file survives reboots so
it does not heal. Past that point every start *necessarily* recycles. The two backends do not share a
pool — apple allocates from its own store, sequentially, appearing to restart from the low end after a
host reboot — so neither backend's behaviour may be generalised to the other
(`lease-binding.txt` L2, `restart-control.txt`, and `backend-idiosyncrasies.md`).

**Consequence for the design.** Nothing may depend on an address being fresh, on an allocator's
ordering, or on a stored address still being the sandbox's. Only *live* state is trustworthy.

## The invariant

> At any moment, an address appears in yoloAI's enforcement state **only if** a currently-running
> sandbox holds that address, and the policy attached to it is that sandbox's own.

Two clauses, and the second is the one a naive reaper misses: removing orphans is not sufficient if
a live address can carry someone else's policy.

## Mechanism

Four rules. The first three are already settled for macOS and carry over unchanged; the fourth is
what the Linux point needs and macOS gets for free.

### 0. What "address" means, and how one sandbox ends up under another's policy

**The address is the sandbox guest's own IP** — `172.17.0.2` on docker, `192.168.64.22` on apple. It
is what the enforcement rule matches as the *source*: "traffic **from** this address is subject to
this policy." So the address is the **join key** between a sandbox and its allowlist, and it is the
only key available, because it is the only thing the packet carries.

Nothing ever writes sandbox A's address into sandbox B's state. The hazard runs the other way, and
it needs no mistake by anyone:

1. Sandbox A holds `192.168.64.22`. Its address sits in slot 0's `src` table, paired by a static rule
   with slot 0's `dst` table — A's allowlist.
2. A dies without teardown. **Its entry stays.** Slot 0 still says "`.22` is subject to A's policy".
3. Sandbox B starts and is handed `.22` — recycled, which on docker is immediate and on macOS is
   inevitable once the lease pool wraps. B is assigned slot 1 and its address is written to slot 1's
   `src` table, correctly.
4. `.22` is now in **both** tables. `quick` is first-match, so slot 0 matches B's traffic first and
   **B runs under A's allowlist**, not its own.

No component behaved incorrectly. The join key was reused while a stale row still referenced it.

### 1. Clear before claim, at every start

**Before installing a sandbox's own rules, delete *this one address* from every table that could
hold it.**

This is not an inspection of other sandboxes, and an earlier draft of this section described it in a
way that read like one. It is a blind delete of a single address across a bounded, fixed set of
tables — 32 on macOS, one chain scan on Linux — without caring whose the address used to be. Deleting
a non-member is a no-op.

**Why blind rather than targeted:** hitting only the right table means knowing which slot the
address's *previous* holder occupied. That is precisely the stored state established above as
untrustworthy — a sandbox that crashed may never have recorded anything, and a record that exists may
name a slot that has since been reused. The address is trustworthy; nothing that claims to describe
it is.

This is the single most important rule, because it makes correctness independent of whether cleanup
ever ran — and the case most needing to survive is the one where nothing ran at teardown.

### 1b. Empty a reused container before populating it — the other half of the same hazard

Rule 1 scrubs the **address**. It does nothing about the **policy container** the address is being
attached to, and a reused container carries the last occupant's contents. *Found by auditing this
plan on 2026-08-07; the first draft stated the invariant's second clause and delivered only the
first.*

The case: sandbox A occupies slot 0 with allowed destinations {X, Y}. A dies. Slot 0 is later
assigned to sandbox B, whose allowlist is {Z}. `pfctl -T add` **adds** — it does not replace — so
`yb_dst_0` becomes {X, Y, Z} and **B silently reaches X and Y**. That is D3's widening again,
arriving through *slot* reuse rather than *address* reuse, and rule 1 cannot see it because B's
address is entirely correct.

Nothing clears it incidentally: table contents **survive a ruleset reload** (`pf-assumptions.txt`
D2 — `src_17` intact across a reload, enforcement continuing), which is a property the design wants
for other reasons and which here means a repair or pool resize will not save us.

**So a reused container is emptied, never added to.** `pfctl -T flush` on the slot's `dst` table
before populating it — already authorised by D132's grant, so no change to the security boundary.

The flush leaves a window in which the table is empty, and that window is safe in the right
direction: **an empty allowlist fails closed** (`pf-assumptions.txt` D4 — empty `dst`, both allowed
and denied destinations unreachable). Flush-then-populate therefore passes through "no egress",
never through "all egress".

Stated generally, because the Linux unit is not chosen: **any container that can be reused must be
emptied as part of claiming it.** A design that allocates a fresh container per sandbox and never
reuses one satisfies this for free — which is a point in favour of that shape on Linux, where
nothing forces a fixed pool.

### 1c. The two clears are orthogonal, and acquisition needs both

They are easy to confuse and neither substitutes for the other:

- **Rule 1 is cross-slot.** It protects against *our address* being stale in **someone else's** slot.
  Flushing the slot we are claiming does nothing about it — that is D3 exactly: B legitimately holds
  slot 1 while its address sits stale in slot 0.
- **Rule 1b is within-slot.** It protects against *this slot's* leftovers. Scrubbing our address
  everywhere does nothing about it, because the leftovers are someone else's destinations, not our
  address.

**A slot holds exactly one sandbox**, so its `src` table should hold exactly one address. Flush both
of the slot's tables rather than only `dst` — that makes the slot's post-condition trivially the
invariant instead of something argued about, and it removes stale addresses that would otherwise be
subject to a policy which is *about to change meaning* under them.

**Acquisition sequence, in order — the order is load-bearing:**

1. `-T flush` the claimed slot's `src` **and** `dst`. The slot is now empty; an empty allowlist
   fails closed (D4), so nothing is reachable through it during the rest of the sequence.
2. `-T delete <our address>` from **every other** slot's `src`. Cross-slot scrub.
3. `-T add <our address>` to the claimed slot's `src`.
4. `-T add <destinations>` to the claimed slot's `dst`.

Steps 2 and 3 must not be reordered: scrubbing after claiming deletes our own entry, and the sandbox
comes up matching no slot at all. That failure is silent in the dangerous direction — no `src` match
means no rule matches, and traffic falls through to whatever the main ruleset does.

**The cost this implies, unmeasured.** sudoers matches one table per invocation, so step 2 is one
`sudo pfctl` call per slot — 31 calls at a 32-slot pool — on top of the flushes and adds, for roughly
35 per sandbox start. Multiple *addresses* fit in one call (D5) but multiple *tables* do not. Nobody
has measured what that costs on the start path. If it is material, the fix is not to skip the scrub
but to reduce the pool to the number of slots actually wanted, since the scrub is O(pool), not
O(sandboxes).

### 2. Reconcile — for capacity and hygiene, *not* for security

Rule 1 is what closes the inheritance hazard, and it closes it completely: an address is scrubbed
immediately before it is claimed, so no sandbox can start under someone else's policy no matter what
was left behind. **The sweep is therefore not on the security path.** What it does is reclaim what
rule 1 leaves behind — entries for addresses nobody has claimed yet — which matters for two
non-security reasons: macOS slots are a finite pool (32), and Linux state would otherwise grow
without bound.

That distinction is worth being firm about, because it sets where the sweep has to run. It does
**not** have to run on every start; it can be lazy — at `system prune`, and on slot exhaustion, which
is the moment its absence first costs anything. A sweep that runs rarely also contends rarely, which
is most of why rule 4 is cheap.

When it does run: any address in enforcement state that **no running sandbox holds** is removed. Two
traps:

- **Identify orphans by address, never by name.** `runtime/orphan.go` documents why: `yoloai-acme-probe`
  is both principal `acme`'s sandbox `probe` and a legacy sandbox named `acme-probe` (DF19/DF115/DF125).
  Any design that names orphans re-creates that finding. Addresses are unambiguous.
- **A stopped-but-not-destroyed sandbox keeps its record**, so a "record gone" predicate never reaps
  its entry — and that entry is exactly the one a recycled address will collide with, because the
  address was released the moment the sandbox stopped.

### 3. Lock-free, by ordering

The sweep holds no lock. Two ordering rules make it safe without one:

- **Write the sandbox record before its enforcement membership.**
- **In the sweep, enumerate membership before reading records.**

Then an address seen in a table had its record written earlier, so a live sandbox is never swept; and
a sandbox created mid-sweep is not in the enumeration at all. Per-sandbox `flock`s already exist
(`store.AcquireLock`, taken in create/start/stop/destroy/reset) and close same-sandbox races. Slot or
chain allocation is cross-sandbox and needs its own lock.

### 4. Live mutation must not race the sweep

`sandbox <name> allow` mutates enforcement state for a *running* sandbox, and after the move it does
so directly on host state rather than by exec'ing into the guest. So a reaper deciding "this address
is orphaned" can interleave with a patch adding an entry for it.

Rule 3's ordering does not cover this: it protects a sandbox's *first* membership write, not a later
one. The mutation and the sweep must therefore agree on the same per-sandbox lock — which is the lock
that already exists and that the file-exchange path was found not to take (DF182). Take it in the
live-patch path, and have the sweep take it per candidate before removing.

**The soft-fail contract survives the move and improves.** `netpolicy.json` on disk stays the source
of truth and a live patch stays best-effort, queued for next start if it cannot be applied. Today it
soft-fails whenever the guest is unreachable; on host state there is no guest to be unreachable, so
the queued-for-next-start path becomes rare rather than routine.

## Reboot is a clean slate on both platforms, and that is load-bearing

Neither platform persists this state, and yoloAI must not make it persist:

- **Linux:** verified on this host — no `netfilter-persistent`/`iptables-persistent` unit and no
  `/etc/iptables/rules.v4`, so rules and ipsets are lost at reboot. Critically, yoloAI containers
  carry **no restart policy** (`grep` for `RestartPolicy` in `runtime/docker` returns nothing), so a
  reboot stops sandboxes rather than resurrecting them unfiltered. Enforcement and sandbox die
  together, which is the fail-*closed* direction.
- **macOS:** anchors do not survive reboot either, which is why D132 authorises restore from one
  pinned root-owned file (`/etc/yoloai/pf-pool.conf`) rather than from arbitrary input.

**The hazard this rules out** is a persistence feature added later for convenience: rules that
survive a reboot while sandboxes do not would come back attached to addresses nothing holds, and the
first sandbox to be handed one of those addresses inherits a policy from before the reboot. If
persistence is ever wanted, it has to persist the *reconciliation*, not the rules.

## What "done" means

- Destroying a sandbox without a clean teardown, then creating another, gives the new one **its own**
  allowlist — asserted by reaching a destination the old policy allowed and the new one does not.
  **Run it twice, for the two independent paths:** once where the new sandbox inherits the old
  *address* (rule 1), and once where it inherits the old *slot* (rule 1b). One test cannot cover
  both, and the second is the one the first draft of this plan would have passed.
- A sweep with a sandbox created concurrently never removes the live sandbox's entry.
- A `sandbox allow` concurrent with a sweep leaves the added entry present.
- Orphan identification is exercised against the ambiguous-name case DF125 describes, and passes
  because it never reads a name.

**Every one of these must pair its negative with a positive control in the same run.** A sandbox with
no network at all satisfies "the old policy no longer applies" for free — that is DF172's vacuity
mode, and it silently invalidated the first run of the `pf` research harness (A22). Assert that a
permitted destination still succeeds, or the test certifies nothing.

## The Linux half, measured on hardware (2026-08-07)

Everything about slots, tables and sudo-call counts above is **macOS-only**. Linux has the same
*hazard* — address-keyed state that outlives its sandbox — and a materially different shape. Measured
on this host rather than reasoned about:

**`ipset` is not on the host.** It ships *inside* the sandbox image (`Dockerfile` installs
`iptables` and `ipset`), which is why the shipped `firewall.py` can use a single global set name
`allowed-domains` — each netns has its own. The host has `iptables` v1.8.10 on the **nf_tables**
backend, `ip6tables`, and `nft`; `ipset` is **absent**. So moving enforcement host-side either adds
a new host dependency or uses **native nftables sets**, which are already present and need nothing
installed. That is a decision this measurement forces and the macOS side never faced.

It also kills the single global name: in the host root netns every sandbox shares one namespace, so
`allowed-domains` becomes a collision rather than a convenience.

| Property | Measured | Consequence |
| --- | --- | --- |
| nft set name length | ≥64 chars accepted | No naming cliff. Sandbox names cap at 56, so a per-sandbox set name fits — unlike the seatbelt socket path, where three bytes of tier decided it (DF169) |
| Delete a set while a rule references it | **Refused**, `Device or resource busy` | Teardown is *ordered*: remove the rule, then the set. Reversed it fails, and a swallowed failure leaks the set |
| Set survives its rule being removed | **Yes** | The orphan on Linux is the **whole container**, not an entry in it |
| Empty set | **Fails closed** — destination unreachable, and reachable again once added, so the block was real and not a dead netns | Flush-then-populate is safe here too, same direction as macOS D4 |

**Where the two platforms genuinely diverge, and it is not cosmetic:**

- **No fixed pool.** Nothing on Linux forces reusable containers, so a **fresh set per sandbox**
  is expressible — which satisfies rule 1b *by construction* rather than by remembering to flush.
- **No capacity cap**, so no user-visible limit and no `doctor` slot reporting. The macOS 32-slot
  ceiling is a consequence of D132's static ruleset, not a property of the problem.
- **The leak is unbounded rather than fixed.** macOS leaks *entries* inside 32 permanent tables;
  Linux leaks *sets*, one per sandbox that ever ran, forever. So the sweep is more valuable here even
  though it is still not the security mechanism — and it has a distinct job: destroy orphaned sets,
  in rule-then-set order.
- **Rule 1 has a Linux analogue but a cheaper one.** The scrub becomes "remove any pre-existing rule
  matching our source address" — O(rules), not O(pool), with no per-table `sudo` multiplier because
  the host process is already privileged. The ~35-invocation cost is a macOS artifact of sudoers
  matching one table per call.
- **`nft -f` applies a whole ruleset file atomically.** The macOS acquisition sequence needs its
  steps ordered because each `sudo pfctl` is a separate transaction with an observable gap; on Linux
  the equivalent can be **one atomic transaction**, which removes that hazard rather than managing
  it. Worth exploiting deliberately.

### D3 reproduces on Linux — measured end to end, 2026-08-07

Not inferred from rule-order semantics. Host-side nft rules were installed by hand (the shipped
firewall is still in-container, so there is no host state to go stale yet), against real containers
on the default bridge:

1. **A** at `172.17.0.2`, allowlist `{1.1.1.1}`. Reached `1.1.1.1`, blocked from `8.8.8.8` — the
   mechanism enforces, both directions.
2. A destroyed, its rules deliberately left behind.
3. **B** created, handed `172.17.0.2`, **no policy of its own**: blocked from `8.8.8.8` and
   **reached `1.1.1.1`** — it inherited a dead sandbox's allowlist, and the reach doubles as the
   control proving B had a working network.
4. **C** created on the same address **with its own correct policy** (`allow 8.8.8.8`) appended after
   the stale rules: **blocked from `8.8.8.8`** — the destination it was configured for — and
   **reached `1.1.1.1`**, which was never its. The stale `drop` at handle 4 shadowed C's own accept
   at handle 6.

C lost its own policy *and* gained A's, which is D3's result exactly. The hazard is not
platform-specific and the Linux mechanism is not more forgiving.

### Distro fragmentation, and the hazard it actually creates

The backend layer has largely converged — modern distros ship `iptables` as an nf_tables front-end
(this host: `iptables v1.8.10 (nf_tables)`, with `iptables-legacy` still present as an alternative).
A custom `inet` table coexists with docker's own chains without interference; the whole experiment
above ran alongside them.

**The fragmentation that matters is the firewall *manager*, and the hazard is a full ruleset flush.**
Verified on this host: `/etc/nftables.conf` begins with

```
flush ruleset
```

which is the Debian/Ubuntu convention. So `systemctl restart nftables` **destroys every table,
including ours** — and it is an ordinary administrative action with no relationship to yoloAI.
`nftables.service` happens to be disabled here and `ufw` is enabled, but neither fact is portable:
firewalld (RHEL/Fedora/SUSE) has an analogous complete-reload, and a host may run any of them.

**This is D6's fail-open mode with a concrete Linux trigger.** D6 measured on macOS that with the
ruleset flushed and the table still populated, a sandbox reaches a denied destination — *"membership
without rules is unenforced, and nothing distinguishes it from working isolation. VERIFY must check
the RULES."* On Linux the same state is reachable by someone restarting a service.

Two consequences the design must carry:

- **Verification checks the rules, not just membership** — the same conclusion D6 forced, now
  load-bearing on both platforms for different reasons.
- **Enforcement can vanish under a *running* sandbox.** Every other failure in this plan is caught at
  start; this one happens mid-life, silently, in the fail-open direction. Detecting it needs a
  periodic or event-driven check that the rules still exist, closer to tart's net-health probe
  (DF172) than to anything in the start path. **Undesigned** — and the first thing to design after
  this, because a guarantee that can be switched off by an unrelated `systemctl` command is not one.

## Settled by review (2026-08-07)

**The two platforms diverge, and the divergence is part of the model.** macOS uses a slot pool of
numbered tables because D132's grant is a static ruleset over fixed table names — that is a
consequence of the security boundary, not a preference. Linux has no such constraint and gets
whatever unit fits it, unbounded. Neither is made to resemble the other.

The consequence is **user-visible and must be surfaced rather than hidden**: macOS supports a bounded
number of concurrently-isolated sandboxes (32 slots, measured to load as 64 rules), and Linux does
not. So the 33rd concurrent isolated sandbox on macOS fails with an error naming the cap and how to
free a slot, and `doctor` reports slot usage on the backends that have slots. A capability that
exists on one platform and not the other belongs in the model as a difference; papering over it would
mean either an invented cap on Linux or a silent failure on macOS.

**The sweep is capacity and hygiene, not security** — see rule 2. It can be lazy.

**Rule 1's ordering is already available, with room to spare.** It needs the sandbox's address before
installing rules, which orders address discovery ahead of rule installation. That ordering exists:
the container is created (address assigned), then the host installs enforcement, and **only then does
the agent launch** — which is the whole point of
[host-controlled-agent-launch.md](host-controlled-agent-launch.md). The window between address
assignment and rules being up is a window in which the thing isolation exists to contain is not
running.

That slack is already an accepted boundary rather than a new claim: on the agent-free path the
entrypoint runs `run_setup_commands` and writes `.substrate-ready` *before* the firewall goes up, so
profile setup commands deliberately run with full network — provisioning is trusted, user-authored
config, mirroring the Dockerfile build. See
[tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) § *Provisioning egress*.
The requirement is therefore not "rules before the container exists" but **"rules before anything
runs that isolation is meant to contain"**, and the agent-launch gate is exactly that line.

## Open questions

None blocking. The Linux unit's concrete shape (chain per sandbox, ipset per sandbox, or one chain
with per-source rules) is an implementation choice to make against the code, not a design fork — all
three satisfy rules 0–4, and the divergence above means it need not answer to the macOS shape.

## Related

- [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) — the enforcement
  points whose state this reclaims; its § *Where the enforcement point actually has to sit* is why
  they are host-side at all.
- [macos-pf-privileged-path.md](macos-pf-privileged-path.md) / D132 — the macOS grant, which
  authorises exactly the membership mutations this design performs.
- [DF172](../findings-unresolved.md) — the vacuity mode that makes conformance pass for free, and the
  reason to key on the address rather than the interface name.
- [DF182](../findings-unresolved.md) — the existing unlocked mutation path, the same lock this needs.
- [DF179](../findings-unresolved.md) — what the move closes, and why it is worth this cost.
