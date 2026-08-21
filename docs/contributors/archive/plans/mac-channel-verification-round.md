> **ABOUTME:** Fixed item set for the Mac half of the network-mode reshape — whether `apple` and
> `tart` can carry a host↔guest socket (shape B), what enforcement point each has if they cannot,
> and whether `--network-none` is enforced there at all. Opened under D136.

# Mac channel verification round

- **Status:** IMPLEMENTED — item set fixed 2026-08-13, round closed the same day. Every item ran or
  was dropped in writing; see § *Results*. The synthesis pass it feeds is the owner's and has not
  been taken.
- **Depends on:** ../../design/plans/network-mode-reshape.md, ../../design/plans/egress-proxy-build.md
- **Decides:** [D139](../../decisions/working-notes.md)'s open fork — whether the shape (A) fallback
  needs to exist at all, and therefore whether one mode name covers two different guarantees.

**This file is the round's boundary** (D136 §2). The round closes when every item below has run or
been **explicitly dropped in writing here**. Until close, `network-mode-reshape.md`, D137, D138 and
D139 are **not edited** — one synthesis pass applies the whole fact set. Adding an item mid-round is
normal; adding it *to this file* is what keeps the boundary real.

Every harness uses `scripts/research_harness_v2.py`. Results land in
`../research/mac-channel/results/`, continuously, including invalidated ones.

## The prior-art gate (D136 §1), discharged 2026-08-13

Read before the item set was fixed. It **answers one of the four questions outright** and disqualifies
a large fraction of the corpus the others would have rested on.

### The corpus splits by harness generation, and the split is load-bearing

`research_harness_v2.py` landed **2026-08-11** (`09b697e6`). D136's own count found **28 of ~29
invalidated runs were harness defects**, and v2 exists to make the one class v1 cannot see —
*the probe never demonstrated it could produce the other answer* — structurally impossible. So a
pre-v2 result is not wrong, but it has never been shown to discriminate.

Classifying every result file in the macOS spike by whether it carries v2's control block:

**The question to ask a pre-v2 result is not "is it old" but *was that probe ever shown saying the
opposite?*** If the run does not record that, it supports *"we observed X"* and not *"X is the
case"* — adequate for a census or a cost, fatal for a guarantee. So the split below is a split by
what a file is being asked to carry, not a discard.

| Directory | v2 | pre-v2 |
| --- | --- | --- |
| `macos-isolation-spike/` | `w1`–`w10`, `agent-privilege-reality`, `agent-uid-and-sysctls`, `proc-sys-net-census`, `tamper-persistence`, `ipv6-sidestep`, `ipv6-allowlist-remedy` | every `pf-*` (63), `tart-net-key`, `tart-binding`, `dns-*`, `df1*`, `coherence-*`, `net-ceiling`, `reboot-*` |
| `proxy-chokepoint/` | all of it (P1–P9) | — |
| `linux-enforcement/` | the 7 `v*` files — round **2** | the other 52 (`k*`, `l*`, `c*`) — round **1** |

**The `linux-enforcement/` filename trap, which caught this round's first draft.** There, `v*` names
a round-2 *item*, not a harness version, and those items ran on harness v2; `k*`/`l*`/`c*` are round-1
items on v1. The letter is the item. An earlier version of this table asserted "all of
`linux-enforcement/` is pre-v2" from a truncated listing that never reached the `v*` files.

**One consequence for this round**, and it is narrower than that draft claimed.
`design/plans/macos-pf-privileged-path.md:308` — the row the brief asks me to trust over the requesting agent — is
sourced from `tart-net-key.txt`, which **is** pre-v2, and it is asked to carry a guarantee ("pf blocks
nothing on a bridge member"). It is not a citable refutation on its own.

**What that draft got wrong, recorded because the correction is the useful part.** It also claimed
D139's fail-open leg was pre-v2. It is not: D139 cites "V5's ifindex result" and "V6b showed a
counter cannot distinguish it", and those are `v2-v5-netdev-device-binding.txt` and
`v6b-foreign-chain-shadowing.txt` — **both v2**. D139's argument for (B) over (A) rests on measured
evidence that meets the current bar. Nothing in this round weakens it.

### The question that is already answered

**Q2 has a v2 answer, and it is neither of the two positions the brief contemplates.**
`w8-softnet-enforcement.txt` measures tart's Softnet default-deny form
(`--net-softnet-block=0.0.0.0/0` relaxed by `--net-softnet-allow`) **enforcing**: permitted `301`,
denied `000`, with the permitted address still answering so it is an allowlist and not an outage,
and with `tart exec` riding the Guest Agent's vsock so a block cannot read as a dead VM. Round 2's
synthesis states it plainly: *"tart is not without an enforcement surface… pf cannot reach it, and
it does not need pf."*

So tart is **not** a backend with no enforcement point, and it does **not** fall back to pf. Both the
requesting agent's claim and the prior-art row it deferred to are superseded by a v2 run neither
cited. What remains open is narrower and is C2 below.

### What this leaves

Q1 is untouched by prior art and stays decisive. Q3 has a static reading and no measurement. Q4 has
a documented mechanism (`backend-idiosyncrasies.md`) that has never been probed adversarially.

## Item set

### C1 — Does `apple` expose a host↔guest data channel we can drive? **DECISIVE**

**Decides:** whether `apple` gets shape (B). Together with C2 it decides whether D139's (A) fallback
needs to exist at all — which is the audit's dilemma about one mode name covering two guarantees.

**Probes.** Guest-side: does `AF_VSOCK` exist and can a guest process bind/connect on it. Host-side:
does the `container` CLI or its daemon expose a vsock CID, a socket-attach flag, or any other
arbitrary-data channel to us, and can it be established **before the agent starts**. Bind-mounted
unix socket over virtiofs is measured rather than assumed, per the brief.

**Controls.** `container exec` working is the positive control that *a* channel exists (A22) — the
question is whether it is ours to use. A guest that cannot open `AF_VSOCK` at all must be shown able
to open some other socket family in the same run, or the negative is free.

**Cost:** ~1h.

### C2 — Can a root guest defeat tart's Softnet policy? **DECISIVE (residual)**

**Decides:** whether Softnet is an *out-of-sandbox* enforcement point in the D137 §1 sense, or merely
an unprivileged-agent one. W8 measured that it enforces; it did **not** measure it against an
adversary. Softnet is a host-side userspace process implementing the vmnet datapath, so the expected
answer is that the guest cannot reach it — but that is exactly the shape of reasoning
`agent-privilege-reality.txt` overturned for the in-guest layer, and W8's own *not tried* list names
source pinning as unexercised.

**Probes.** As root in the guest: change the VM's MAC, spoof a source address, add routes, reconfigure
the interface, and re-attempt a denied destination. Positive control beside each: the permitted
address still answers, and the denied one answers when the policy is `--net-softnet-allow=0.0.0.0/0`.

**Also asks:** does `tart` expose vsock to *us* (C1's question on the other backend)? If it does, tart
gets (B) and Softnet becomes defence in depth rather than the mechanism.

**Cost:** ~1.5h.

### C2b — Can pf key on a tart VM? **DROPPABLE**

The brief asks me to verify `tart-net-key.txt`'s claim that a pf rule on a bridge member blocks
nothing while a bridge-wide rule blocks both VMs. It is pre-v2 and so not citable. **But it decides
nothing under C2's result:** if Softnet holds, tart does not need pf; if Softnet falls, pf keyed on a
shared bridge cannot give per-sandbox policy either way, which is the one thing both generations
agree on. **Dropped unless C2 fails**, recorded here rather than silently skipped.

### C3 — Is `--network-none` actually unenforced on `apple` and `tart`? (DF198)

**Decides:** nothing in the design — this is an independent live defect and does not wait for the
fork. If confirmed, shipped help (`internal/cli/helpcmd/help/security.md:74`, *"holds on every
backend"*) and `design/netpolicy.md` (*"a hard boundary on every backend"*) both carry a false
security claim, for the mode the product recommends when the allowlist cannot be trusted.

**Static reading, already done:** `runtime/apple/apple.go:235` mentions `NetworkMode` only in a
comment explaining the `"isolated"` case and never tests `"none"`; `runtime/tart/` contains **zero**
`NetworkMode` references; `runtime/containerd/lifecycle.go:527` admits the same gap in a comment.
`runtime/seatbelt/profile.go:369` and `runtime/docker/docker.go:535` do honour it.

**Probe.** Run **the product's own launch path** (A37 — not a hand-built container): create a sandbox
with `--network-none` on apple and on tart, attempt egress from inside. Positive control: the same
sandbox without the flag reaches the destination, and a seatbelt `--network-none` sandbox is refused.

**Noted in passing, for whoever fixes it:** the existing conformance case
`runtime/runtimetest/conformance.go:165` `NetworkNone` cannot fail — it wraps its only assertion in
`if err == nil`, so an `Exec` error passes it unconditionally. It is also in the **docker-API-only**
suite (`RunConformance`), which apple, tart, seatbelt and containerd do not run — they run
`RunInterfaceConformance`. So no backend that has the gap is covered by the case that would find it.

**Cost:** ~45min.

### C4 — What actually makes shape (B) sound on `seatbelt`?

**Decides:** whether D139's assignment of (B) to seatbelt rests on anything. D139 justifies (B) by the
`CAP_NET_ADMIN` bounding-set drop; **seatbelt has no bounding set**, so the stated reasoning does not
transfer even if the conclusion is right.

**Probe.** From inside a seatbelt sandbox, attempt to escape `(allow network*)`'s absence: `sudo`,
re-exec, a fresh `sandbox_init` with a laxer profile, and spawning via a helper. The mechanism to
name is presumably that a Seatbelt profile is irrevocable and inherited across `exec` — including
across `sudo`, which is the one that matters, since every other backend grants the agent root
deliberately.

**Controls.** The same sandbox with network allowed must reach the destination (or the denial is
free — A22), and the escape attempts must be shown to *run* rather than to fail for an unrelated
reason.

**Cost:** ~1h.

## Traps carried in from the corpus

- `pfctl -f` **replaces the main ruleset** where vmnet's NAT lives, destroying egress for every VM on
  the host; reloading `/etc/pf.conf` does not restore it. Nested anchor only. (Not expected to arise —
  no item here loads pf rules.)
- `block drop quick in on <if>` does not parse; `block drop in quick on <if>` does.
- **A22:** every block assertion needs a positive control beside it, or a sandbox with no network at
  all passes.
- **A37:** if the rig builds its own subject, the claim is about the rig. C3 in particular must go
  through `yoloai`, not through `container run` by hand.
- A global `sudo` ticket invalidates every "is the refusal back?" check
  (`project_spike_harness_sudo`). C4's `sudo` arm must establish `sudo -n /usr/bin/true` fails first,
  the method `pf-grant-matrix.txt` used.

## Results — round closed 2026-08-13

Every item has run. C2b is dropped, in writing, below. **This section reports; it does not decide.**
Per the brief, `network-mode-reshape.md`, D137, D138 and D139 are untouched by this round and the
synthesis pass is the owner's.

| Item | Result | Record |
| --- | --- | --- |
| C1 | **apple has shape (B).** | [`c1-apple-channel.txt`](../../design/research/mac-channel/results/c1-apple-channel.txt) |
| C2 | **Softnet contains a root guest.** | [`c2-softnet-adversarial.txt`](../../design/research/mac-channel/results/c2-softnet-adversarial.txt) |
| C2b | **Dropped** — see below | — |
| C3 | **DF198 confirmed on apple and tart; seatbelt fails a third way.** | [`c3-network-none.txt`](../../design/research/mac-channel/results/c3-network-none.txt) |
| C4 | **The seatbelt mechanism is not a bounding set; it is SBPL's irrevocability.** | [`c4-seatbelt-soundness.txt`](../../design/research/mac-channel/results/c4-seatbelt-soundness.txt) |

**C1 — apple exposes `--publish-socket host_path:container_path`**, which carries arbitrary
bidirectional data across the VM boundary and **survives `--network none`**: the guest holds only
`lo`, its egress is gone, and the channel still completes round trips, 5 of 5 concurrently. Two facts
for whoever builds on it. **The direction is inverted for a proxy** — `container` creates the host end
and refuses to reuse an existing one, so the guest listens and the host connects; the guest cannot
initiate (vsock to host CID 2 answers `ConnectionReset` on every port tried). A build therefore needs
a guest-side shim that multiplexes local connections over the channel, or a host proxy that connects
in and speaks a reverse protocol; which is cheaper is not decided here. And **the host end is an
`AF_UNIX` path with a 108-byte `sun_path` limit**, which a socket under
`~/.yoloai/sandboxes/<name>/` can exceed — it bit this round before it was noticed.

**C2 — Softnet is an out-of-sandbox enforcement point in D137 §1's sense.** Default-deny blocks the
denied destination while the permitted one still answers, and a **root** guest does not get out from
under it: a MAC spoof and a gateway override both stayed blocked with the liveness control holding.
A static-IP reassignment is recorded **INCONCLUSIVE** rather than as containment — it cost the guest
its own permitted destination too, so its "denied host unreachable" was self-inflicted. 2 of 3
attacks counted. The mechanism is structural: `softnet` is a *host* process holding the VM's network
FD and enforcing its MAC, so there is no in-guest handle on it.

**C2b is dropped.** It would have re-measured `tart-net-key.txt`'s pre-v2 claim that pf keyed on a
bridge member blocks nothing. C2 settles the question it was serving — tart does not need pf — and
the pf result decides nothing either way, since a rule on a shared bridge cannot give per-sandbox
policy on either generation's reading. **The requesting agent's original claim is nonetheless wrong**,
and by a route neither party proposed: tart's enforcement is Softnet, not pf, and not nothing.

**C3 — see [DF198](../../design/findings-unresolved.md), now measured, plus two new findings.** A sandbox
reporting `Network:  none` reaches the internet on apple (**301**, with a global **IPv6** address as
well as v4) and on tart (**301**), positive controls holding on both. Seatbelt is a third behaviour —
it **refuses to start** ([DF199](../../design/findings-unresolved.md)) — and the conformance case that should
have caught any of this **cannot fail and does not run on any affected backend**
([DF200](../../design/findings-unresolved.md)). The apple remedy is measured and is one flag: `container run
--network none` yields a stackless guest, and `none` is a special value rather than an unattachable
name.

**C4 — D139's stated reasoning does not transfer to seatbelt, and its conclusion survives anyway.**
The mechanism is that an SBPL profile is applied at `exec`, inherited, and cannot be relaxed from
inside: a nested fully-permissive `sandbox-exec` escapes from an unsandboxed parent and **not** from
inside the profile, and **a genuinely root process** — `sudo` confirmed returning `uid=0` under a
temporary sudoers grant, so the refusal is containment and not an authentication failure — **stays
contained**. Seatbelt can express shape (B): `(deny default)` plus
`(allow network-outbound (literal "<socket>"))` gives proxy-socket-reachable / internet-refused. Two
traps: SBPL's `network*` class covers **AF_UNIX**, so `none` and (B) are *different profiles* (this is
DF199's mechanism), and the literal must be **vnode-resolved** or it silently grants nothing.

### What the round did to its own inputs

**One reported observation was withdrawn mid-round, and the harness is why.** An exploratory
`tart run` was read by hand as *"Softnet default-deny reproduced: permitted 301, denied 000"* and
reported as such. The denied address was `8.8.8.8`, which does not answer HTTP from this host under
**any** policy — so the `000` was free and the reading was the [D136](../../decisions/working-notes.md)
§3 specimen, reproduced by the round that cites it. C2 run 1 then voided at its baseline before
rendering a single expectation, and the run is kept as
[`c2-softnet-adversarial-run1-free-negative.txt`](../../design/research/mac-channel/results/c2-softnet-adversarial-run1-free-negative.txt).
What separated the two was not scepticism; it was that one of them had to declare a baseline.

A second invalidated run,
[`c1-apple-channel-run1-probe-not-armed.txt`](../../design/research/mac-channel/results/c1-apple-channel-run1-probe-not-armed.txt),
is a `predicate-bug`: a probe callable not parameterised by arm, so a sample re-ran the baseline.
`Direction: contradicted` — it was caught because it disagreed.

And the prior-art gate's own first draft was wrong about the corpus, recorded as
[A39](../../agent-failures.md). The correction is in the gate section above.

### Owed to the corpus

`macos-isolation-spike/results/` is **owed a provenance marking** and does not have one, because a
round was live in that directory while the provenance work landed. This round did not write there.
Whoever marks it should note that the three generations of superseded position already in
`macos-pf-privileged-path.md` are the argument for doing it.
