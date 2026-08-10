> **ABOUTME:** How host-side per-sandbox egress enforcement is keyed, applied, verified and
> reclaimed, on both platforms. Rewritten 2026-08-10: the original design keyed on the guest's IP
> address, and measurement plus source research established that was the wrong axis.

# Plan: host-side per-sandbox egress enforcement

- **Status:** PLANNED — no production code. Rewritten 2026-08-10 on a different basis from the
  2026-08-06 draft; see § *Why this was rewritten*.
- **Depends on:** tamper-resistant-network-isolation.md, macos-pf-privileged-path.md
- **Rides:** **any.** It adds enforcement to a mechanism that does not ship yet; nothing user-visible
  is withdrawn.

**Filename note.** This file is still `enforcement-state-reaping.md` because eleven documents and
harness scripts link to it, five of them in another agent's write surface. The name is historical:
reaping is now one section rather than the subject. Renaming it is a follow-up, not a reason to churn
those files.

## Why this was rewritten

The 2026-08-06 draft keyed enforcement on the sandbox's IP address and built four rules to make that
safe: clear-before-claim, empty-before-populate, an acquisition ordering, and a sweep. Three things
happened.

1. **Two independent audits** found the draft's conclusions systematically stated one step wider than
   the runs that produced them. The load-bearing one — *"there is no stable per-sandbox non-address
   key on either platform"* — rested on a single nftables matcher on Linux and on a misread of the
   macOS result.
2. **Re-measurement refuted it.** A per-sandbox interface *is* a usable key on both platforms
   (`k1-interface-as-sole-key.txt`, `k2-veth-key-shared-bridge.txt`, `pf-interface-key.txt`).
3. **Source research** into Calico, Cilium, moby, netavark, CNI, Tailscale and systemd found that
   nobody keys on the workload's address, that the conntrack fast-path we had adopted is the direct
   cause of two bugs we hit, and that our reaping design had already shipped and been fixed as a
   production defect elsewhere.

The full list of retracted claims lives in
[`research/linux-enforcement/results/README.md`](../research/linux-enforcement/results/README.md)
§ *What these files do NOT support*, which is the durable record. This document is the replacement
design, not a diff against the old one.

## The three decisions everything else follows from

1. **Key on the host-side interface, never on the guest's address.**
2. **Evaluate policy on every packet — no `ct state established,related accept` in front.**
3. **A rule's identity is the sandbox ID; the interface name is only its match.**

---

## 1. The key is the host-side interface

**Measured, Linux** (`k1-interface-as-sole-key.txt`): two sandboxes on two docker networks, under a
policy containing **zero address-matching rules** and keyed only on `iifname "br-<netid>"`. The
allowlisted destination stayed reachable, the denied one was blocked with the counter to prove which
rule decided, and the second sandbox was untouched. Four networks created and destroyed in sequence
produced four distinct bridge names: **they do not recycle.**

**Measured, shared bridge** (`k2-veth-key-shared-bridge.txt`): containerd puts every sandbox on one
`yoloai0`, so the per-network variant does not apply there. With `br_netfilter` loaded,
`iptables -m physdev --physdev-in <veth>` discriminated one sandbox from another on `docker0`.

**Measured, macOS** (`pf-interface-key.txt`): a held bridge index is never reassigned; an ingress tag
keyed on the bridge is per-sandbox under one network per sandbox, under a ruleset containing no
address; and a rule re-attaches by name when its interface returns. What the earlier pass recorded as
"the index recycles" had measured recycling *after release*, which was never the question.

**It is not defeasible by the guest.** Holding `CAP_NET_ADMIN`, a sandbox added a second address and
stayed blocked, changed its MAC and stayed blocked, and cannot name the host-side interface at all —
it sees `eth0` and `lo`, and renaming the peer from inside fails with `No such device`. Neither Calico
nor Cilium does MAC-based anti-spoofing either, for the same reason: the host-side interface is the
trust boundary.

### The pin: if an address appears in a rule, bind it to the interface upstream

Both mature implementations still *use* addresses — they just never let the address be the thing that
identifies the workload. Calico's per-endpoint chains contain **zero** IP matches
(`felix/rules/endpoints.go`); the IP↔interface binding is enforced *before* policy by strict RPF in
`raw`/PREROUTING (`-m rpfilter --invert --validmark`), with a comment aimed straight at our threat
model: *"non-privileged containers can't usually spoof but privileged containers and VMs can."*
Cilium bakes the endpoint's IP into its per-endpoint program as a load-time constant and drops on
inequality (`bpf/lib/lxc.h`, `is_valid_lxc_src_ipv4`).

**So: an address may appear in a rule only where an upstream check guarantees it belongs to that
interface.** Without the pin, address-keyed rules are defeasible; with it, they are a derived
attribute of a key that is not.

**Not measured:** whether RPF is available and correct on every backend's topology here — in
particular under rootless podman's userspace stack, and on macOS where the equivalent is Cilium's
per-endpoint constant rather than a route lookup. Assume nothing.

---

## 2. No conntrack fast-path

Every ruleset in the old draft opened with `ct state established,related accept`. **That rule is the
direct cause of two of the bugs this workstream found**, and it is optional.

Cilium routes `CT_NEW` and `CT_ESTABLISHED` into the same case, both calling a live policy lookup;
only `CT_RELATED` and `CT_REPLY` skip enforcement (`bpf/bpf_lxc.c`). Consequently Cilium has **no
conntrack flush on policy change anywhere**, and its GC filter could not express one. Calico keeps the
fast-path and therefore needs flushing, with an ordering constraint most people would get wrong.

**Measured here** (`p1-no-fastpath-correctness.txt`, `p1b-revocation-decay.txt`,
`p2-fastpath-cost.txt`), on a rig with no DNS in the path:

| | correctness | revocation of an in-flight transfer | throughput |
| --- | --- | --- | --- |
| fast-path | 64 MB transfer completes, denied refused, DNS fine | **300 KB/s sustained for 30 s; drop counter 0** | 35–42 Gbit/s |
| no fast-path | identical | **0 KB/s within 10 s; drop counter 12** | 35–42 Gbit/s |

The drop counter is what makes the second column a result: with the fast-path, no packet ever reached
the deny rule. Throughput was measured at allowlist sizes 1, 1000 and 10000; run-to-run variance on a
single configuration exceeded every difference between configurations, so the honest statement is
**this proxy cannot separate the two shapes**, not that they are equal.

**What dropping it removes:** the whole conntrack teardown apparatus — no flush, no ordering
constraint, and no destination-scoped invalidation. That last one matters most, because **nobody in
the surveyed corpus has built one.** Calico flushes by *endpoint* address (`--orig-src`/`--reply-src`);
we would be revoking a *destination*. Every time this workstream built something with no prior art, it
was wrong.

It also removes the recycling inheritance measured in `l10c-udp-residue.txt`, which existed precisely
because the established rule accepted a flow the allowlist did not.

**And it closes a hole the field leaves open.** Grepping Calico and Cilium for conntrack helpers
returns zero hits in both; both accept `RELATED`. An enabled FTP/SIP helper can mint an expectation
authorising a connection the allowlist never approved. Not accepting `RELATED` on egress closes it.
**This is a claim about rule shape and was not exercised by any run** — it needs its own test.

### Two constraints that come with it

**The chain policy must remain `accept`.** Predicted wrong and caught by a counter: replies *do*
traverse the chain (1308 packets, 67 MB in `p1-no-fastpath-correctness.txt`) — they simply match no
rule, because every rule matches on the guest's side. A chain defaulting to `drop` would kill all
return traffic.

**The sandbox gets a stall, not an error.** The transfer sat at 0 KB/s for 25 s rather than failing.
Calico's BPF mode injects RSTs for exactly this reason, because a silently dropped flow is a black
hole until TCP timeout. If "the agent just reconnects" is to be true, that is separate work and it
applies to either shape.

---

## 3. Identity is the sandbox ID, not the interface name

**Interface names are reusable, and one backend reuses them aggressively.** docker generates
`veth` + 7 random hex with a 3-attempt collision probe and **persists** the name because it depends on
it. CNI generates `veth%x` from 4 random bytes and does **not** persist it. netavark passes an **empty
name and lets the kernel pick `vethN`** — lowest-free sequential, the worst case. My own K2 measured
three docker cycles without reuse; that result does not transfer to netavark.

CNI's own mac-spoof checker faces exactly this problem and separates the two concerns
(`pkg/link/spoofcheck.go`): the **match** is `iifname == <host veth>`, while the **identity** is a
chain name and rule comment derived from the container ID. Its teardown proves the point — it locates
and removes everything with the interface name and MAC passed as empty strings.

**So every rule we install carries an identity derived from the sandbox ID**, in the nft comment or
the chain name. Calico does the same with a hash in an iptables comment, and its reaper parses back
*only* the comments, having explicitly rejected parsing full rules as unrobust.

**Consequence:** if we ever key on iptables rather than nftables, we must plant a marker rule — CNI's
own source says GC is impossible otherwise. An nft comment is already a reconcilable key.

---

## Reaping, which is now small

With a key that does not recycle and an identity independent of it, reaping stops being a security
mechanism and becomes hygiene.

**Nothing beneath us will do it.** The CNI GC verb is unimplemented in all 18 plugins (`/* FIXME GC */`),
and the one working reconcile in that tree is unreferenced dead code. netavark has no reconciler at
all. Docker's `cleanupLocalEndpoints` is the only one in the corpus, and it runs at daemon start only.
The six-day-old orphaned accept rule and IPAM reservation in `l12b-stale-cni-entry.txt` are the
expected outcome, not an anomaly.

**The design, taken from docker's shape with netavark's ordering rule:**

- A **persisted per-sandbox record** is the reconcile input. Docker reconciles from an in-process
  registry, which does not survive its own death; netavark persists, which does.
- **The record is removed before the rules it describes**, never after — netavark's file-lock comment
  names the exact race, and they hit it.
- **At startup, diff persisted records against live sandboxes and tear down the difference**, driving
  the same path a normal delete takes.
- **Never delete on one identifier alone.** Calico shipped an address-keyed reaper that deleted a
  *live* workload's conntrack entries because an overlapping object went away (commit `cd27e0af`), and
  Cilium guards its interface sweep with a reverse-pointer cross-check against an ifindex clash. Both
  converge on cross-validating against a second independent signal. Cilium's liveness signal is
  `LinkByName` plus **two consecutive** failed 5-minute rounds — deliberately slow, to avoid flapping.

**Neither model covers a kill between "rules torn down" and "record removed."** That is netavark's
residual gap and the reason a startup reconcile is still needed on top of ordered teardown.

---

## Verification and liveness

**Reading the rules is not verification, on either platform.** On macOS, under the shadowed fault,
`pfctl -s rules` reports HEALTHY while a denied destination answers 301. On Linux, a `socket cgroupv2`
rule in `prerouting` loads clean and never fires. State that looks right can be inert.

**Linux: subscribe.** Corrected 2026-08-10 — the earlier conclusion that a whole-ruleset flush emits
no signal was reasoned from firewalld's D-Bus signal not covering it and generalised without testing
the mechanism that does. Verified in a throwaway netns: `nft flush ruleset` emits a delete event per
object plus `# new generation N by process <pid> (nft)`. `iptables-nft` operations are equally
visible. **`iptables-legacy` is silent** — it uses `setsockopt`, which has no multicast channel — so a
probe is the fallback for that backend and a startup backstop, not the primary mechanism.
`github.com/google/nftables` ships a `Monitor` joining `NFNLGRP_NFTABLES`; it has no `NFT_MSG_NEWGEN`
case, so attribution needs custom parsing, but `DELSETELEM` is covered.

**macOS: poll.** No signal of any kind — zero log entries on a pf predicate, zero of nine watched
notify keys with the watcher proven alive in the same run, and `/etc/pf.conf`'s mtime unmoved. The
behavioural canary is the detector, at a **corrected 320–385 ms** with a 15.3 s worst case against a
dropping path. It previously failed open, returning HEALTHY when the network was down.

**Do not fight forever.** Tailscale's trample handling is the pattern: detect, re-apply with backoff,
**bound the retries**, then raise a persistent health warning and stop — at n=10 it logs that it is no
longer attempting to replace the file. Collapse a batch of events into one fixup and rate-limit, or
two processes fight at machine speed. **yoloAI must decide its terminal state when it loses**: fail the
sandbox, or degrade to visibly-unenforced. That is a product decision, not an implementation detail.

---

## What the forward hook does not cover

**Sandbox→host traffic never enters it** (`r6-host-destined-traffic.txt`). Under a drop-everything
forward policy a sandbox still reached the bridge gateway *and* the host's LAN address on SSH: forward
counter 3, input counter 8. Packets addressed to an address the host holds are delivered locally.

So every rule here is scoped to traffic the host **routes onward**, and is silent about traffic it
**terminates** — and silence is permission. The counterpart must be an **allowlist of host services**,
not a blanket deny, because the sandbox is supposed to reach the credential broker's injector endpoint
on the gateway address. Getting that backwards breaks credential injection on every bridge backend at
once.

---

## Per-backend reach

| Backend | Key available | Notes |
| --- | --- | --- |
| docker | per-network bridge, or veth via `physdev` | measured |
| containerd | veth via `physdev` (one shared `yoloai0`) | mechanism measured on docker's veths, **not on CNI's** |
| rootless podman | inside its own netns | the host netns cannot reach it at all; enforcement installs in the rootless netns, which the guest cannot enter (`CAP_SYS_ADMIN` absent, netns path invisible) |
| apple | bridge index under one network per sandbox | needs the lifecycle rule below |
| tart | **none** | no per-sandbox networks; a different question, not a missing answer |
| seatbelt | out of scope here | parked, see `seatbelt-host-pf-enforcement.md` |

**`br_netfilter` is unowned.** The veth variant needs it, docker only enables it for `icc=false`, and
neither CNI nor netavark mentions it at all. It is a **host-wide** setting that changes packet paths
for every bridge on the machine. It needs an explicit preflight assertion, not an assumption.

**Rootless podman's enforcement dies with the last sandbox** — the netns is destroyed and the rules
with it — so installation is per bring-up, before the agent runs.

---

## macOS: one lifecycle rule Linux does not need

An ordinary restart releases a bridge index, a stranger can take it, and **that stranger inherits the
departed sandbox's policy whole** — it reached a destination only the departed sandbox was granted and
was refused its own. First-match-with-`quick` means the stale pass wins before the stranger's own rule
is reached, and it is invisible to inspection. Withdrawing the stale rule restores correct policy
exactly (measured, not advised).

**So: withdraw a sandbox's rule when its interface goes; re-read the real index when it returns.**
Linux needs no equivalent because its names do not recycle.

**macOS also still needs state teardown for revocation**, which Linux does not once the fast-path is
gone. The rule-alone arm was run and failed: the transfer survived, all four states intact. The form
that works is `pfctl -k <guest> -k <gateway>`, guest first — gateway-first kills zero states and
reports success. **Whether pf's `no state` gives the Cilium shape on macOS, removing this asymmetry,
is untested and is the obvious next experiment.**

**Also unresolved:** "pin the `-k` peer to our gateway" holds on the default network but weakens
exactly where per-sandbox networks are adopted, since those get their own gateways from a hole-filling
allocator. The two recommendations pull against each other and the combination has not been through
D132's permit/refuse matrix.

---

## What "done" means

Each of these must fail when the change it covers is reverted.

- A sandbox holding `CAP_NET_ADMIN` that adds a second address, and one that changes its MAC, are
  **still contained** — with a positive control proving an allowlisted destination still works.
- Revoking a destination **stops an in-flight transfer**, asserted on the rate falling to zero *and*
  on the deny counter incrementing. A rate of zero with a zero counter is a free negative.
- Two sandboxes on one bridge get **independent** policy: one blocked, the other reachable, in the
  same run.
- A sandbox destroyed without clean teardown leaves **no rule** that a later sandbox can inherit,
  asserted by reconciling and then checking a fresh sandbox gets only its own allowlist.
- Rules for a sandbox whose interface has gone are **withdrawn** (macOS), asserted by a stranger
  taking the index and getting its own policy rather than the departed one's.
- The **host-service allowlist** permits the broker's injector endpoint and denies everything else on
  the host, asserted with credential injection actually working.
- Enforcement is **reinstalled before the agent runs** on rootless podman, asserted across a
  stop/start of the last sandbox in the netns.

**Every one pairs its negative with a positive control in the same run** (testing-principles §11), and
the control must travel the **same protocol and path** as the test — three runs in this workstream
recorded a reassuring "blocked" that was free because they probed with ICMP and controlled with TCP.

---

## Open questions

- **Does pf's `no state` give the Cilium shape on macOS?** If so the platforms converge completely and
  the `-k` grant widening becomes unnecessary. Cheap, and it is the highest-value remaining experiment.
- **Does the veth key work on CNI's own veths**, not just docker's? Mechanism should carry; untested.
- **What is the terminal state when we lose the fight** to another firewall manager?
- **Does the `RELATED` omission actually close the helper hole**, measured rather than argued?
- **DF189's subnet collision.** Interface keying makes it survivable — two sandboxes with the same
  address are still distinguishable — but the routing conflict remains and the subnet should still
  move off `10.89.0.0/16`. `10.0.0.0`–`10.87.255.255` is outside every podman pool and every docker
  local pool.
- **DF190**, an apple sandbox losing egress when an unrelated sandbox reclaims its bridge index, is a
  backend defect underneath all of this and is not addressed by any of it.

## Related

- [macos-pf-privileged-path.md](macos-pf-privileged-path.md) / [D132](../../decisions/working-notes.md) — the macOS privileged path.
- [D133](../../decisions/working-notes.md) — resolution happens in the guest's context; its central rejected alternative is still owed a re-measurement with `--dns` set.
- [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) — the in-guest layer, which this does not replace.
- [prior-art-egress-enforcement.md](../research/prior-art-egress-enforcement.md) — the first reading pass. **Treat with care:** an audit found it inadequate, and the source-reading round that followed it is where the load-bearing findings came from.
- Raw runs: [`research/linux-enforcement/results/`](../research/linux-enforcement/results/) and [`research/macos-isolation-spike/results/`](../research/macos-isolation-spike/results/), both of which keep their invalidated runs deliberately.
