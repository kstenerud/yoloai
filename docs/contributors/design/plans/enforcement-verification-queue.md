> **ABOUTME:** Everything in the host-side enforcement workstream that is still a claim rather
> than a measurement, split by the hardware that can settle it. One Linux pass and one macOS
> pass should clear it. Records what to run and what each result decides, not code.

# Enforcement verification queue — Linux and macOS

- **Status:** IN-PROGRESS — queue opened 2026-08-08 after the prior-art pass. Nothing run yet.
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

### L4 — Does address recycling behave the same on podman and containerd?

Only docker was measured (lowest-free, immediate reuse). podman and containerd allocate differently
and containerd goes through CNI IPAM.

**Decides:** whether "clear before claim" needs per-backend handling, and whether two backends on one
host can ever hand out the same address — which would make a blind cross-scrub delete a *live*
sandbox's entry. Cheap.

### L5 — Is the IPv6 hole live on Linux?

DF104: `grep -rn ip6tables` returns nothing repo-wide. macOS found a live v6 hole on apple. Whether a
docker guest here holds a routable v6 address at all is unmeasured.

**Decides:** whether IPv6 filtering is urgent on Linux or latent. Cheap.

### L6 — Is `nft -f` atomic in the way the design assumes?

The plan claims the whole acquisition sequence can be one atomic transaction, which is what removes
macOS's step-ordering hazard. Asserted from documentation, not observed.

**Decides:** whether the Linux acquisition path can skip the ordering rules entirely. Cheap.

### L7 — Hook priority against docker's own chains

Our base chain ran at `priority -10` in the probes and enforced. Whether that is the *right*
priority relative to docker's rules — rather than one that happened to work — is unexamined.

**Decides:** the shipped hook priority, and whether docker's rules can pre-empt ours.

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
