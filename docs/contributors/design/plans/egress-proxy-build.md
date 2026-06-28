# Egress proxy (workstream D) — build brief

ABOUTME: The "start here" kickoff for building the egress-proxy credential-broker. Design is
settled and validated; this is the actionable build plan. Rationale lives in D105 (+ its
validation addendum) in decisions/working-notes.md; do not re-litigate it — build to it.

**Status:** Design settled + validated (2026-06-28). Build NOT started. Read first:
[D105 + validation addendum](../../decisions/working-notes.md#d105--egress-proxy-workstream-d-brokering-is-the-default-containment-is-opt-in-phased-by-credential-material-refines-d90d95),
then [secure-secrets.md](../secure-secrets.md) (D95) and [netpolicy.md](../netpolicy.md) (D90),
then the spike at [research/egress-broker-spike/](../research/egress-broker-spike/).

## The shape in one paragraph

Default posture = **"API keys brokered; egress open unless restricted."** Two layers:
(1) an **always-on key-injector** — a small per-sandbox fixed-upstream reverse proxy; the
agent is pointed at it via `base_url` with a **placeholder** credential, and the proxy swaps
in the real upstream key (general traffic goes direct). (2) an **opt-in egress-containment**
layer — default-deny netns + an SNI-splicing forwarder + allowlist — that subsumes the
injector when enabled. Transport is **`base_url` redirect, not transparent MITM** (proven:
no agent pins certs; all accept plain-`http://` localhost). Bespoke small Go (no
Python/SaaS deps). Linux-first enforcement (host nftables on the veth).

## Build order

1. **`CredentialSource` + general credential-shape (tool-agnostic).** Fill the reserved
   seams (`EnvSpec`, `netpolicycfg`). Reserve the *full* shape now — retrofitting it later
   is a breaking change:
   ```
   CredentialBinding { Destination, Apply, Source }
     Apply  = header-set | basic-auth | request-signer   // closed 3-variant enum
     Source = static | refreshing(Fetch->value,expiresAt) | minting
   ```
   Implement `header-set` + `basic-auth` + `static`/`refreshing` now; **reserve**
   `request-signer` (AWS SigV4 / Azure SharedKey — runs LAST, sees the whole request) and
   `minting` (GitHub-App JWT->1h token; Docker/OCI). Do NOT solve Docker/OCI or SigV4 in the
   proxy yet — route Docker to a `minting` source + an in-sandbox credential helper.
2. **Always-on key-injector — metered API keys (the default).** Per-sandbox forward-and-swap
   reverse proxy: terminate the agent's request, replace the placeholder auth header with the
   real `x-api-key`/`Authorization`, stream the response (SSE) back, forward to the agent's
   configured real upstream. Wire the per-agent launch config (next section). `--no-broker`
   escape hatch.
3. **Subscription-OAuth broker (Phase 2).** Proxy holds the credential host-side and injects
   `Authorization: Bearer`; the agent gets `base_url` + a dummy handle and never holds creds.
   **Prefer the sanctioned `claude setup-token` -> 1-year `CLAUDE_CODE_OAUTH_TOKEN`** held
   host-side over reverse-engineering Anthropic's OAuth refresh (undocumented, rotated,
   discouraged — fragile).
4. **Opt-in egress containment.** Default-deny netns + SNI-splicing TCP forwarder (peek
   plaintext SNI, allow/deny, splice — no decryption) + allowlist. Linux first
   (docker/podman/Kata/gVisor via host nftables on the veth — uniquely gives gVisor a real
   allowlist). Strategy-dispatch `LivePatchNetwork` (`ip-filter` vs `egress-proxy`). macOS
   deferred (Tart/Apple = host `pf` on the TAP; Seatbelt has no netns — hardest).
5. **(later)** MITM fallback for non-redirectable injection; macOS enforcement; Bedrock/Vertex
   (SigV4 signer); the git broker (GitHub-App minting).

**Keep direct delivery (today's `/run/secrets` file/env path) as the per-backend transitional
fallback — no flag-day.** Brokering rolls out backend-by-backend; subscription stays on direct
delivery until Phase 2.

## Per-agent launch config (all brokerable via base_url + dummy cred)

| Agent | base_url + dummy-cred knobs | Must-do |
|---|---|---|
| Claude | `ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN=dummy` | pre-seed `~/.claude.json` `hasCompletedOnboarding:true`; allowlist `api.anthropic.com` for WebFetch preflight (or `skipWebFetchPreflight:true`); `ENABLE_TOOL_SEARCH=true` if MCP tool search needed |
| Codex | custom `[model_providers.X] base_url`+`env_key`+`wire_api="responses"`+`forced_login_method="api"` | proxy speaks **Responses API** `/responses`; not the built-in `openai` provider; disable telemetry/update |
| Gemini | `GOOGLE_GEMINI_BASE_URL` + `GEMINI_API_KEY=dummy` | **must** force API-key mode (base_url ignored under OAuth) |
| Aider | `--openai-api-base` + `--openai-api-key dummy` | model name **must** be `openai/<model>`; `--no-check-update --analytics-disable` |
| OpenCode | provider `options.baseURL` + `options.apiKey` | **pre-cache the `@ai-sdk/openai-compatible` npm pkg + models.dev in the image** (runtime fetch precedes the proxy); `autoupdate:false`, `share:"disabled"` |

The injector itself stays simple (forward + header-swap to a per-agent-configured upstream);
all per-agent quirks above live in launch config (agent definitions + envsetup), not the proxy.

## Confirm at build (each ~30-min localhost echo)

- Codex: zero-key-validation + exact Responses request shape.
- Gemini: current base_url env-var spelling (it has churned across versions — pin the CLI).
- OpenCode: `options`-forwarding on the pinned version (issue #5674).
- Subscription: `claude setup-token` token works injected; ToS sanity for brokered subscription.

## Reserved seams already in tree (fill these)

`StrategyEgressProxy` const, the `netpolicycfg` record + `Compose`, `EnvSpec` credential
fields, `LivePatchNetwork` (strategy-dispatch). See the seam map in D90/D95 and the
code-level audit referenced from D105.

## Out of scope / do not over-build

Transparent MITM (base_url redirect makes it unnecessary for the LLM path — keep as a later
fallback only). Docker/OCI token-exchange in the proxy (use a credential helper). macOS
enforcement (Phase 5). Concealment of credentials (impossible by design — goal is bounded
blast radius + detection, composed with the copy/diff/apply review gate + a narrow allowlist
+ egress logging).
