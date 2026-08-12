> **ARCHIVED — not maintained, not swept, not a live reference.** Everything below was
> true when written and has not been checked since. It is **not a specification** — its
> conclusions have been applied to the live documents listed under *Where this landed*, and
> those are the current answer. Good for archaeology: what was asked, what was run, and which
> runs were thrown away. See [`../README.md`](../README.md).

> **ABOUTME:** The fixed item set for the second Linux enforcement verification round — what is still
> a claim rather than a measurement before the build starts, what each item decides, and what it
> costs. Records what to run, not code.

# Enforcement verification round 2 — Linux

- **Status:** IMPLEMENTED — opened and closed 2026-08-12 under [D136](../../decisions/working-notes.md). Every item ran or was dropped in writing; the single synthesis pass has been applied to `enforcement-build.md` and `enforcement-state-reaping.md`, and **those are what to cite**.
- **Depends on:** —

**Where this landed.** The outcomes below are recorded as they were found; the conclusions drawn from
them are live elsewhere, and those are what to cite:

- [`enforcement-build.md`](../../design/plans/enforcement-build.md) — **Part 2 rewritten** (the
  counter poll retired, two subscriptions adopted, no spike outstanding), Part 0's forced ordering and
  mandatory reinstall, Part 1's UDP and install cost, Part 3's broken-host and event-loss cases, Part
  5's tier-3 row, and § *Known-unmeasured*.
- [`enforcement-state-reaping.md`](../../design/plans/enforcement-state-reaping.md) § *Why the
  immunity is structural* — V5's ifindex binding and what it closes.
- [DF194](../../design/findings-unresolved.md) — filed: a guest can unbind its own enforcement by
  destroying its own interface. [DF192](../../design/findings-unresolved.md) — **amended**: the Linux
  counter-comparison it proposed is superseded and now wrong in both directions.
- [`backend-idiosyncrasies.md`](../../backend-idiosyncrasies.md) — `nft flush ruleset` destroys
  Docker's own chains.

**The raw runs are not archived** and remain the citable evidence:
`design/research/linux-enforcement/results/v*.txt`.


**This file is the round's boundary** (D136 §3). The item set below was fixed before the first run.
The round closes when every item has run or been explicitly dropped **in writing, here**. Until then
`enforcement-build.md` and `enforcement-state-reaping.md` are **not edited** — intermediate optima
are working state, and one synthesis pass applies the whole fact set at close.

Adding an item mid-round is normal; a run that opens a question is doing its job. Adding it *to this
file* is what keeps the boundary real.

## The prior-art gate (D136 §1), discharged 2026-08-12

Run before the item set was fixed, and it changed the item set — which is the point.

- **[`prior-art-egress-enforcement.md`](../research/prior-art-egress-enforcement.md) §3 says the
  industry remedy for enforcement liveness is a *subscription, not a poll*.** Docker reacts to
  firewalld's D-Bus reload signal (moby PR #49443) rather than health-probing. **Consequence for this
  round:** the brief's Part 2 spike was written as *"does the counter detector handle an idle
  sandbox?"*. That is the second question. The first is **what the subscription actually covers** —
  and there is at least one fault where no ruleset event can exist, because nothing in the ruleset
  changes: a netdev chain going inert when its device disappears (r11). So V1 precedes V6, and V6 is
  scoped to whatever V1 leaves uncovered.
- **Same document, §3's correction: moby PR #49728, "Stop firewalld reload re-creating rules for
  deleted networks."** Their liveness fix resurrected rules for dead networks. Restore and reap are
  one question asked in two directions. This is already in the brief's Part 3 and needs no item here —
  recorded so the round does not re-derive it.
- **`man nft` on this host is ambiguous about netdev device binding** — base chains "exist per
  interface only" but take a device "name as a string". Mailing-list traffic exists around same-name
  device re-registration, and a CVE the search surfaced **could not be verified** against cve.org,
  NVD or kernel.org, so it is not cited. **Consequence:** the field has not published a clean answer,
  so V5 is warranted rather than redundant. This is the opposite of the 2026-08-08 case (A35), where
  the answer *was* published and got re-derived anyway.
- **Not re-opened:** `meta ibrname` was flagged twice (r8, r9) as possibly removing the
  `br_netfilter` dependency. The netdev key already removed it (r10 part B measured a netdev ingress
  chain enforcing with `br_netfilter` absent), so the question is moot for the adopted design and is
  **dropped, not deferred**.

## Working model

Each item names what to run, **what it decides**, and what it costs. An item that decides nothing is
not an item. Every harness is written against `scripts/research_harness_v2.py` (D136 §2), so an
expectation that rests on a probe never shown reporting failure will refuse to render.

## Items

| ID | Question | Decides | Cost |
| --- | --- | --- | --- |
| V1 | Does `NFNLGRP_NFTABLES` deliver an **attributable** event for each way our chain can stop enforcing? | Whether Part 2 is subscription-first with a poll backstop, or poll-first. Prior art says the former; this says whether it holds for *our* fault set. | med |
| V2 | Can a netdev chain be installed for a device that **does not exist yet**, and does it bind when the device appears? | Part 0's ordering. If yes, enforcement precedes the sandbox and the pre-agent hook is simple. If no, there is a window and Part 0 must close it another way. | low |
| V3 | Does the netdev chain's deny cover **UDP**, and does DNS survive it? | A containment hole if not: every rule and probe in the corpus is TCP. Part 1's rule shape. | low |
| V4 | Can a guest holding `CAP_NET_ADMIN` influence a **netdev chain** bound to its host-side veth? | D135's tier-3 claim, "measured not defeasible". k1 tested an interface-keyed *filter rule*; a netdev chain is a different object. | med |
| V5 | Does nft bind a netdev chain by **name** or by **ifindex**? | Whether r11's `stale-but-inert` is structural (new ifindex can never inherit) or incidental to this kernel. | low |
| V1b | **Added mid-round 2026-08-12, from V1's result.** Does the *link* netlink group (`RTNLGRP_LINK`) report the device disappearing — including when a hostile guest destroys its own end? | Whether Part 2 needs a **poll at all**. V1 found the nftables group silent for exactly one fault: the device vanishing. That is a link event, not a ruleset event, and if a second subscription covers it the detector is two subscriptions and no polling — which makes V6 moot. | low |
| ~~V6~~ | ~~Does a counter-vs-`rx_packets` detector report UNKNOWN for an idle sandbox?~~ | **DROPPED 2026-08-12, explicitly, after V1b.** Its premise was the faults the subscriptions leave uncovered, and they leave none — so the detector never infers from traffic and a quiet sandbox is never ambiguous. The question was a cost of the poll, and the poll is gone. Recorded rather than deleted, because a silently vanishing item is how a round pretends to have closed. | — |
| V6b | **Replaces V6.** Can a **foreign netdev chain** on the same device shadow ours — a higher-priority `accept` that stops our deny being reached? | The Linux analogue of `pf-anchor-eval.txt`, the sharpest finding in the macOS corpus: a loaded anchor pf never evaluates, with all three health checks green. Neither subscription covers it — the foreign chain's creation is an event about *someone else's* table. R8's bounds name it as untested: *"Arm 1's inert case is a missing kernel module, not a rule being shadowed."* | low |
| V7 | What do **N chains** cost, and how long does loading a 10000-element set take? | Part 1's acquisition-path cost. r14 priced one device at 1/1000/10000 elements; N devices and set-load latency are both unmeasured and both land on every start. | med |

## Outcomes

Filled in as each item runs. Raw output in
[`../research/linux-enforcement/results/`](../research/linux-enforcement/results/).

| ID | Outcome |
| --- | --- |
| V1 | `v1-netlink-event-coverage.txt`. The nftables group reports **every ruleset fault attributably** — our table deleted, `flush ruleset`, a reload beginning with a flush. **Silent for exactly one**: the device vanishing, where nothing in the ruleset changes and the table still lists as present. Collateral: a host-wide flush destroys **docker's own chains** too. |
| V1b | `v1b-link-event-coverage.txt`. The **link group covers that one**, attributably, in both cases that matter — an ordinary stop and a guest destroying its own end. So the detector is two subscriptions over disjoint fault classes, with **no poll**. |
| V2 | `v2-v5-netdev-device-binding.txt`. **A netdev chain naming an absent device does not load** (`No such file or directory`). Part 0 cannot install enforcement first; there is a window, and it is closeable only because yoloAI controls when the *agent* starts. |
| V3 | `v3-netdev-udp.txt`. **UDP is denied by the same `ip daddr` rule**, allowlisted UDP still resolves, and `reject` answers in 0.05s against `drop`'s 3.06s — the same benefit R10 measured for TCP. The corpus's largest protocol gap is closed. |
| V4 | `v4-hostile-guest-vs-netdev.txt`. A guest with `CAP_NET_ADMIN` adds an address, changes its MAC, flaps, renames its own end and fragments — **blocked through all five**. It **can** unbind enforcement by destroying its own interface, which costs it the network in the same instant. |
| V5 | `v2-v5-netdev-device-binding.txt`. **Binding is by ifindex**, proved by renaming a live device: index unchanged, name changed, chain still enforcing, nft re-rendering under the new name. R11's `stale-but-inert` is therefore **structural**. |
| ~~V6~~ | **Dropped in writing** — premise removed by V1b. See the item row. |
| V6b | `v6b-foreign-chain-shadowing.txt`. A foreign higher-priority **accept does not shadow** our deny (ours still counted 2→4). A foreign terminal **drop freezes our counter** while enforcement is intact — so the retired poll had errors in *both* directions. |
| V7 | `v7-scale-and-install-cost.txt`. A 10000-element allowlist installs in **25.7 ms** empty / **30.0 ms** with 32 other chains loaded, against an 816 ms sandbox start. Absorbed, not visible. All cells n=1. |

## Explicitly out of this round

- **macOS anything.** No Mac on this host; the macOS half is its own round.
- **IPv6.** Owned by `ipv6-network-isolation.md`; the netdev policy lets it past the chain policy
  deliberately and that is a design choice, not an unmeasured fact.
- **containerd with yoloAI's own conflist**, and **rootless podman netns teardown.** Both are Part 4
  and neither blocks Parts 0–3. Named so they are not lost.
- **A hostile guest against the detector** (V6's adversarial half) — shaping traffic to keep
  `rx_packets` low. Out of scope for the errant-agent threat this layer targets; it belongs with the
  rogue-agent work.
