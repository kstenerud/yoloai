# Netpolicy — the network-policy refinement over the substrate

**Status:** Design converged 2026-06-24 (design conversation), not yet implemented. The **netpolicy** refinement
of [plans/public-layering.md](plans/public-layering.md) — a standalone layer over the substrate
([D84](../decisions/working-notes.md)). Resolves [DF34](findings-unresolved.md) (network isolation woven into
the backends). Consumes the agent layer's domain-floor ([D89](../decisions/working-notes.md)). Grounded in a
source map of the current CNI/iptables/ipset implementation.

**One-line definition.** Netpolicy **composes** a network egress policy (`mode × agent-floor × user
allow/deny`) and **enforces** it over the substrate via a **pluggable enforcement strategy** — carving the
network logic out of the backends.

## The model (the decisions behind the surface)

1. **The carve: provisioning vs policy.** DF34 says "isolation woven into containerd," but the domain-allowlist
   enforcement is already backend-agnostic (the in-container `entrypoint.py:isolate_network` iptables/ipset,
   identical for docker/podman/containerd/apple-VM). What's containerd-specific is the **CNI netns/bridge/IP
   plumbing** — *network provisioning*, a **substrate** concern ("give me a container with a filterable network
   namespace"). So: **substrate ← provisioning; netpolicy ← policy + enforcement.**

2. **Policy is domain-centric and agnostic.** `mode ∈ {none, isolated, full}` is the *intent*; in `isolated`
   the effective allowlist = `agent-floor ∪ user-allow \ user-deny`, with **provenance** (agent-required vs
   user-added, already in `network.go`). `none` = no network; `full` = no filtering. Domain-centric is
   forward-compatible (see §3): L7 is natively domain/SNI-based — the IP-approximation is the *degradation*,
   not the policy.

3. **Enforcement is a pluggable *strategy* axis, and the enforcement point is NOT assumed in-sandbox.** Mode is
   the intent; the **strategy** is the realization. Two strategies behind one contract:
   - **`ip-filter`** (now): in-sandbox iptables/ipset, IP-approximate (see §4). The extracted `entrypoint.py`
     logic, applied at provision/launch (fits the D88 carve — off the mandatory entrypoint).
   - **`egress-proxy`** (the committed future, §5): an **out-of-agent-control** L7 proxy, domain-native,
     hostile-grade.

   The **load-bearing structural room:** the strategy contract must allow an enforcement point **outside the
   sandbox** as first-class. The obvious extraction would bake in "enforcement happens in-sandbox" — and that
   single assumption is what would force a rewrite when the proxy lands. "Hostile" = `isolated` + the
   `egress-proxy` strategy — a new *strategy*, not a new *mode*.

4. **Capability is per-(backend × strategy), with honest characterization.** `ip-filter` needs in-container
   iptables (containers + Linux-VM); `egress-proxy` needs a host-side interception point — *potentially
   broader*, and notably it may **solve seatbelt/tart's current no-op** (a host-side proxy filters where
   in-guest iptables can't). Netpolicy **characterizes-and-surfaces** unenforceable policy (e.g. `isolated` on
   seatbelt/tart today is a silent no-op → refuse/warn, don't pretend) — the same discipline as copyflow's
   nature-mismatch.

## Honest characterization — what `ip-filter` does and doesn't promise

`ip-filter` is a **best-effort domain allowlist, IP-enforced** — it keeps an *honest* agent on a leash; it is
**not** adversarial containment. The holes (all properties of resolving domains→IPs and filtering in-sandbox):
- **DNS exfil** — port 53 to the nameservers must be open to resolve, so a determined agent tunnels data out
  via DNS queries.
- **Shared-IP over-allow** — allowing one domain on a CDN/cloud IP allows every domain co-hosted on that IP.
- **Stale IPs** — resolved once at startup (+ on `allow`/`deny`); TTLs expire / IPs rotate; no dynamic
  re-resolution. IPv4-only.
- **In-sandbox tamper** — enforcement lives inside the box; an agent with in-sandbox privilege (yoloai grants
  sudo for deps) can disturb it.

So **`none` is the only hard network boundary today.** The design must label `ip-filter` honestly and not
over-promise (a wrong security claim is the worst kind — our own critique principle).

## Hostile containment — the committed future (`egress-proxy`)

Hostile network containment **is** coming — slotted as a feature *after* the layering revamp, not folded in
now; the structure is shaped for it (§3). The key realization: hostile containment is **not "a better
filter"** — it is **enforcement outside the agent's reach by construction**. The `egress-proxy` strategy is a
host-side (or isolated-netns) L7 proxy the agent's traffic is *forced* through and cannot disable, doing
SNI/Host-based allow and killing DNS-exfil. Because the policy model is already domain-centric (§2), the proxy
is a strictly-better *interpretation of the same allowlist* — it slots into the strategy axis with **no policy-
model change and no contract change** (the contract already doesn't assume an in-sandbox point). It may also
be how seatbelt/tart gain isolation at all (§4).

## Lifecycle

Composed at create (`mode` + `network_allow` → `environment.json`; mirrored to the enforcement input);
mutated at runtime (`allow`/`deny` re-resolve + live-patch the running enforcement); persisted in the
Environment; re-applied on restart (fresh resolution under `ip-filter`). The no-dynamic-re-resolution
stale-IP gap is an `ip-filter` property the `egress-proxy` strategy (domain-native) does not share.

## Cross-references

- **Decisions:** [D84](../decisions/working-notes.md) (substrate — netpolicy is a refinement over it; network
  *provisioning* is substrate, policy is netpolicy), [D88](../decisions/working-notes.md) (the entrypoint
  carve — enforcement moves off the mandatory entrypoint), [D89](../decisions/working-notes.md) (the agent's
  domain-floor input); this layer's own entry **D90**.
- **Findings:** resolves [DF34](findings-unresolved.md) (network isolation woven into the backend); related
  smoke findings DF9 (CNI firewall no-op) / DF10 (netns leak) are `ip-filter`/CNI-provisioning bugs.
- **Consumer:** control-eval — its adversarial untrusted-agent evals are the driver for the committed
  `egress-proxy` future (today they use `none` for hard isolation; `isolated` is the honest-agent leash).
