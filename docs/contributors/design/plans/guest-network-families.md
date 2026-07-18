> **ABOUTME:** Design for guest network address-family parity — the guest gets whatever families
> (IPv4/IPv6) the host has, chooseable by default-or-explicit policy, with isolation filtering
> every present family and impossible asks failing loud.

# Guest network families (host parity)

- **Status:** PLANNED — vision set by the owner (2026-07-18); the policy surface and per-backend
  provisioning are designed below with the real decisions marked open.
- **Depends on:** —
- **Rides:** **breaking** — giving a guest IPv6 changes what egress a non-isolated sandbox has, and
  enabling family-aware isolation changes the `--network-isolated` contract. Which half is breaking
  depends on the default policy chosen below.

## The principle

Whatever network families are available to the **host** should be **potentially available to the
guest**. If the host has working IPv6, a sandbox can have IPv6; if it doesn't, the guest is IPv4-only.
On top of that:

1. **Chooseable** — a default policy, plus an explicit way to force a family **on** (enterprise that
   requires IPv6) or **off** (someone who doesn't want IPv6 in their sandboxes at all).
2. **Fail loud on the impossible** — asking for something that can't be delivered (IPv6 firewall
   rules where there's no IPv6, IPv6 egress where the host has none) is an **error**, never a silent
   degrade to a weaker posture. (This generalizes the fail-closed call the owner made for the
   isolation layer, 2026-07-18.)

## Why this exists (what the IPv6 investigation actually found)

Sizing "add IPv6 to `--network-isolated`" (DF104) surfaced that the framing was wrong. The guest
has no IPv6 today **not by policy but by omission**, and it differs by backend:

| Backend | Guest families today | Why |
| --- | --- | --- |
| docker | IPv4 only | attaches to Docker's **default bridge** (`NetworkMode=""`), which has no v6 unless configured; yoloai creates no network of its own |
| podman | IPv4 only | same shape as docker |
| containerd | IPv4 only | CNI subnet is a hardcoded v4 `10.89.0.0/16` (`cni.go`), with an explicit "not IPv4" guard |
| apple | IPv4 **+ IPv6 (ULA)** | macOS `vmnet` hands the guest an `fd…/64` and a v6 default route with no yoloai involvement |
| tart / seatbelt | host-shared | run under host networking; families are the host's by construction |

So the DF104 leak (v6 egress escaping the allowlist) is real **only on apple** — the one backend where
the guest already has v6 — and there it is unfiltered. On the Linux backends there is no guest v6 to
leak, so the "add v6 rules" work would be pure defense-in-depth there and un-verifiable (this dev host
has no v6 uplink at all). The right unit of work is therefore not "filter v6" but "**decide what
families the guest has, provision them, then filter what's present**." yoloai does **not** disable v6
anywhere (no `disable_ipv6` sysctl); it simply never enables it.

## The design

Three layers, each with a fail-loud seam.

### 1. Policy — what families should this guest have?

Inputs: the host's available families (probe), the sandbox's isolation mode, and an explicit user
choice. Proposed resolution (an **open decision**, see below):

- **Default:** the guest inherits the host's **usable** families — v4 always; v6 iff the host has a
  routable/usable v6 stack. This is the "host parity" default and matches what apple already does.
- **Force on** (`--network-families=v4,v6` or a `network_families` config key): require these families;
  if the host can't provide one, **fail loud** rather than quietly omit it.
- **Force off** (`--network-families=v4`): the operator who doesn't want IPv6 in sandboxes at all;
  the guest gets no v6 even if the host has it.

Open: the exact flag/config surface, and whether the default is really "inherit host" or the more
conservative "v4 only unless asked" (inherit-host is friendlier but silently grants v6 egress to
non-isolated sandboxes — a connectivity change).

### 2. Provisioning — actually give the guest the chosen families

Per backend, this is where the real work is:

- **docker/podman:** stop relying on the default bridge for v6; create (and reclaim — note DF137
  scoping) a yoloai-owned network with `EnableIPv6` + a ULA subnet when v6 is wanted. Per-network
  `--ipv6` works on modern Docker (verified on this host's Docker 29.x) without daemon-wide config.
  This is new network-management code — yoloai currently creates no docker networks.
- **containerd:** a dual-stack CNI config (v6 IPAM alongside the v4 subnet), replacing the hardcoded
  v4-only `cni.go` assumption.
- **apple:** already dual-stack via vmnet — nothing to provision, only to filter.
- **tart/seatbelt:** host-shared; nothing to provision.

Real internet v6 egress additionally needs a host v6 **uplink**; a ULA-only host gives the guest a
v6 address that is link-local-ish (fine for *testing the filter*, not for reaching the v6 internet).

### 3. Isolation — filter every present family, fail loud on the impossible

Once the guest's families are known, `--network-isolated` installs its default-deny + allowlist for
**each present family**: `iptables`/ipset(inet) for v4, `ip6tables`/ipset(inet6) for v6. This is the
concrete firewall work detailed in [ipv6-network-isolation.md](ipv6-network-isolation.md) (dual-stack
`resolve_domains`, family-classified nameservers, `apply_firewall` parameterized over the family
toolset, run once per present family). DF104 is resolved when the guest's v6 is a **deliberate,
provisioned** family **and** isolation filters it.

**Fail-loud seams (owner's call, 2026-07-18: fail closed):**

- Isolation requested, guest has a family, but its tooling can't filter it (e.g. `ip6tables` missing,
  or the guest kernel disabled IPv6 so `ip6tables` errors) → **abort the launch**, same as a missing
  `iptables` today. Do not start a sandbox believing it is isolated when a present family is unfiltered.
- A family **force-on** but the host can't provide it → **error at the edge**, before launch.
- A family simply **absent** (not provisioned, not present) → no rules for it; nothing to filter, so
  nothing to fail. (This is why DF134's interim fix *skips* a v6 nameserver rather than aborting — a
  v6 nameserver is not a request for v6 isolation.)

## Relationship to findings and the existing plan

- **DF134** (RESOLVED 2026-07-18) — the interim: a v4-only firewall skips non-v4 nameservers so a
  stray v6 entry can't abort isolation. Independent of this design; already shipped.
- **DF104** (OPEN) — v6 egress unfiltered on apple. Resolved by this design (provision + filter v6),
  not by the narrow firewall patch alone.
- **[ipv6-network-isolation.md](ipv6-network-isolation.md)** — the **isolation slice** (layer 3's
  `firewall.py` dual-stack change). It now depends on this design: filtering v6 is only meaningful
  once the guest is provisioned with v6, which is layers 1–2 here.

## Open decisions (owner)

1. **Default policy:** inherit-host families, or v4-only-unless-asked? (Connectivity vs friendliness.)
2. **Surface:** a new `--network-families` flag + `network_families` config, or fold into existing
   network flags?
3. **Non-isolated sandboxes:** do they get v6 by default when the host has it (new egress), or is v6
   opt-in there too?
4. **Host v6 "usable" detection:** address present? default route present? actual reachability probe?
   (The plan warns detection is easy to get wrong — a false "no v6" silently drops parity, a false
   "yes" makes isolation fail-loud on a family that can't actually route.)

## Verification note

None of this is verifiable on the current dev host (no v6 uplink, `0` global v6 addresses). It needs
either a host with real IPv6, a synthetic v6 netns to exercise the filter in isolation, or the apple
(macOS) backend for the vmnet-ULA path that motivated DF104. Any "done" claim must say which of those
it rests on.
