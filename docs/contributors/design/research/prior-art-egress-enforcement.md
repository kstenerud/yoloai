> **ABOUTME:** What other systems do about the three problems our host-side enforcement design
> hit — address recycling, enforcement liveness, and host-vs-guest DNS. Prior art, not
> measurement: all of it from published docs and issue trackers, none run on our hardware.

# Prior art: how others solve what our enforcement design ran into

**Evidence level, stated up front.** This document is *reading*, not measurement, and it is the only
document in this workstream that is. Everything in
[enforcement-state-reaping.md](../plans/enforcement-state-reaping.md) and
[macos-pf-privileged-path.md](../plans/macos-pf-privileged-path.md) was run on hardware; nothing here
was. Sources are search results and project documentation, summarised — for the items that would
change a decision, read the linked source before relying on it. Treat every claim below as "published
elsewhere", never as "true here".

Three problems drove this: enforcement state keyed on an address that recycles; enforcement that can
silently stop applying to a running sandbox; and a domain allowlist whose resolution moves from the
guest to the host.

## 1. The DNS problem is solved by inverting the direction

Our design resolves allowlisted domains **on the host** and installs the addresses. That produced two
open problems — split-horizon divergence (measured: the guest could not reach the name it was
allowlisted for) and one-shot decay (measured: `github.com` moved within 10 minutes).

**Cilium does the opposite, and it dissolves both.** Its `toFQDNs` policy runs a DNS proxy that
intercepts the *pod's own* DNS queries, observes the response, and populates policy with the
addresses **the pod actually received** — expiring them on the DNS TTL.

- Split-horizon cannot arise, because there is only one answer and it is the guest's.
- Decay is handled structurally: entries live as long as the TTL says, not as long as the sandbox.

This is a direct answer to the question our plans left open, and it points away from where our design
was heading. The cost is a DNS proxy in the path — real, but it replaces "reconcile two resolvers"
with "have one".

## 2. The address-recycling problem: the industry answer is to not key on addresses

Our rule 1 (clear before claim) exists because a recycled address inherits a dead sandbox's policy.
Cilium's documentation states the same premise and draws a stronger conclusion: pod IPs are
"ephemeral by design", so IP-keyed allowlists are "brittle, operationally expensive to maintain, and
unreliable at scale". Their answer is a **security identity** derived from labels; policy is written
against identity, and address→identity is resolved at enforcement time.

Two things transfer, and one does not:

- **Transfers:** the diagnosis. Keying enforcement on a recycled address is a known-bad pattern, not
  an edge case we stumbled on. Our rule 1 is a *mitigation* for it; an identity layer is a *cure*.
- **Transfers:** they name the same race our rule 1 addresses — new workloads must not start before
  every enforcement point has been updated, "as otherwise connection attempts could be mistakenly
  dropped". Our answer is the agent-launch gate; theirs is ordering. Same shape.
- **Does not transfer:** the identity model itself. Cilium's identities come from Kubernetes labels
  across a cluster of nodes. We have one host and a handful of sandboxes, and no label model.

**The lead worth following.** The principle — key on something the guest cannot change and that does
not recycle — may be available to us cheaply on Linux without any of Cilium's machinery: nftables can
match on **`meta cgroup`**, and a container's cgroup is per-instance and not reused the way an address
is. If that works, the recycling hazard is not mitigated but *absent*, and rules 1/1b/1c exist only
for the macOS side. **Unmeasured, and it is the highest-value experiment left in this workstream.**

## 3. Enforcement liveness is a known CVE-class bug, and the fix is detect-and-reinstall

We found, on both platforms, that ordinary unrelated administrative actions can silently void
enforcement for a running sandbox. This is not novel — it is Docker's, it has a security advisory,
and the remedy is established.

- **firewalld issue #1099**, "Do not clear iptables rules when reloading firewalld" — the upstream
  statement of the problem: `firewall-cmd --reload` flushes the whole ruleset and re-applies only
  firewalld's own config, wiping chains other tools added, because it does not know they exist.
- **moby GHSA-x4rx-4gw3-53p4**, "Firewalld reload makes published container ports accessible from
  remote hosts" — the same mechanism as a **security advisory**, with fail-open as the outcome.
  Precisely our hazard, in the largest container runtime, with a CVE identifier attached.
- **moby PR #49443**, "Restore new iptables rules on firewalld reload" — the fix: on reload, Docker
  walks its current networks and asks each to restore its rules.

**So the remedy is a subscription, not a poll.** Docker reacts to the reload signal rather than
periodically checking. That is cheaper and faster than the health-probe shape our plan sketched, and
it is worth designing toward on Linux, where firewalld emits a D-Bus signal. It does not cover every
trigger we measured — `systemctl restart nftables` running a config that begins `flush ruleset` emits
no such signal — so a subscription is a fast path, not a complete one.

### And the correction to my own recommendation

**moby PR #49728, "Stop firewalld reload re-creating rules for deleted networks."** Docker's liveness
fix re-created rules for networks that no longer existed — which is *our reaping problem, appearing
inside their liveness fix*. They shipped the restore, and the restore resurrected stale state.

I had recommended splitting reaping and liveness into separate plans on the grounds that they are
different subjects. **That split is where this bug comes from.** "Restore what should be enforcing"
and "reclaim what should not" are the same question asked in two directions, and a restore path that
does not consult the reaper will faithfully rebuild rules for dead sandboxes. They must be designed
against one model of *what is currently live*, whatever documents they live in.

## 4. On macOS, the sanctioned mechanism differs per backend

Since Big Sur, application-level firewalls on macOS are expected to use **NetworkExtension**
(`NEFilterDataProvider`), which is what Little Snitch and LuLu use; it provides per-flow verdicts on
TCP/UDP. Relevant qualifications from the same reading: it filters *flows*, not packets, and it sits
**above** pf — "if pf blocks a packet the application firewall will never see it". There is also a
documented history of Apple's own applications being exempted from network filters and VPNs.

For our backends this splits, and D132 should say so rather than implying one answer:

- **tart / apple** — the target is a VM with its own address, filtered by address at the packet layer.
  pf is the right layer and D132 stands. NetworkExtension would be a heavier route to a weaker place.
- **seatbelt** — the target is a *host process group*, not an address, which is why that half needed
  the dedicated-gid machinery. This is exactly what `NEFilterDataProvider` is for, and it is the
  sanctioned mechanism where the gid approach is a workaround. Against that: a system extension needs
  notarization and explicit user approval, which is a much larger install-time ask than a sudoers
  grant. `seatbelt-host-pf-enforcement.md` is already **parked**; this gives that parking a reason
  better than "not worth building yet", and a route if it is ever unparked.

## 5. The dual-firewall idea is mainstream, and the endpoint is a proxy

Belt-and-braces — filtering inside the sandbox *and* outside it — is what the peer group does, not a
last resort. Published agent-sandbox designs pair in-container `iptables` (forcing traffic through a
proxy) with host or cloud-level egress control.

But the stronger pattern in that same reading is that the outer layer is an **egress proxy**, not an
IP filter: agent VMs with no external interface at all, talking over a virtual socket to a host-side
proxy that enforces an allowlist by *hostname* and injects credentials, so the sandbox never holds a
token. That sidesteps the entire DNS-versus-IP problem, because a proxy sees names.

**This is where yoloAI's own roadmap already points.** The credential broker (D105/D106) is built and
shipped, `egress-proxy-build.md` calls the current work "step 1.5", and
[tamper-resistant-network-isolation.md](../plans/tamper-resistant-network-isolation.md) already names
a hostile-grade SNI proxy as the destination. The prior art says that destination is the right one and
that IP filtering is the interim layer — which is what we are building. Nothing here says stop; it
says the interim layer should not acquire more complexity than an interim layer deserves.

## What this changes

1. **Consider guest-side resolution or a DNS proxy** instead of host-side resolution. It is the
   established answer and it closes two measured problems at once.
2. **Test `meta cgroup` matching on Linux.** If policy can key on something that does not recycle,
   the hazard is absent rather than mitigated.
3. **Do not design liveness and reaping apart** — moby #49728 is the bug that split produces.
4. **Prefer a reload subscription over a poll** where the platform emits one, with a slower check
   behind it for the triggers that emit nothing.
5. **Say per backend which macOS mechanism is sanctioned**, in D132.
6. **Keep the in-sandbox layer** rather than replacing it. Defense in depth is the norm here, and the
   outer layer is the one that can be silently voided.

## Sources

- [FQDN and DNS Proxy — cilium/cilium (DeepWiki)](https://deepwiki.com/cilium/cilium/9.2-fqdn-and-dns-proxy)
- [Standalone DNS Proxy — Cilium docs](https://docs.cilium.io/en/stable/security/standalone-dns-proxy/)
- [Identity-Based — Cilium docs](https://docs.cilium.io/en/stable/security/network/identity/)
- [Layer 3 Policies — Cilium docs](https://docs.cilium.io/en/stable/security/policy/layer3/)
- [firewalld#1099 — Do not clear iptables rules when reloading firewalld](https://github.com/firewalld/firewalld/issues/1099)
- [moby GHSA-x4rx-4gw3-53p4 — Firewalld reload makes published container ports accessible](https://github.com/moby/moby/security/advisories/GHSA-x4rx-4gw3-53p4)
- [moby#49443 — Restore new iptables rules on firewalld reload](https://github.com/moby/moby/pull/49443)
- [moby#49728 — Stop firewalld reload re-creating rules for deleted networks](https://github.com/moby/moby/pull/49728)
- [Strict Filtering of Docker Containers — firewalld](https://firewalld.org/2024/04/strictly-filtering-docker-containers)
- [Filter and tunnel network traffic with NetworkExtension — WWDC25](https://developer.apple.com/videos/play/wwdc2025/234/)
- [NEFilterDataProvider vs NEFilterPacketProvider — Apple Developer Forums](https://developer.apple.com/forums/thread/128228)
- [Apple Apps Exempt From Network Filters and VPNs — Michael Tsai](https://mjtsai.com/blog/2020/10/22/apple-apps-exempt-from-network-filters-and-vpns/)
- [Securely deploying AI agents — Claude Code docs](https://code.claude.com/docs/en/agent-sdk/secure-deployment)
- [Restricting network access for AI coding agents with a proxy allowlist — INNOQ](https://www.innoq.com/en/blog/2026/03/dev-sandbox-network/)
