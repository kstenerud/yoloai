> **ABOUTME:** The fixed item set for the round that decides whether a no-route sandbox plus a
> host-side proxy carries the work a developer expects, and which of two chokepoint shapes to
> build. Records what to run, not code.

# Proxy chokepoint verification round — Linux

- **Status:** IN-PROGRESS — opened 2026-08-12 under [D136](../../decisions/working-notes.md).
- **Depends on:** —

**Why this round exists.** The workstream changed course. `agent-privilege-reality.txt` measured
that the sandbox grants the agent root by design (`Dockerfile:229`, `NOPASSWD:ALL`), and that root
defeats two of three containment vectors — so **in-guest enforcement cannot be made tamper-resistant
on any backend**, and the open-ended set of things an adversarial agent might reach cannot be
enumerated from inside. The replacement is not a better filter: it is to give the guest **one
destination** and move all policy to a host-side proxy the agent has no privilege over.

That reframing is cheap to state and expensive to get wrong, because it trades an open-ended
*policy* problem for a closed *coverage* problem — and nobody has measured the coverage. This round
measures it.

**This file is the round's boundary** (D136 §2). The item set below was fixed before the first run.
The round closes when every item has run or been explicitly dropped **in writing, here**. Until then
`egress-proxy-build.md`, `netpolicy.md` and `tamper-resistant-network-isolation.md` are **not
edited** — intermediate optima are working state, and one synthesis pass applies the whole fact set
at close. Adding an item mid-round is normal; adding it *to this file* is what keeps the boundary
real.

## The fork this round is testing

Two shapes satisfy "the guest's only egress is the proxy". Both are live; one is provisionally
chosen and this round can falsify it.

| | **(A) IP chokepoint** | **(B) No IP stack** |
| --- | --- | --- |
| Guest has | a normal stack; one address permitted | `lo` only; a socket to the host |
| Enforcement rests on | `NET_ADMIN` drop **+** a host filter, correct, for the sandbox's life | the `NET_ADMIN` drop |
| Fails open to | the whole internet | nothing — there is no route to fail open *to* |
| IPv6 | must be covered explicitly in both families | absent by construction |
| Host state to reap | yes — a rule keyed to a device | none |
| `--port` (shipped) | works as today | **needs a reverse forwarder** |
| VM backends | a pf/filter rule, already measured on hardware | needs a channel across the VM boundary (vsock) |

**Provisionally chosen: (B) where the channel is cheap (docker, podman, seatbelt), (A) where it is
not (apple, tart, containerd/Kata), unified by one conformance assertion that does not care which.**
The reasoning is that (B)'s containment rests solely on the bounding-set `NET_ADMIN` drop — the one
vector `agent-privilege-reality.txt` measured to survive `sudo` — while (A) additionally rests on a
filter being present and correctly bound for the sandbox's whole life, which V5 and DF194 both show
is not free.

**P2 and P7 are this choice's falsification tests.** If a chokepoint sandbox cannot carry realistic
work, or if `--port` under (B) costs a second per-backend channel rather than a small forwarder,
(A) becomes the better shape on the majority backends too.

## The prior-art gate (D136 §1), discharged 2026-08-12

Read before the item set was fixed. It removed two items, split one, and found that part of the
answer was already on disk — which is the second time that has happened in this workstream ([A35](../../agent-failures.md)),
and the reason the gate exists.

- **[`agent-proxy-support.md`](../research/agent-proxy-support.md) already settles the containment
  half of the proxy-env question, with a verified per-agent table.** `HTTP_PROXY` is *never* a
  containment boundary for an untrusted agent — it lives inside the agent's environment, so the agent
  can unset it or open raw sockets. That document reached the identical conclusion this round was
  about to re-derive. **Consequence:** the proposed item "do package managers honour the proxy" loses
  its containment half entirely. What survives is *coverage* — whether the honest path works — which
  is genuinely unmeasured. Re-scoped as P4.
- **Same document: Codex `.no_proxy()`s inside its own sandbox, and Aider ignores env proxies
  without `DISABLE_AIOHTTP_TRANSPORT=True`.** Under today's design that is a quirk; under a
  chokepoint it is fatal, because there is no other path out. **This is a new item (P8)** and it did
  not exist in the proposed set.
- **The LLM path does not use `HTTP_PROXY` at all.** The shipped credential broker (D105/D106)
  points the agent at the injector via `base_url` with a placeholder credential. So the per-agent
  proxy quirks above bite only on *non-LLM* traffic — WebFetch, MCP servers, tool calls, package
  managers. That splits the census (P1) from the agent-cooperation question (P8) and is why they are
  separate items.
- **[`prior-art-egress-enforcement.md`](../research/prior-art-egress-enforcement.md) §5 records the
  peer group converging on shape (B)**: "agent VMs with no external interface at all, talking over a
  virtual socket to a host-side proxy that enforces an allowlist by hostname and injects
  credentials". Per D136 §1 a convergence away from our design is a *constraint*, not a note — and
  here it converges **toward** the provisional choice while diverging from this repo's own written
  primitive (below). Recorded so the synthesis pass does not treat (B) as novel.
- **[`netpolicy.md`](../netpolicy.md) §Hostile already commits to shape (A)** in writing: "a
  default-deny egress firewall in the sandbox netns whose only outbound path is forced to a
  filtering proxy on a different principal/namespace than the agent." That is the repo's committed
  primitive and the fork above departs from it. **Consequence:** the synthesis pass owes `netpolicy.md`
  an edit either way, and this round must not silently change a written commitment.
- **`prior-art` §1: Cilium inverts the DNS direction** — a DNS proxy observes the pod's own queries
  and populates policy from the answers the pod received, expiring on TTL. Under a chokepoint the
  proxy sees names directly and the whole host-vs-guest resolution problem dissolves. **Consequence:**
  the proposed DNS item is largely answered by reading; downgraded to P6, which asks only the residual
  question a document cannot answer — whether the guest needs a resolver at all.
- **`prior-art` §6 recommends "keep the in-sandbox layer rather than replacing it", and that
  recommendation is now weaker than when written.** It predates `agent-privilege-reality.txt`. The
  in-sandbox layer is defence-in-depth against a *confused* agent and must not be counted toward the
  adversarial property. Recorded here so it is not cited unqualified.

## Item set

Every harness uses `scripts/research_harness_v2.py`. Each item names what it decides, because an
item that decides nothing is a measurement looking for a home.

**P1 — Protocol and destination census of a real agent session.**
Run the product's own image and a real agent on a realistic task; capture every outbound flow.
*Decides:* what fraction of real traffic a proxy can serve at all, and what the non-HTTP remainder
is. Everything downstream is sized by this. *Cost:* one real agent run plus capture; the rig must
use the product's launch path, not a hand-built container ([A37](../../agent-failures.md)).

**P2 — Does a chokepoint sandbox complete realistic work?**
Clone, install dependencies, run tests, make LLM calls, with the guest holding one destination.
*Decides:* whether the whole direction is viable. A falsification test for the fork.
*Cost:* moderate; needs a working proxy stand-in, which is scaffolding, not the product's proxy.

**P3 — git over SSH.**
*Decides:* what it needs, and whether `CONNECT`-tunnelling it is a containment hole worth refusing
outright. A tunnel that carries anything is a hole in both shapes, so this is not fork-specific.

**P4 — Package managers under a forced proxy (coverage only).**
apt, npm, pip, go. *Decides:* whether the honest path works unmodified, and what config the image
must ship. The containment half is already settled by prior art and is **not** re-asked.

**P6 — Does the guest need a resolver at all?**
Under (B) there is no route for DNS to travel; under (A) port 53 is the classic exfil hole. *Decides:*
whether the image can ship with no resolver, which would close the DNS-exfil class by construction
rather than by policy.

**P7 — `--port` under a no-IP-stack guest.**
Port publishing is a shipped, documented feature (`--port host:container`, GUIDE.md:414,
`parsePortBindings`) that composes with `--network-isolated` today. *Decides:* whether (B) costs one
channel or two per backend. The other falsification test for the fork.

**P8 — Do the shipped agents work when the proxy is the only path?**
Claude via `base_url` (brokered), plus Codex and Aider, whose proxy handling prior art flags as
unreliable. *Decides:* whether a chokepoint is agent-agnostic or silently Claude-only.

### Dropped before the first run, in writing

- **"The closure question — what must the proxy decide on, and can that set be fixed at creation?"**
  Proposed as an item and **dropped: it is not measurable.** No hardware run distinguishes a closed
  policy from an open one; that is a design decision informed by P1's census. It moves to the
  course-change decision record (D137), which is where a choice belongs.

## Out of this round

- **macOS anything.** Needs a Mac; the channel question for apple/tart/seatbelt is explicitly the
  Mac agent's, per the split agreed 2026-08-12. apple's vsock availability is the single unknown
  that decides whether (B) is reachable there — and the fallback to (A) means it blocks nothing.
- **Building the proxy.** This round measures whether the shape works, not the SNI/`CONNECT` matcher
  it would need. `egress-proxy-build.md`'s hardening requirements stand and are not re-litigated.
- **The parser-differential class.** Already documented with a live precedent; it is a build
  requirement, not an open question.

## Outcomes

| Item | Verdict | Raw run |
| --- | --- | --- |
| P1 | **99.2% HTTP+DNS, zero QUIC; SSH to GitHub appeared unasked** | [`p1-agent-traffic-census.txt`](../research/proxy-chokepoint/results/p1-agent-traffic-census.txt) |
| P2 | **PASSES — agent completed the task, exit 0, with one destination; 7 real hostnames recovered** | [`p2-chokepoint-viability.txt`](../research/proxy-chokepoint/results/p2-chokepoint-viability.txt) |
| P3 | **SSH works via CONNECT — but no tunnel tool ships, and allowing it costs closure** | [`p3p4-toolchain-under-chokepoint.txt`](../research/proxy-chokepoint/results/p3p4-toolchain-under-chokepoint.txt) |
| P4 | **PASSES — npm/pip/apt/go/curl all work on standard proxy env alone** | [`p3p4-toolchain-under-chokepoint.txt`](../research/proxy-chokepoint/results/p3p4-toolchain-under-chokepoint.txt) |
| P6 | **Answered in passing by P2** — DNS was not permitted and the session worked; the proxy resolves | [`p2-chokepoint-viability.txt`](../research/proxy-chokepoint/results/p2-chokepoint-viability.txt) |
| P7 | — | — |
| P8 | — | — |
