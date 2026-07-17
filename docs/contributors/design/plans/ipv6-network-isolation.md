> **ABOUTME:** Sizes proper IPv6 support for `--network-isolated` — family-aware resolution and a
> parallel v6 ruleset — and records why both cheap alternatives (disable v6, or deny v6 unfiltered)
> are regressions rather than shortcuts. Closes DF104 and DF134.

# Plan: make `--network-isolated` mean both address families

- **Status:** PLANNED — sized 2026-07-17, not started. Deferred out of v0.9.0 (owner's call): the
  code is small, the *verification* needs a v6-routable network per backend, and a security boundary
  is the wrong thing to rush into a cut.
- **Depends on:** —

## The one-sentence version

`--network-isolated` filters IPv4 only, on every backend that claims it, and says so nowhere
([DF104](../findings-unresolved.md)) — while the same family-blindness makes a single v6 nameserver
abort the whole isolation path ([DF134](../findings-unresolved.md)). Resolve both families, install
the same ruleset twice, and the claim becomes true.

## Why the two cheap routes are wrong, not just cheap

This is the load-bearing section, because both alternatives look like one-liners and DF104 originally
recommended one of them.

**The allowlist is a promise about a destination, and the destination does not choose its family.**
`resolve_domains` requests `AF_INET` only (`runtime/docker/resources/firewall.py:73`). So for a
domain the user *explicitly allowlisted* that resolves AAAA:

| Route | What happens to an allowlisted v6-only domain |
| --- | --- |
| `net.ipv6.conf.all.disable_ipv6=1` | **unreachable** — no v6 stack to reach it with |
| v6 default-deny, v4-only allowlist | **blocked** — the allowlist has no v6 entries to match |
| family-aware (this plan) | **reachable and filtered** — the promise kept |

Both shortcuts make the allowlist silently fail to *allow*, and it surfaces as a network fault rather
than as a policy decision — the user sees a domain they permitted timing out. That is a regression
for legitimate use. Proper support takes away only v6 egress to **non-allowlisted** hosts, which is
the hole DF104 documents, not a feature anyone was promised.

`disable_ipv6` carries a second, unrelated cost: `conf.all` takes `::1` and link-local with it, and
some runtimes reach for v6 loopback. Per-interface disabling (keeping `lo`) is the survivable form —
which is already a wrinkle the "it's one sysctl" framing hides.

## What the work actually is

**The code is the small part.** `apply_firewall` is already a flat sequence of `run_strict` calls, so
this is parameterization, not redesign:

1. `resolve_domains` — `AF_INET` → `AF_UNSPEC`; carry the family alongside each `(domain, ip)` pair.
2. `read_nameservers` — classify each entry by family; route v6 nameservers to the v6 ruleset instead
   of into `iptables -d`, where they abort the run (DF134).
3. `apply_firewall` — parameterize over `(iptables, ipset family inet, icmp-port-unreachable)` and
   `(ip6tables, ipset family inet6, icmp6-adm-prohibited)`, then run it twice. The existing
   ipset-unavailable fallback ladder generalizes unchanged.
4. The v6 REJECT is the same load-bearing final rule, with the same comment: if every prior rule
   installs and this one fails, the sandbox is open on v6.

**Verified present in `yoloai-base:latest` (2026-07-17):** `ip6tables` at `/usr/sbin/ip6tables`, and
`ipset create -exist t6 hash:net family inet6` → exit 0. So the tooling is there **by construction on
the images we build**, which shrinks the "what if v6 filtering is unavailable" branch from a design
crux to a defensive concern for custom base images. An early sizing of this plan had that dilemma as
the centrepiece; checking the image rather than reasoning about it removed it.

**Dual-stack completeness is a correctness requirement, not a nicety.** A domain with both A and AAAA
must land in *both* allowlists. Miss that and Happy Eyeballs picks v6, hits the v6 REJECT, and the
allowlist becomes intermittent — the worst failure shape available, because it depends on resolver
order and looks like a flake rather than a rule.

## Why this parks: the verification, not the code

Four backends declare `NetworkIsolation: true` — **docker**, **podman**, **containerd**, **apple**.
(`tart` and `seatbelt` declare `false` and are out of scope entirely.) Each needs a **v6-routable
test network**, and that is the part nobody has:

- **docker** — needs `--ipv6` plus an explicit subnet on the network.
- **containerd** — needs a CNI with v6 IPAM.
- **podman** — same shape as docker; untested.
- **apple** — vmnet hands out a **ULA** (`fd96::/64`, per DF104's live capture), so testing genuinely
  *routable* v6 may need a v6 uplink, and may not be reachable on this hardware at all.

DF104 parked for exactly this reason and nothing about it has changed. Any estimate that prices the
rules and not the test network is wrong by the ratio between them.

## Least astonishment while it is deferred — **done, 2026-07-17**

Deferring the fix is not a reason to keep the claim, so the claim was corrected first. Landed ahead
of this plan (non-breaking, no flag or key renamed):

- `--network-isolated` help now reads *"IPv4 iptables allowlist; IPv6 is not filtered"* —
  `internal/cli/lifecycle/new.go` and its verbatim mirror `internal/cli/helpcmd/help/flags.md`.
- `internal/cli/helpcmd/help/security.md` carries the caveat in full, including *why* it is not
  currently exploitable and why that is not a guarantee.
- `docs/GUIDE.md`'s cheatsheet comment says IPv4-only.
- All four claiming descriptors carry the scope at the field (`runtime/docker/docker.go`,
  `podman`, `containerd`, `apple`), so a developer reading the capability sees it.
- `design/network-isolation.md` opens with a banner: its guarantees are v4-scoped until this lands.

Still available independently: DF134's one-line mitigation (drop non-v4 nameservers with a log line
rather than aborting). It does not wait for this plan.

## Open questions

1. **Fail-closed on missing v6 filtering.** If `ip6tables` is absent (a custom base image), the
   choices are abort isolation — matching the existing posture, *"abort rather than proceed with
   unenforced isolation"* — or filter v4 only and log, which silently reinstates DF104. The existing
   code fails closed; the burden is on any argument to do otherwise. Cheap on our own images, since
   the tooling is verified present.
2. **Does the guest have v6 at all, and should that be detected?** Installing a v6 ruleset on a guest
   with no v6 stack is harmless but noisy. Detection is easy to get subtly wrong (an interface can
   gain v6 after the rules install), so the default should be to install regardless.
3. **Do the non-Docker paths reach this file?** The rules live under `runtime/docker/resources/` but
   apple runs them in-guest too (DF104 verified iptables inside an apple sandbox). Confirm the actual
   call paths for podman/containerd/apple before assuming one edit covers four backends.
4. **The sidecar.** `install-firewall.py` and the netns-sharing sidecar (the tamper-resistant
   firewall work) install rules from outside the agent container. Whether the v6 pass rides the same
   path unchanged is unverified.

## References

[DF104](../findings-unresolved.md) (the IPv4-only isolation, and its live capture);
[DF134](../findings-unresolved.md) (a v6 nameserver aborts the run);
`runtime/docker/resources/firewall.py` (`resolve_domains:63`, `read_nameservers:84`,
`apply_firewall:109`, `run_strict:44`); `runtime/docker/resources/install-firewall.py` (the sidecar
installer); `runtime/docker/docker.go:53`, `runtime/podman/podman.go:39`,
`runtime/containerd/containerd.go:46`, `runtime/apple/apple.go:61` (the four claimants);
`runtime/tart/tart.go:59`, `runtime/seatbelt/seatbelt.go:38` (declare `false`).
