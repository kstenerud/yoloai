> **ABOUTME:** One design for reclaiming address-keyed network-enforcement state on both platforms,
> because a stale entry does not merely block a new sandbox — it can hand it another sandbox's
> allowlist. Covers the Linux host-netns point and the macOS `pf` point as one problem.

# Plan: reaping address-keyed enforcement state

- **Status:** PLANNED — designed 2026-08-06 against measured address behaviour on both platforms. No
  production code written. The macOS half was already designed inside
  [macos-pf-privileged-path.md](macos-pf-privileged-path.md); this generalises it and settles the
  Linux half, which was named as a cost and never worked out.
- **Depends on:** [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md)
  (the enforcement points this reclaims), [macos-pf-privileged-path.md](macos-pf-privileged-path.md)
  (D132 — the grant that authorises the macOS mutations)
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

### 1. Clear before claim, at every start

**Before installing a sandbox's own rules, delete its address from every other sandbox's enforcement
state.** Deleting a non-member is a no-op, so this is deterministic regardless of what teardown did
or did not manage. It closes the inherited-allowlist case without depending on any previous shutdown
having succeeded — which matters because the case we most need to survive is the one where nothing
ran at teardown.

This is the single most important rule, because it makes correctness independent of cleanup.

### 2. Reconcile on every run, keyed on *running*, never on *record-exists*

Any address in enforcement state that **no running sandbox holds** is removed. Two traps:

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
- A sweep with a sandbox created concurrently never removes the live sandbox's entry.
- A `sandbox allow` concurrent with a sweep leaves the added entry present.
- Orphan identification is exercised against the ambiguous-name case DF125 describes, and passes
  because it never reads a name.

**Every one of these must pair its negative with a positive control in the same run.** A sandbox with
no network at all satisfies "the old policy no longer applies" for free — that is DF172's vacuity
mode, and it silently invalidated the first run of the `pf` research harness (A22). Assert that a
permitted destination still succeeds, or the test certifies nothing.

## Open questions

1. **What is the Linux per-sandbox unit?** macOS has a slot pool of numbered tables because its grant
   is a static ruleset over fixed table names. Linux has no such constraint — a chain or ipset per
   sandbox is expressible — so the two need not have the same shape, and forcing them to would import
   the slot cap for no reason. Decide before building.
2. **Where does the sweep run?** `system prune` already reaps host artifacts identity-keyed (D114),
   which is the obvious home; but reconciliation has to happen on every *start*, not only when an
   operator prunes. Likely both, sharing one implementation.
3. **Does rule 1 need the address before the sandbox has it?** On Linux the container's address is
   assigned at create, so "clear before claim" needs the address first — which orders address
   discovery before rule installation. Confirm that ordering is available on every backend.

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
