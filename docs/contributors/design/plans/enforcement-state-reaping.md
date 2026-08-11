> **ABOUTME:** How host-side per-sandbox egress enforcement is keyed, applied, verified and
> reclaimed, on both platforms. Rewritten 2026-08-10: the original design keyed on the guest's IP
> address, and measurement plus source research established that was the wrong axis.

# Plan: host-side per-sandbox egress enforcement

- **Status:** PLANNED — no production code. Rewritten 2026-08-10 on a different basis from the
  2026-08-06 draft; see § *Why this was rewritten*. **Corrected 2026-08-11** from the macOS
  post-rewrite runs, which withdrew the `pfctl -k` requirement, found that the rewrite had dropped a
  rule the retired draft carried, and found that interface keying did not fit D132 at all. Each
  correction is marked in place rather than folded in silently.
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

**And on CNI's own veths, not just docker's** (`r9-cni-veth-key.txt`, 2026-08-11). The reach table
previously said the mechanism was measured on docker and "should carry" to CNI, which is the form of
reasoning this workstream has retracted five times. Two containerd sandboxes on one shared CNI
bridge: the physdev-keyed rule blocked A's denied destination (drop counter 5), left A's allowlisted
destination reachable, and left B untouched.

### A better Linux key: bind the chain to the device (2026-08-11)

`physdev` has a dependency the plan calls unowned, and **`r8-inert-rule-detection.txt` measured what
its absence costs**: with `br_netfilter` unloaded, a correct-looking physdev rule counted **0** while
a bridge-keyed rule counted 17 packets of the same traffic. It does not fail — it goes quietly
inert. Loading the module revived the same rule (counter +17). So `br_netfilter` is precisely what
gates this key, and anything on the host can unload it.

**A netdev ingress chain avoids the dependency entirely** (`r10-rule-shape.txt`). With
`br_netfilter` **not** loaded, a chain attached to one sandbox's veth blocked that sandbox (counter
5) and left a second sandbox on the same bridge reachable. **The key stops being a match and becomes
the attachment**: a chain bound to one device is per-sandbox by construction, and it runs before
bridging or routing decide anything.

**Its lifecycle is the trade, and it is measured** (`r11-netdev-lifecycle.txt`). On a hand-built veth
pair, so the same name could be destroyed and recreated on demand — docker does not recycle names and
therefore cannot produce the collision at all:

| | result |
| --- | --- |
| chain enforces while its device exists | yes (counter 2) |
| chain survives the device being deleted | **yes** |
| a NEW device under the same name inherits the old policy | **no** — reachable, counter frozen at 0 |

**So netdev trades one hazard for the other.** It is immune to the `k3b`/macOS-I5 inheritance
hazard — a returning `veth0` does *not* pick up a departed sandbox's allowlist, which is exactly what
rootless podman's name reuse makes dangerous for every other key. What it gets instead is a
**stale-but-inert** chain: still listed, still reading correctly, enforcing nothing. That is class 8,
and it is why `r8`'s counter detector is a prerequisite rather than a nicety.

**Consequence for the build:** enforcement must be **reinstalled when a sandbox's device is
recreated**, and its absence is invisible to rule inspection. That is a second, independent reason for
the pre-agent hook the rootless-podman row already required.

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

### On macOS the pool must be inverted, or the key does not fit the privileged path

**Added 2026-08-11.** Interface keying and D132 were designed against each other and neither document
noticed. D132 is defensible precisely because the unprivileged side changes **table membership** and
reloads one pinned file that root wrote — it can never author rule text. But an interface-keyed rule
*names a bridge*, and indices are dynamic, per-sandbox, and change across a restart, with the
lifecycle rule reloading on every transition. All three routes to installing such a rule are refused,
correctly (`pf-grant-matrix.txt` G4): loading our own file, rewriting the pinned file, and an inline
ruleset on stdin. **A clean refusal on all three is the problem, not the reassurance it resembles.**

**The fix keeps the model: invert the pool.** One slot per **bridge index** rather than per sandbox.
The pinned file enumerates every index the host could hand out, each with its own table; claiming a
sandbox means adding its allowlist to the table named after the index it landed on. Rules stay
static, membership stays the only thing the unprivileged side touches, and the key stays the
interface. Only the grant's table regex widens — a pattern change, not a model change. Measured, not
proposed: 164 rules covering `bridge100`–`bridge140` load, and two real sandboxes get independent
policy with **no rule text written at any point** (G5).

pf interface *groups* would have been cleaner and macOS does not have them — `ifconfig` answers
`group: bad value` and the man page never mentions them. Closed by measurement, not assumed.

**Two hazards this creates.** A sandbox landing on an index **outside** the pinned range meets no
rule and is silently unenforced — **fail-open, the worst direction**, and it needs a preflight
assertion that does not exist. And the superset makes the unpriced cost worse: every packet is now
evaluated against a first-match list dominated by rules for interfaces that do not exist. Linux
measured the fast-path free at allowlist sizes 1/1000/10000; **nothing prices per-packet evaluation
on pf at any size**, so "it costs nothing" is a Linux result only.

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

**On macOS the same observation has the opposite consequence**, and reading this section across would
build the wrong ruleset. There, a reply matching no rule meets pf's default *pass*, which mints
bidirectional state and defeats `no state` outright — so pf needs an explicit egress block that Linux
must not have. See § *The macOS price is a rule shape, not a keyword*.

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

> **Do not adopt that canary for this design without reading [DF192](../findings-unresolved.md).**
> It probes **from inside the guest** via `container exec`, so the hostile agent this workstream
> exists to contain can shadow `curl` and answer for it. Moving enforcement host-side is only half
> the win if the thing that decides whether enforcement is working still asks the sandbox. The
> proposed replacement — read our own anchor's `Evaluations` counter host-side, paired with a bridge
> traffic baseline — is in `macos-pf-privileged-path.md` § *Candidate: a fourth way out*, with the
> three measurements it needs first.

**The detector belongs on the host's side of the trust boundary, on both platforms.** Linux already
is: nftables counters are kernel state a guest cannot write, and comparing a chain's counters against
the veth's host-side `rx_packets` separates "our rules are in the path" from "our rules are inert"
with no guest participation at all. Anything in-guest is diagnostics and UX — worth having to tell a
cooperative user *why* a destination is refused, never load-bearing for whether yoloAI believes it is
enforcing.

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

### Measured 2026-08-11 (`r7-host-service-allowlist.txt`), and it settles the sever design

Two things this document previously reasoned its way to. Both now have runs, each with a counter
proving the deciding rule fired rather than a probe failing for its own reasons.

| | measured |
| --- | --- |
| Under a forward-hook **deny-all**, does the sandbox still reach the host gateway? | **yes** — external egress stops (drop counter 4), the gateway stays reachable |
| Can an **input-hook allowlist** permit only the broker port? | **yes** — broker reachable (accept counter 6), every other host service denied (drop counter 4) |

**Consequence: on Linux, "fail closed" for a running sandbox can mean *sever*, not *kill*.** Because
host-destined traffic never enters the forward hook, dropping everything there cuts internet egress
while leaving the agent's path to the credential broker intact — and a brokered agent therefore keeps
working through a sever, seeing only "some destinations now refuse". That was previously an inference
joining R6's input/forward split to `applyBrokerEnv`'s use of `DialHost`, which is precisely the shape
of the conclusions this workstream has had to retract. It is now a measurement.

**The complete sever shape is both hooks**: forward deny-all *plus* an input allowlist holding the
broker and nothing else. The forward half alone leaves every other host service reachable.

**Deny with `reject`, not `drop` — now measured on Linux too** (`r10-rule-shape.txt`). This was
previously a macOS result plus Calico's RST injection, read across; both are evidence about other
systems, and a platform read-across is how both of this plan's rule-shape errors happened. Same
denied destination, same probe, one keyword different: **`drop` blocked in 5.06 s (the full curl
timeout), `reject` blocked in 0.10 s** — with a counter on each proving the rule fired rather than
the probe failing for its own reasons. Both block; only one turns a black hole into an immediate,
handleable error. macOS's own measurement said the same and added that *"agents' blocked connections
stop hanging too"* (`pf-canary-probe.txt` C3).

**None of this transfers to macOS**, and the reason is structural: there the rules sit on the bridge
and the working shape blocks in **both** directions, so the gateway is *inside* the enforced surface
rather than outside it. macOS must allowlist its gateway explicitly. Reading a Linux hook result
across to pf is how both of this plan's rule-shape errors happened.

---

## Per-backend reach

| Backend | Key available | Notes |
| --- | --- | --- |
| docker | per-network bridge, veth via `physdev`, or a **netdev chain bound to the veth** | measured; names do not recycle over 6 cycles |
| containerd | veth via `physdev` (one shared `yoloai0`), or a netdev chain | **measured on CNI's own veths** 2026-08-11 (`r9`), not extrapolated; CNI names do not recycle over 6 cycles |
| rootless podman | inside its own netns | the host netns cannot reach it at all; enforcement installs in the rootless netns, which the guest cannot enter (`CAP_SYS_ADMIN` absent, netns path invisible). **Veth names recycle here** — needs the lifecycle rule |
| apple | bridge index under one network per sandbox | needs the lifecycle rule below |
| tart | **none reachable** | right in effect, **wrong in its former reason**. tart *does* give each VM its own host-side `vmenetN` in both shared NAT and Softnet — the same shape as `k2`. What fails is pf: a rule on the member interface blocks nothing (counter 0) while the same rule on the bridge blocks **both** VMs (counter 12), and OpenBSD's `received-on` — pf's `physdev` — is a syntax error here, with a control proving the anchor loads (`tart-net-key.txt`) |
| seatbelt | out of scope here | parked, see `seatbelt-host-pf-enforcement.md` |

**The tart row cannot be widened to "VM backends are out of scope."** The obstacle is macOS pf, not
tart's networking, and apple — also a VM backend — is reachable. It also leaves tart's own
`--net-softnet-allow` / `--net-softnet-block` per-VM CIDR lists as the only enforcement surface that
backend has. Nothing in this design mentions them and nobody has tested them.

**`br_netfilter` is unowned, and its absence is silent** (`r8`). The `physdev` variant needs it,
docker only enables it for `icc=false`, and neither CNI nor netavark mentions it at all. It is a
**host-wide** setting that changes packet paths for every bridge on the machine, and with it unloaded
a physdev rule counts zero while looking entirely correct. If `physdev` is the chosen key it needs an
explicit preflight assertion; **the netdev variant removes the dependency instead**, which is the
stronger reason to prefer it.

**Rootless podman's enforcement dies with the last sandbox** — the netns is destroyed and the rules
with it — so installation is per bring-up, before the agent runs.

---

## The lifecycle rule, wherever interface names recycle

An ordinary restart releases a bridge index, a stranger can take it, and **that stranger inherits the
departed sandbox's policy whole** — it reached a destination only the departed sandbox was granted and
was refused its own. First-match-with-`quick` means the stale pass wins before the stranger's own rule
is reached, and it is invisible to inspection. Withdrawing the stale rule restores correct policy
exactly (measured, not advised).

**So: withdraw a sandbox's rule when its interface goes; re-read the real index when it returns.**

**The withdraw half is measured against a live restart** (`pf-lifecycle.txt`). It wins the race by
783 ms: the backend takes 5212 ms to actually release an index — not our cost — detection plus
withdrawal completes 33 ms after release, and a stranger attaches at 816 ms. The control arm, with no
mechanism running, reproduces the inheritance hazard, so the good arm is not a stranger who would
have been fine anyway.

**Do not read that margin as a guarantee.** It is one measurement on an idle host with a 50 ms poll
that was never varied, and the canary this design adopts polls at 320–385 ms. Folding detection into
that loop consumes most of the margin. The mechanism is shown to have won here, not to win by
construction. A killed VM or a daemon restart may release an index differently, and that is the
dangerous case.

**The re-read half could not be exercised at all.** It exists for "a stranger holds the old index, so
the sandbox returns on a different one" — and that case does not arise: a returning sandbox reclaims
its old index *off the incumbent*, which is DF190 seen from the other side. Whether the re-read is
therefore unnecessary or merely untested is undecided, and it should not be built as though the
question were settled.

> **Corrected 2026-08-10 — this is not macOS-only.** This section originally said "Linux needs no
> equivalent because its names do not recycle", which contradicted this document's own note that
> netavark lets the kernel assign the name. Measured (`k3-veth-name-reuse.txt`,
> `k3b-veth-reuse-live-sibling.txt`): over six create/destroy cycles docker produced 6 distinct veth
> names and containerd/CNI produced 6 — neither recycles. **Rootless podman produced `veth0` every
> time.** And the reuse re-points at a *live* sandbox: with A on `veth0` and B on `veth1`, destroying
> A and starting C gave **C the name `veth0`**, so a rule still naming `veth0` now matches a different
> sandbox with different policy. B kept its own name throughout, so the macOS variant where a running
> sandbox's identifier changes underneath it (DF190) does **not** occur here.
>
> **The lifecycle rule is therefore required on rootless podman as well**, for the same reason and
> with the same remedy. It is not required on docker or containerd — bounded by six sequential cycles,
> which is a bound rather than a proof, and concurrent churn was not tested.

> **Corrected 2026-08-11 — `pfctl -k` is withdrawn entirely.** This section required state teardown
> on macOS and named `pfctl -k <guest> -k <gateway>` as the working form. Both statements were true of
> the ruleset they were measured against and false of the one this document now specifies. Revocation
> under the stateless shape is a table delete and nothing else, which is already inside D132's shipped
> grant (`pf-no-state.txt`, `pf-grant-matrix.txt` G3). **The platforms converge: no `-k`, no grant
> widening, and the gateway-pinning tension that pulled against per-sandbox networks dissolves with
> it.** The `-k` recommendation is now a *simplification* of the boundary rather than a cost.

### The macOS price is a rule shape, not a keyword

`no state` alone does nothing, and this is the part that would have been got wrong by reading the
Cilium result across. **Every rule in an interface-keyed design is `in` on the bridge.** The host's
reply travels `out`, matches no rule, and meets pf's default pass — and a passed packet creates
state, which is bidirectional and carries the guest's forward packets past rule evaluation entirely.
Adding a return rule reaches a genuine zero-state census under a live transfer and **revocation still
fails**, because the download direction is never evaluated against a block at all. A sandbox has a
permitted direction regardless of its allowlist.

Both cells of each row measured (`pf-no-state.txt` N2–N4), on the host-terminated gateway path *and*
on the NAT'd external path, each with its own control:

| | no egress block | with egress block |
| --- | --- | --- |
| stateless (`no state`) | survives | **stops** |
| stateful (`keep state`) | survives | survives |

**Neither ingredient is sufficient.** The working shape is a `no state` pass **and** a block, in
**both** directions — four rules per slot, not two. The egress block is the piece this rewrite did
not have: the retired address-keyed draft carried `block return out quick to <src>` and dropping it
was unnoticed. The translated state created on `en0` by rules outside our anchor survives the
revocation and does **not** rescue the flow, which closes the floating-state question.

**`block drop out` is not free.** It denies host-initiated traffic to the sandbox as well as replies,
so **every sandbox's allowlist must contain its own gateway or credential injection breaks.** That is
the same requirement the § *What the forward hook does not cover* section reaches from the Linux
side, arrived at independently — which is the strongest reason to believe it.

**Linux is not known to need the same.** Its revocation was measured with ingress-keyed rules only
and worked, counter and all (`p1b-revocation-decay.txt`). The asymmetry is now the reverse of what
this section used to claim, and it is a difference in hook semantics rather than in capability. But
the macOS finding does raise a Linux question nobody has asked: **inbound packets from a revoked
destination match no rule on Linux either**, and the chain policy is `accept` by design. TCP stalls
because the guest's own ACKs are dropped; a one-way inbound UDP flow has no such lever. Untested.

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
- On macOS, revocation is **a table delete and nothing else** — no `pfctl -k`, no reload — asserted on
  the **egress** block counter incrementing. The ingress counter stays at 0 in the shape that works,
  so asserting on it would be a free negative.
- A sandbox that lands on a bridge index **outside the pinned superset fails closed**, asserted by
  forcing an out-of-range index rather than by reading the preflight's own report. Today it fails
  open silently, which is why this one is on the list before the code exists.
- The **host-service allowlist** permits the broker's injector endpoint and denies everything else on
  the host, asserted with credential injection actually working.
- Enforcement is **reinstalled before the agent runs** on rootless podman, asserted across a
  stop/start of the last sandbox in the netns.

**Every one pairs its negative with a positive control in the same run** (testing-principles §11), and
the control must travel the **same protocol and path** as the test — three runs in this workstream
recorded a reassuring "blocked" that was free because they probed with ICMP and controlled with TCP.

---

## Open questions

- **What does per-packet evaluation cost on pf?** Unpriced at any allowlist size, and the pinned
  superset enlarges the question. The Linux "it costs nothing" result does not transfer.
- **UDP, on both platforms.** Every rule and probe in `pf-no-state.txt` is `proto tcp`, and DNS rides
  on UDP. On Linux the open half is inbound: a revoked destination's packets match no rule and the
  chain policy is `accept`, so a one-way inbound flow has no ACK to strangle.
- **The preflight assertion for the index range** — what yoloAI does when a sandbox lands outside the
  pinned superset. Today's answer is silent non-enforcement.
- **Does the input-hook allowlist hold against a hostile guest?** R7 answered the shape with a
  cooperative `curl`; nothing has tested an agent trying to defeat it. Same for the reply path, which
  R7 assumed leaves via output/postrouting rather than measuring it.
- **The same two questions on rootless podman and containerd.** R7 is docker-only, and rootless
  podman's netns changes what "the host gateway" even means.
- **Does the veth key work on CNI's own veths**, not just docker's? Mechanism should carry; untested.
- **What is the terminal state when we lose the fight** to another firewall manager?
- **Does the `RELATED` omission actually close the helper hole**, measured rather than argued?
- **tart's `--net-softnet-allow`/`-block`**, the one enforcement surface that backend has and the one
  nothing here has touched.
- **DF189's subnet collision.** Interface keying makes it survivable — two sandboxes with the same
  address are still distinguishable — but the routing conflict remains and the subnet should still
  move off `10.89.0.0/16`. `10.0.0.0`–`10.87.255.255` is outside every podman pool and every docker
  local pool.
- **DF190 is Apple's, and now has a mechanism** (`df190-mechanism.txt`). It reproduces with zero rules
  of ours loaded and every anchor asserted empty first, so nothing in this design causes it or can fix
  it. The departing network's vmnet helper stays alive across the stop — `released session
  [allocations=1]`, not a shutdown — and re-allocates on the same network id when its sandbox returns,
  onto an index handed out in the meantime; deleting the network while empty removes the displacement
  entirely. **The workaround is therefore ours: delete the network when its last sandbox goes.** Its
  severity was understated in the safe direction — a stop/start does not recover, it *moves* the
  defect to whichever sandbox holds the index next.

## Related

- [macos-pf-privileged-path.md](macos-pf-privileged-path.md) / [D132](../../decisions/working-notes.md) — the macOS privileged path.
- [D133](../../decisions/working-notes.md) — resolution happens in the guest's context; its central rejected alternative is still owed a re-measurement with `--dns` set.
- [tamper-resistant-network-isolation.md](tamper-resistant-network-isolation.md) — the in-guest layer, which this does not replace.
- [prior-art-egress-enforcement.md](../research/prior-art-egress-enforcement.md) — the first reading pass. **Treat with care:** an audit found it inadequate, and the source-reading round that followed it is where the load-bearing findings came from.
- Raw runs: [`research/linux-enforcement/results/`](../research/linux-enforcement/results/) and [`research/macos-isolation-spike/results/`](../research/macos-isolation-spike/results/), both of which keep their invalidated runs deliberately.
