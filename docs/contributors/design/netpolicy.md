> **ABOUTME:** Design for netpolicy, the layer that composes a network egress policy (mode, agent
> floor, user allow/deny) and enforces it over the substrate through a pluggable strategy —
> carving that logic out of the backends. Covers the ip-filter/egress-proxy strategy split and
> what each honestly does and doesn't guarantee.

# Netpolicy — the network-policy refinement over the substrate

> **Refined by [D105](../decisions/working-notes.md#d105--egress-proxy-workstream-d-brokering-is-the-default-containment-is-opt-in-phased-by-credential-material-refines-d90d95) (2026-06-28):** the `egress-proxy` strategy is realised as an **opt-in** layer (default-deny netns + an **SNI-splicing forwarder** — no decryption — + allowlist), **Linux-first** (host nftables on the veth; uniquely fixes gVisor). Egress restriction stays opt-in/open-by-default; it composes with the always-on credential injector from D105/D95. Read D105 for the settled shape.

**Status:** Design converged 2026-06-24, refined by D105 (2026-06-28), not yet implemented. The **netpolicy** refinement
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

4. **Capability is per-(backend × *isolation-mode* × platform × strategy), with honest characterization.**
   *(Sharpened 2026-06-24, D92 — verified against code; the earlier "per-(backend × strategy)" was
   under-dimensioned.)* `ip-filter` needs an in-sandbox iptables the kernel actually consults, so its
   enforceability turns on **the isolation mode and platform**, not just the backend:
   - **Works:** `container` / `container-privileged` (docker/podman), `vm` / `vm-enhanced` (containerd-Kata,
     tart-VM, apple-VM) — all run the in-sandbox iptables path. *(Podman-on-macOS: the kernel lacks `xt_set`, so
     ipset fails and the code falls back to per-IP iptables rules — verified; a degraded-but-working path.)*
   - **Cannot (silent no-op), already hard-rejected in shipped code:** `container-enhanced` (gVisor/runsc).
     *Precisely* (verified online 2026-06-24): gVisor's userspace netstack lacks the iptables **NAT table**
     (`gvisor#170`, open since 2018) and **`ipset`/`xt_set`**, with only partial filter-table support — so
     yoloai's **ipset-based** OUTPUT default-deny does not reliably apply. `IsolationEnforcesInSandboxIptables`
     returns false and `buildInstanceConfig` **refuses `isolated + container-enhanced` at create**. This existing
     reject must become a **first-class part of netpolicy's capability model**, not an ad-hoc create-time guard.
   - **`isolated` not supported at all:** seatbelt/tart (no in-sandbox iptables) — but see the verified
     `none`-holds note below; their *deny-all* path differs from their *allowlist* gap.
   `egress-proxy` needs a host-side interception point — *potentially broader* — and may **solve seatbelt/tart's
   `isolated` gap** (a host-side proxy filters where in-guest iptables can't). Netpolicy
   **characterizes-and-surfaces** unenforceable policy (refuse/warn, don't pretend).

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

So **`none` is the only hard network boundary today** *for `ip-filter`-class backends*. The design must label
`ip-filter` honestly and not over-promise (a wrong security claim is the worst kind — our own critique
principle).

**`none` DOES hold on macOS (verified 2026-06-24, D92 — a reviewed false alarm corrected).** `mode: none` is
enforced *natively*, independent of the in-sandbox iptables path: **seatbelt** via SBPL `(deny default)` (the
profile simply omits `(allow network*)`); **tart** via `--net-softnet-block=0.0.0.0/0` + `::/0` at the VM
level. So `none` is a hard boundary on *every* backend — only `isolated` (the allowlist) is the seatbelt/tart
gap. Fail-mode is already **fail-closed** (`entrypoint.py` aborts before launching the agent if rules can't
install); the carve must **preserve** that — enforcement failing to apply must refuse the launch, never fail
open (for the metered-JV-key + adversarial case, fail-open is a credential-exfil event).

**`ip-filter` is the re-home target for `entrypoint.py:isolate_network`** ([DF41](findings-unresolved.md)) — the
existing in-container enforcement *is* this strategy, lifted to a Go-driven step over the neutral keep-alive.

## Hostile containment — the committed future (`egress-proxy`)

Hostile network containment **is** coming — slotted as a feature *after* the layering revamp, not folded in
now; the structure is shaped for it (§3). The key realization: hostile containment is **not "a better
filter"** — it is **enforcement outside the agent's reach by construction**. The `egress-proxy` strategy is a
host-side (or isolated-netns) L7 proxy the agent's traffic is *forced* through and cannot disable, doing
SNI/Host-based allow and killing DNS-exfil. Because the policy model is already domain-centric (§2), the proxy
is a strictly-better *interpretation of the same allowlist* — it slots into the strategy axis with **no policy-
model change and no contract change** (the contract already doesn't assume an in-sandbox point). It may also
be how seatbelt/tart gain isolation at all (§4).

**Two structural-room refinements the review surfaced (2026-06-24, D92):**
- **The *mutation transport* is also per-strategy — not just the enforcement *point*.** Today `allow`/`deny`
  live-patch by exec'ing an in-sandbox `dig`+ipset script — structurally married to `ip-filter`. The
  `egress-proxy` cannot reuse it (it reconfigures host-side). So `Network.Allow/Deny` must **dispatch on the
  active strategy** (the dispatch point doesn't exist today). The policy *data* is strategy-agnostic; only the
  apply/live-patch *transport* needs the seam.
- **The proxy *mechanism* (resolved 2026-06-24 — [research/agent-proxy-support.md](research/agent-proxy-support.md),
  closes Q37).** Env-proxy (`HTTP_PROXY`) is **never** a containment boundary for an untrusted agent — it lives
  *inside* the agent's environment, so it can unset it, ignore it, or use raw sockets / UDP / IPv6 (and Codex
  *disables* env proxy inside its own sandbox; Aider needs a flag; support is per-agent-quirky). And —
  sharper than the earlier "routing-enforced" framing — **transparent intercept *alone* is also bypassable**
  (a `NET_ADMIN`/sudo agent rewrites the rules; UDP/IPv6 escape). So the load-bearing `egress-proxy` primitive
  is: **a default-deny egress firewall in the sandbox netns whose only outbound path is forced to a filtering
  proxy on a *different principal/namespace* than the agent.** This *is* "enforcement outside the agent's reach
  by construction," made precise — and it's why the current in-sandbox `ip-filter` (iptables a sudo agent can
  flush) is not hostile-grade. Per-agent proxy-env injection stays a *convenience hint* for cooperating agents
  + observability (an agent-layer capability), never the boundary.

## Lifecycle

Composed at create; mutated at runtime (`allow`/`deny` re-resolve + live-patch the running enforcement —
per-strategy transport, §Hostile); re-applied on restart (fresh resolution under `ip-filter`). The
no-dynamic-re-resolution stale-IP gap is an `ip-filter` property the `egress-proxy` strategy (domain-native)
does not share.

**Persisted home (resolves a review contradiction, D92).** The substrate's `ProvisionSpec`/`environment.json`
(D85, agent-free) explicitly *disclaims* the egress allowlist ("egress allowlist = netpolicy refinement"). So
the composed `mode + allowlist + provenance` is **netpolicy's own persisted record** — its own persistence
domain (D87 `Handle`), not a field smuggled into the substrate's record. (Done on
`substrate-move`: `NetworkMode`/`NetworkAllow` were relocated out of `environment.json` into the
sibling `netpolicy.json` record — `internal/netpolicycfg` — folded into the Q104 v2→v3
`system migrate` so it remains the last on-disk migration; the egress-proxy enforcement reads
them from there. D103.)

## Cross-references

- **Decisions:** [D84](../decisions/working-notes.md) (substrate — netpolicy is a refinement over it; network
  *provisioning* is substrate, policy is netpolicy), [D88](../decisions/working-notes.md) (the entrypoint
  carve — enforcement moves off the mandatory entrypoint), [D89](../decisions/working-notes.md) (the agent's
  domain-floor input); this layer's own entry **D90**.
- **Findings:** resolves [DF34](findings-unresolved.md) (network isolation woven into the backend); related
  smoke findings DF9 (CNI firewall no-op) / DF10 (netns leak) are `ip-filter`/CNI-provisioning bugs.
- **Consumer:** control-eval — its adversarial untrusted-agent evals are the driver for the committed
  `egress-proxy` future (today they use `none` for hard isolation; `isolated` is the honest-agent leash).
