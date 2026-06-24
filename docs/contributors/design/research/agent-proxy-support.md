# Agent proxy-env support + egress-containment primitive

**Verified mid-2026 (web survey of official docs + source; design-review remediation [D92]).** Backs the
netpolicy `egress-proxy` strategy ([netpolicy.md](../netpolicy.md) §Hostile) and **resolves
[Q37](../questions-unresolved.md)** (Codex `HTTP_PROXY` support). A fast-moving snapshot — treat as
version-dependent.

## Do the agents honor `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`?

| Agent | Honors proxy env? | Notes / knobs (if injecting env as a convenience) |
|---|---|---|
| **Claude Code** | **Yes** (documented) | No SOCKS; custom CA via `NODE_EXTRA_CA_CERTS`; mTLS supported |
| **OpenCode** | **Yes** (documented) | `NO_PROXY` must include `localhost,127.0.0.1` (TUI ↔ local server); CA via `NODE_EXTRA_CA_CERTS` |
| **Gemini CLI** | **Partial** (env is the path; `--proxy` flag removed) | `NO_PROXY`/`ALL_PROXY` undocumented — verify empirically |
| **Codex CLI** | **Partial / de-facto** (reqwest default; **#4242 open**) | **Disables env proxy *inside its own sandbox*** (`.no_proxy()`); CA via `CODEX_CA_CERTIFICATE`/`SSL_CERT_FILE` |
| **Aider** | **Only with a flag** | Default `aiohttp` transport ignores env proxies — needs `DISABLE_AIOHTTP_TRANSPORT=True`; CA via `SSL_CERT_FILE` |

**Q37 resolved:** Codex honors `HTTP_PROXY` only because it preserves reqwest's default behavior (no explicit
feature), coverage is inconsistent across call-sites, and it *intentionally ignores* proxy env inside its own
sandbox. So Codex proxy support is **unreliable** — exactly the kind of per-agent quirk a containment design
must not depend on.

## The design conclusion (confirmed + sharpened)

**Env-proxy is never a containment boundary for an untrusted agent.** It is a cooperative, application-layer
convention that lives *inside* the agent's own environment, so the agent can unset it, ignore it, or open raw
sockets / UDP / ICMP / IPv6 the variable has no say over. A security boundary must live **outside the trust
domain of the thing it constrains**. (Unanimous across Istio, mitmproxy, curl, the kernel docs, AWS/GCP, CMU
SEI, and Anthropic's own Agent-SDK secure-deployment guidance.)

**Sharper than "routing-enforced, not env-cooperative":** transparent intercept *alone* (iptables
`REDIRECT`/`TPROXY`) is **also** bypassable — by a process with `NET_ADMIN`/sudo (it rewrites the rules — and
yoloai grants agents sudo) or via UDP/IPv6. So the load-bearing primitive is:

> **a default-deny egress firewall in the sandbox's network namespace, whose *only* permitted outbound path is
> forced to a filtering proxy running on a *different principal / namespace* than the agent.**

Transparent redirect is a convenience layered on top of default-deny, not a substitute. This is the precise
content of netpolicy's "enforcement outside the agent's reach **by construction**" — and it explains why the
current `ip-filter` (in-sandbox iptables an `NET_ADMIN` agent can flush) is *not* hostile-grade.

**For yoloai:** treat per-agent proxy-env injection purely as a *convenience hint* for cooperating agents and
for observability — never as containment. If injected, the working knobs differ per agent (table above: Aider
needs `DISABLE_AIOHTTP_TRANSPORT`, Codex needs CA vars + won't honor it in its own sandbox) — an **agent-layer
capability** detail. Real containment = the default-deny-egress-netns + out-of-privilege proxy above.

## Sources

Claude — `code.claude.com/docs/en/network-config`. OpenCode — `opencode.ai/docs/network/`. Gemini —
`github.com/google-gemini/gemini-cli` PR #13538, `geminicli.com/docs/cli/enterprise/`. Codex —
`github.com/openai/codex` `codex-rs/login/src/auth/default_client.rs`, issue #4242,
`developers.openai.com/codex/config-reference`. Aider/LiteLLM — `docs.litellm.ai/docs/guides/security_settings`,
`python-httpx.org/environment_variables/`. Bypass reasoning — `blog.howardjohn.info/posts/bypass-egress/`,
`platform.claude.com/docs/en/agent-sdk/secure-deployment`, CMU SEI egress-filtering.
