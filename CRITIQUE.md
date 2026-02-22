# Critique — Round 2

Second design-vs-research audit. Focused on factual accuracy of security claims, internal consistency, and design gaps.

## Applied

- **C14.** `CAP_SYS_ADMIN` does NOT include `CAP_NET_ADMIN` — fixed in both DESIGN.md and RESEARCH.md. Both capabilities must be explicitly granted via `--cap-add`. Container startup section updated to list both.
- **C15.** Claude Code proxy support — researched and documented in new RESEARCH.md section "Claude Code Proxy Support Research". Claude Code (npm) honors `HTTP_PROXY`/`HTTPS_PROXY` via undici's `EnvHttpProxyAgent`. Native binary (Bun) does NOT — base image must use npm install. DESIGN.md updated with proxy env var requirement in container startup, npm requirement in base image, and note about v2 per-agent proxy verification.
- **C16.** Container startup credential reference — changed from "ANTHROPIC_API_KEY from host environment" to "API key injected via file-based bind mount at `/run/secrets/`" with cross-reference to Credential Management section.
- **C17.** `env` config field — added to config schema (defaults + profiles, merged with profile winning on conflict). SSH agent forwarding advice updated to use the new field. Note added that `ANTHROPIC_API_KEY` uses file-based injection, not `env`.
- **C18.** Proxy container lifecycle — specified: per-sandbox (`yoloai-<name>-proxy`), created during `yoloai new --network-isolated`, stopped/started/destroyed alongside sandbox, proxy image built during `yoloai build`, allowlist stored in `meta.json`.
- **C19.** Default network allowlist — specified per-agent defaults (v1 Claude Code: `api.anthropic.com`, `statsig.anthropic.com`, `sentry.io`). v2 will add per-agent defaults for other agents. `--network-allow` is additive.
- **C20.** Architecture diagram — fixed `/claude-state` to `~/.claude (per-sandbox state)` to match actual mount at `/home/yoloai/.claude`.
- **C21.** RESEARCH.md filesystem isolation intro — updated from "current design uses full directory copies" to "design uses overlayfs by default with full directory copies as a fallback."

## Deferred

(none)
