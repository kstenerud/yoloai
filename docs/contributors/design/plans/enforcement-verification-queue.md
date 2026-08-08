> **ABOUTME:** Everything in the host-side enforcement workstream that is still a claim rather
> than a measurement, split by the hardware that can settle it. One Linux pass and one macOS
> pass should clear it. Records what to run and what each result decides, not code.

# Enforcement verification queue — Linux and macOS

- **Status:** IN-PROGRESS — queue opened 2026-08-08 after the prior-art pass. **Linux pass complete
  (L1–L7, 2026-08-08); macOS pass (M1–M8) outstanding.** The shared design documents are deliberately
  untouched until both halves are in — see "Running both passes in parallel".
- **Depends on:** enforcement-state-reaping.md, macos-pf-privileged-path.md

**Working model.** Each item names what to run, what it *decides*, and what it costs. An item is
done when a raw run lands in `research/macos-isolation-spike/results/` (macOS) or an equivalent
Linux results file, and the owning plan is updated with the outcome — including when the outcome is
"no effect", which is a result and not a non-event.

**Two method rules bind every item, both learned here the hard way.**

- **Every negative needs a positive control in the same run.** "Blocked" in a sandbox with no
  network is free, and it silently invalidated the first `pf` harness run (A22) and one macOS
  fail-closed claim that had to be retracted a day later.
- **A negative result states what was tried.** "Nothing wiped the anchor" is a much weaker claim
  than it reads unless it names the seven things attempted, as `pf_midlife_wipe.sh` does.

## Running both passes in parallel

They are largely independent, and the way to keep them so is to **make the write surfaces disjoint
by role** rather than hope the diffs miss each other. This is the model
[mac-verification-queue.md](mac-verification-queue.md) already used: the queue holds
verification-only tasks — run it, record the result — and design changes are somebody else's commit.

**Each pass writes only:**

- its own raw runs — `research/macos-isolation-spike/results/` for macOS, a separate
  `research/linux-enforcement/results/` for Linux, so there is no shared index to append to;
- its own harness scripts;
- **its own item's outcome line in this file**, and nothing else in this file.

**Neither pass edits the shared design documents.** `enforcement-state-reaping.md`,
`macos-pf-privileged-path.md` and D132 get updated in **one synthesis commit after both passes
land**. That is not only conflict avoidance — see below.

**Four collision points, in descending nastiness:**

1. **Rationale-ID allocation, which is not a merge conflict but a duplicate.** `scripts/next-id.sh`
   scans every local and remote-tracking ref, so the window is only between two allocations with no
   fetch in between — and two agents working at the same time is exactly that window. **`git fetch`
   immediately before allocating**, or better, have one side allocate a block for both up front. The
   ascending-order rule makes a collision surface as a rebase conflict rather than silently, but
   that is detection, not prevention.
2. **`findings-unresolved.md`** — appended at the end by both, so two new entries conflict. That is
   working as designed (it is what makes 1 detectable), but it means filing findings from both
   passes concurrently guarantees a rebase.
3. **This file** — both marking items done. Trivial to resolve, frequent if edited item-by-item.
4. **The shared design docs** — the real one, and the reason for the synthesis rule above.

**The bigger risk is not git.** L1/M1 are the same question asked on two platforms; so are L2/M2 and
L3+M3+M4. Whoever finishes first would otherwise rewrite a shared design section from **half the
evidence** — and a negative on M1 changes what a positive on L1 means. Hold the design edits until
both halves are in; the queue's *What the results feed* section pairs them for exactly this reason.

---

## Linux

### L1 — Can policy key on `meta cgroup` instead of the source address? **(highest value)**

Everything in `enforcement-state-reaping.md` rules 1/1b/1c exists to make address-keyed enforcement
safe under recycling. Cilium's answer to the same problem is to not key on addresses. nftables can
match `meta cgroup`, and a container's cgroup is per-instance.

Run: an nft rule matching `meta cgroup` for a live container, with a second container as the
negative control. Then destroy and recreate to see whether the cgroup id is reused the way
`172.17.0.2` is.

**Decides:** whether rules 1/1b/1c are needed on Linux at all, or become macOS-only. If the cgroup
does not recycle, the hazard is *absent* here rather than mitigated. Cheap.

**Outcome (2026-08-08): negative, and it closes the direction.** `results/l1-cgroup-key.txt`,
`results/l1b-cgroup-prerouting.txt`. The kernel **refuses** `socket cgroupv2` in the `forward` and
`postrouting` hooks outright — "Operation not supported" at rule-load time — and container egress
is forwarded traffic. `prerouting` accepts the rule and then never fires it, on packets the same
chain provably sees (address counter 3, cgroup counter 0). `meta cgroup` reads the cgroup-v1
`net_cls` classid, which does not exist under unified cgroup v2. The match itself is fine where the
kernel allows it: in the `output` hook it counted 3 packets from the probe cgroup and 0 from outside
it. Confirmed alongside: the cgroup path does **not** recycle, and the address recycles immediately
— the next container took `172.17.0.2` back. So the lead is real about the key and unavailable
about the hook, and **rules 1/1b/1c stay on Linux.**

### L2 — Does split-horizon DNS break the allowlist on Linux too?

macOS reproduced it end to end: host and guest resolve one name differently, the host's answer goes
into the allowlist, the guest cannot reach the name it was allowlisted for. The macOS write-up says
it "applies to Linux equally — a container has its own `resolv.conf` too". Unverified here.

Run: the `/etc/hosts` divergence trick against a docker sandbox, host answer installed as the design
would, guest reachability measured both ways.

**Decides:** whether host-side resolution is viable anywhere, or whether the DNS-proxy direction is
forced on both platforms. Cheap, and it is the other half of the biggest open design question.

**Outcome (2026-08-08): reproduced, with no simulation needed, plus a second failure.**
`results/l2-split-horizon-dns.txt`. The divergence is docker's shipped default on any
systemd-resolved host: the host resolves through the loopback stub to the LAN resolver, and docker
— unable to hand a container a loopback nameserver — substitutes public DNS. `yoloai.tail571a40.ts.net`
resolves to `192.168.111.33` on the host and is NXDOMAIN in the guest. Both controls held
(`example.com` reachable by name and address; `1.1.1.1` denied). Two results, not one:
**(1)** the guest cannot reach the name it was allowlisted for — fail-closed and functional, as on
macOS; **(2)** the host's answer wrote **the host's own LAN address** into the guest's allowed set,
and the guest then completed a TCP connection to it. That second one is a widening that *happened*,
not one inferred: packet `172.17.0.2 → 192.168.111.33:22`, forward hook, matched the `@allowed`
accept. Host-side resolution is not viable on Linux either.

### L3 — What do `ufw` and `firewalld` do to a custom nft table?

Measured so far: `/etc/nftables.conf` begins `flush ruleset`, so `systemctl restart nftables`
destroys everything. **`ufw` is enabled on this host and was never tested**, and firewalld is not
installed but is the RHEL/Fedora/SUSE default.

Run: `ufw reload`, `ufw disable`/`enable`, and — in a VM or container with firewalld — a
`firewall-cmd --reload`, each against a live enforcing sandbox, judged on rules, membership and
egress in both directions.

**Decides:** the trigger list the liveness detector must cover, and whether a reload *subscription*
(moby #49443's approach) covers enough of them to be the fast path.

**Outcome (2026-08-08): measurement inverted the reading.** `results/l3-firewall-manager-triggers.txt`,
`l3b`, `l3c`, `l3d`. Nothing a firewall manager did touched a dedicated `inet` table of ours:
`ufw enable`/`reload`/`disable` and a full `docker restart` all left it present *and* enforcing
(checked with live packets, not just `nft list`), and firewalld 2.2.3 on its nftables backend left it
intact across `--reload`, `--complete-reload` and a full restart.

**The variable is not whether a manager exists, it is whether you share its table.** Switching
firewalld to its iptables backend and installing a foreign chain plus a `FORWARD` jump — docker's
exact `DOCKER-USER` shape — a single `firewall-cmd --reload` destroyed both the chain and the jump,
while firewalld rebuilt its own 30 chains. That is the moby CVE mechanism, reproduced, and it lands
on tools that write into the shared `filter` table. Our design does not.

**Consequence for liveness:** a reload *subscription* is much less valuable than moby #49443 implies
for us, because the reloads do not reach us. What remains is the whole-ruleset class — `nft flush
ruleset`, as `/etc/nftables.conf` opens with — which is table-agnostic and emits no signal at all. So
if liveness detection is needed on Linux it is a probe, not a subscription. Note that
`nftables.service` is **disabled** on the test host, making that trigger latent rather than live
here; it was not fired, because it would have taken the SSH session's networking with it.

### L4 — Does address recycling behave the same on podman and containerd?

Only docker was measured (lowest-free, immediate reuse). podman and containerd allocate differently
and containerd goes through CNI IPAM.

**Decides:** whether "clear before claim" needs per-backend handling, and whether two backends on one
host can ever hand out the same address — which would make a blind cross-scrub delete a *live*
sandbox's entry. Cheap.

**Outcome (2026-08-08): three different behaviours, and one backend that cannot be enforced at all.**
`results/l4-address-recycling.txt`, `l4b`, `l4c`.

- **docker** — lowest-free, immediate reuse. Destroy a sandbox and the next one takes its address
  back straight away. This is the case rule 1 was written for, and it is real.
- **containerd** (CNI host-local IPAM) — allocates *forward*, not lowest-free: `10.4.0.2` freed, next
  allocation `10.4.0.4`, then `.5`. That delays recycling rather than preventing it; host-local wraps
  at the end of its range, which this run did not reach and so did not verify.
- **rootless podman** — bridge addresses are distinct and also allocate forward, but **none of it is
  visible to the host**. There is no host route to the rootless bridge; egress is NAT'd by a
  `slirp4netns` process into **the host's own source address** and appears in the `output` hook, not
  `forward`. Measured: the container's address counter in `forward` stayed at 0 while the host-address
  counter in `output` took all four packets.

**So the gap is bigger than recycling.** `output` is the one hook where the L1 cgroup key *is*
available — but the egress process is shared: 1, 2 and 3 running sandboxes all had exactly **one**
`slirp4netns`, whose cgroup names the rootless network namespace, not a sandbox. The sandboxes' own
cgroups are distinct and do not originate the host-visible packets. **On rootless podman no
per-sandbox key exists at the host level by any means measured** — not the address, not the cgroup.
Host-side enforcement cannot single out one rootless podman sandbox, or tell its traffic from the
host's own.

**Cross-backend collision:** none in the default configuration — docker `172.17/16`, containerd
`10.4.0/24`, yoloAI's own bridge `10.89/16` are disjoint, so a live address identifies at most one
sandbox. That is a property of the shipped subnets, not a guarantee; operators can overlap them.

### L5 — Is the IPv6 hole live on Linux?

DF104: `grep -rn ip6tables` returns nothing repo-wide. macOS found a live v6 hole on apple. Whether a
docker guest here holds a routable v6 address at all is unmeasured.

**Decides:** whether IPv6 filtering is urgent on Linux or latent. Cheap.

**Outcome (2026-08-08): latent on this host, but the rule semantics are confirmed — and the run
turned up a worse gap.** `results/l5b-ipv6-hole.txt`, `l5c`. The host has no route to the v6
internet and docker's daemon has v6 off by default, so nothing is exposed here today. The
family semantics are not in doubt though: in an `inet` chain an `ip saddr` rule counted 6 IPv4
packets and **0** IPv6 packets, while an `ip6 saddr` rule counted the 6 v6 packets. Every allowlist
rule the design writes is `ip saddr`-shaped, so a guest that does hold a routable v6 address is
simply unfiltered. Enable v6 on the daemon and the hole is live.

**The bigger find, which L5's failed attempts led to.** `br_netfilter` is not loaded, so traffic
between two sandboxes **on the same bridge is switched, not routed, and never enters the forward
hook at all**. Measured with the strongest policy the design can express — drop everything from A:
A→internet was blocked and the counter moved, and A→B on the same bridge stayed **reachable**. Host-
side nftables cannot contain sandbox-to-sandbox traffic on a shared bridge, and the default docker
bridge is shared by every container on it.

**yoloAI does share one bridge, on both Linux backends.** `runtime/docker/docker.go` translates
network mode `isolated` to the *default* bridge, with the comment that isolation "is implemented via
iptables inside the container (entrypoint.py), not by Docker's network layer"; `runtime/containerd/
cni.go` puts every sandbox on one `yoloai0` / `10.89.0.0/16` CNI network. So the topology this was
measured on is the shipped one.

**Which makes the layer comparison the useful result** (`results/l5d-layers-compared.txt`). Measuring
each layer *alone*, against a baseline where all three destinations are reachable:

| | allowlisted | denied | sibling sandbox |
| --- | --- | --- | --- |
| host nftables only | reachable | blocked | **reachable** |
| in-guest iptables only | reachable | blocked | **blocked** |

The in-guest layer catches what the host layer does not see, because a sandbox's own `OUTPUT` chain
sees all of its traffic regardless of whether the packets are ever routed.

> **Corrected 2026-08-08 by L8.** The paragraphs above originally said the host layer
> *structurally* cannot see this traffic, and concluded that it "is not a superset of the in-guest
> one and cannot replace it". That was too strong and the second half is wrong as stated. What was
> measured is a **default**: `br_netfilter` is available on the test host and simply not loaded.
> Load it, set `bridge-nf-call-iptables=1`, and the host layer blocks sibling traffic — see L8. The
> surviving claim is narrower: *as shipped and as configured by default*, the host layer does not
> see it, and closing that requires a host-wide module and sysctl that yoloAI does not currently set.

### L6 — Is `nft -f` atomic in the way the design assumes?

The plan claims the whole acquisition sequence can be one atomic transaction, which is what removes
macOS's step-ordering hazard. Asserted from documentation, not observed.

**Decides:** whether the Linux acquisition path can skip the ordering rules entirely. Cheap.

**Outcome (2026-08-08): confirmed, and it is stronger than the plan assumed.**
`results/l5-l6-l7-kernel-assumptions.txt`, `l6b`. A file whose last rule is syntactically invalid
lands nothing; so does one whose last rule the *kernel* rejects rather than the parser — the failure
mode is the same and the table does not appear at all. Replacing a live set's membership with
`flush set` plus a repopulate in one file is a single transaction, so there is **no window in which
the set is empty**. And a replacement that fails leaves the previous membership in force rather than
an empty set, so the failure is closed rather than open. The Linux acquisition path can be one
transaction and skip the ordering rules the macOS pf path has to sequence by hand.

Noted in passing, because it misleads: since the whole file is one transaction, a single bad rule
makes `nft` report the error against *every* rule in it. During L1 that made a valid `ip saddr` rule
look unsupported.

### L7 — Hook priority against docker's own chains

Our base chain ran at `priority -10` in the probes and enforced. Whether that is the *right*
priority relative to docker's rules — rather than one that happened to work — is unexamined.

**Decides:** the shipped hook priority, and whether docker's rules can pre-empt ours.

**Outcome (2026-08-08): the priority barely matters; the asymmetry does.**
`results/l5-l6-l7-kernel-assumptions.txt`. Docker's chains all sit at `priority filter` (0). Our
chain enforced a drop identically at **-10, 0 and +10** — the choice of priority is not what makes
containment work, because a drop ends the packet wherever it happens.

**What is not symmetric is accept.** A drop at a lower priority number wins outright; an accept only
ends *our* chain, and every later chain still runs and can still drop. Demonstrated by accepting
everything from the guest at -10 and dropping it from another chain at 100: egress was blocked. So
`-10` is the right place to *deny* from, and no priority anywhere lets our allowlist *guarantee*
reachability — anything from docker to an operator's own rules can still deny what we permit. The
design should describe our table as a denier, never as a grant.

---

## Linux — round 2

Opened 2026-08-08 from what round 1 turned up. L8 exists because round 1 overstated a conclusion;
the rest are questions round 1 raised and did not answer.

### L8 — is the sibling-traffic gap structural, or just unloaded? **(a correction)**

L5c/L5d concluded the host layer "structurally cannot" see traffic between sandboxes on one bridge.
`br_netfilter` is present on this host and merely not loaded, which makes that claim unsafe.

**Outcome: it is a default, not a limit.** `results/l8-br-netfilter.txt`. Same containers, same
policy, nothing else changed: with the module unloaded the sibling was reachable; after
`modprobe br_netfilter` and `bridge-nf-call-iptables=1` it was blocked, and the host chain's drop
counter moved 3 → 8, so the chain genuinely saw and dropped those packets. Controls held throughout
(allowlisted reachable, denied blocked). **The host layer can be made to see sandbox-to-sandbox
traffic.** The cost is that both settings are host-wide rather than per-sandbox, and yoloAI sets
neither today. The L5 outcome above carries the correction.

### L9 — does the shipped sidecar resolve in a different context than the agent it binds?

`install-firewall.py` resolves the allowlist in the sidecar and installs rules that bind the agent,
asserting in its docstring that `--network container:<id>` shares the resolver view.

**Outcome: the assertion holds, and the hypothesised divergence does not exist.**
`results/l9-sidecar-resolver-context.txt`. `/etc/resolv.conf` is shared, and so — contrary to what
prompted the item — is `/etc/hosts`: an `--add-host` entry written for the agent was visible in the
sidecar, and both resolved it to the same address. There is no sidecar-vs-agent split-horizon.

**But the run's own positive control diverged**, which was the real find: two resolutions of
`example.com` moments apart returned different addresses. That is CDN rotation, not context
divergence, and it applies to any one-shot resolution. So L9b asked whether the *shipped* allowlist
is exposed to it. `results/l9b-allowlist-set-stability.txt`: sampling all five domains from
`internal/agent/agent.go:385` six times over a minute, the four that resolve are single-address and
did not move (`api.anthropic.com`, `claude.ai`, `platform.claude.com` all → `160.79.104.10`;
`sentry.io` → `35.186.247.156`). The shipped allowlist is not exposed today; a user-supplied
CDN-fronted domain would be, and `example.com` is the demonstration.

**Two incidental findings.** `statsig.anthropic.com`, a shipped allowlist entry, does not resolve
publicly and this host's resolver answers `0.0.0.0` for it — the standard DNS-blocking answer for a
telemetry host. `firewall.py`'s `is_ipv4()` accepts `0.0.0.0`, so it reaches `iptables -d`. That is
**not** a widening: `results/l9c-zero-address-allowlist.txt` shows iptables normalising it to
`0.0.0.0/32`, with `1.1.1.1` and `example.com` both still blocked. The entry is inert, and the
failure is closed — an allowlisted domain the agent silently cannot reach, with only a log line.

### L10 — does connection state outlive a recycled address? **(the significant one)**

Every rule shape here accepts `ct state established,related`, and docker hands a freed address
straight to the next sandbox. Rule 1 clears rules; it does not clear conntrack.

**Outcome: TCP is safe, UDP is not, and a new sandbox can inherit a dead one's authorisation.**

- **TCP** (`results/l10-conntrack-recycling.txt`): state does outlive the sandbox, but a cleanly
  closed flow lingers as `TIME_WAIT`, and a SYN reusing the dead tuple is classified `NEW` and falls
  through to the allowlist. 8 packets hit the deny rule, 0 hit `established`. No inheritance.
- **UDP** (`results/l10c-udp-residue.txt`): UDP has no close handshake, so the entry simply persists.
  Sandbox A queried `1.1.1.1:53` from port 54323 while allowlisted for it; A was destroyed; B took
  the same address with an allowlist containing **neither** the destination nor port 53. A query from
  a fresh source port was denied — 1 packet on the deny counter. **The same query reusing A's source
  port was answered — 1 packet on the `established` accept.** The counters name the rule that
  decided.

Packet, path, enforcement point: UDP `172.17.0.2:54323 → 1.1.1.1:53`, over `docker0` through the
forward hook, crossing the `ct state established,related accept` rule by matching a conntrack entry
created by a different, already-destroyed sandbox. The window is bounded by the UDP timeout (tens of
seconds) and docker recycles addresses immediately, so the windows overlap. It requires reusing the
prior flow's 5-tuple, which a hostile agent in B can simply search — the space is one 16-bit port
against a guessable destination, and a prior DNS flow to a common resolver is the likely case.

**Consequence: "clear before claim" must clear conntrack for the address, not only rules.**

### L11 — does CNI host-local wrap and reuse?

L4 said containerd "delays recycling rather than preventing it" and explicitly did not verify it.

**Outcome: it wraps.** `results/l11-l12-cni.txt`. On a `/29`, host-local allocated forward to the end
of the range and then handed the freed `10.66.0.2` straight back, repeatedly. containerd needs
clear-before-claim exactly as docker does; the only difference is how many sandboxes it takes.

### L12 — is DF9 still live?

**Outcome: its symptom is absent here, but the run found live stale state instead.**
`results/l11-l12-cni.txt`, `l12b`. The CNI firewall plugin did install `CNI-FORWARD` accepts naming
each container, so the silent no-op did not reproduce on this host — corroboration only, since this
used nerdctl's own network rather than yoloAI's conflist.

What it did surface is round 1's hazard, live on this machine: `CNI-FORWARD` still carries an
unconditional `-s 10.89.0.2/32 -j ACCEPT`, and host-local still holds a 6-day-old reservation file
for that address, while the `yoloai0` bridge **does not exist** and no sandbox is running. A blanket
accept with nothing behind it is precisely what rule 1 is for. Note the two leaks partly cancel — the
stale IPAM reservation stops the address being reallocated — so the inheritance needs a reaper that
clears IPAM without clearing the rule. `system prune` reaps CNI IPAM (D114), which is that shape.

---

## macOS

### M1 — Is there any non-address key for a VM's traffic?

L1's counterpart. pf can match `group` (gid), which is what the seatbelt design uses, but a tart or
apple VM's traffic arrives over vmnet with no process identity attached.

**Decides:** whether macOS is *structurally* stuck with address keying. If it is, that is the reason
rules 1/1b/1c stay in the design, and it is worth stating as a measured constraint rather than an
assumption. If some stable key exists, the two platforms converge.

### M2 — Can the guest's DNS be intercepted on apple and tart?

The DNS-proxy direction (Cilium's `toFQDNs` shape) needs the guest's own queries to be observable or
redirectable. The vmnet gateway already forwards DNS, so the question is whether yoloAI can interpose
without owning the gateway.

**Decides:** whether guest-side resolution is available on macOS, which is the difference between
"fix DNS properly" and "validate host answers and accept the gap".

### M3 — How is the unevaluated-anchor state detected cheaply?

The worst finding of the last pass: the anchor holds correct rules, membership is right, pf is
enabled, and pf never descends into it because the main ruleset's `anchor "com.apple/*"` line is
gone. Every start-path check passed.

Run: find the cheapest reliable probe that distinguishes "pf evaluates our anchor" from "our anchor
is correct but unreferenced" — a `pfctl -s rules` inspection of the main ruleset, or a live probe
packet, and what each costs on the start path.

**Decides:** what verification actually has to do on macOS, and whether it is affordable per start
now that `block return` makes a probe cost a round trip instead of a timeout.

### M4 — Does macOS emit any signal when the main ruleset changes?

Linux can subscribe to firewalld's reload. If macOS has no equivalent, the liveness detector must
poll, and its interval is a design parameter rather than an implementation detail.

**Decides:** subscription vs polling on macOS, and the polling interval if forced.

### M5 — What does a complete uninstall actually leave behind?

`pfctl` has no verb that removes an anchor; two research anchors were still loaded three days later.
The sudoers grant and the pinned ruleset file survive a reboot, and D132 designs an install with no
uninstall.

Run: install the grant, run a sandbox, then remove yoloAI as a user would, and enumerate what
remains: anchor, tables, sudoers file, pinned ruleset.

**Decides:** the content of a D132 uninstall amendment — a security-boundary change, so it amends the
decision record and not just the plan.

### M6 — Is `NEFilterDataProvider` viable for seatbelt, and at what install cost?

The sanctioned macOS mechanism for filtering a *host process group* is a NetworkExtension system
extension, not pf with a dedicated gid. Against it: notarization, a Developer ID, and explicit user
approval.

**Decides:** whether `seatbelt-host-pf-enforcement.md` stays parked for the reason currently given,
or is unparked toward a different mechanism. Expensive — timebox it to establishing the install
ceremony, not building anything.

### M7 — Confirm the IPv6 hole on tart

Found live on apple. tart unmeasured.

**Decides:** whether the v6 gap is per-backend or universal on macOS. Cheap.

### M8 — Does pool size scale acquisition cost linearly?

329ms at 32 slots, 9.3ms per `sudo` call, 85% of which is `sudo` rather than `pfctl`. Linearity is
assumed, so the "shrink the pool" lever is assumed too.

Run: the same acquisition sequence at 8 and 16 slots.

**Decides:** the pool-size/start-latency trade with real numbers on both axes, which is an owner
decision and currently has numbers on only one.

---

## What the results feed

- **L1 + M1** decide whether address keying is a Linux problem, a macOS problem, or both — and
  therefore how much of `enforcement-state-reaping.md` survives.
- **L2 + M2** decide the allowlist primitive: host-side resolution with validation, or a DNS proxy.
- **L3 + M3 + M4** decide the liveness design, which must be built against the same model of "what is
  currently live" as reaping — moby #49728 is the bug that comes from designing them apart.
- **M5** decides the D132 amendment.
- **M8** decides the pool size, which is the owner's call.
