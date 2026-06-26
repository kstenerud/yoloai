# Per-agent headless-mode authentication behavior

**ABOUTME:** Verified matrix of how each agent's headless/non-interactive mode behaves
**ABOUTME:** under different auth, backing the `yoloai run` headless-vs-TTY fallback (D101).

**Status:** Researched 2026-06-26 (sourced against official docs + GitHub issues). Backs
[D101](../../decisions/working-notes.md) and `agent.Definition.HeadlessSafeWithoutAPIKey`.
Scope-limited per the user: trust the docs, don't chase incidental edge cases — headless is
a startup optimization, and the TTY path is the always-safe fallback.

## Why this matters

`yoloai run` prefers headless launch (`claude -p`, etc.) to skip the settle+inject startup
cost. But several agents' headless modes **hang** when they lack usable auth: they try to
start a browser/OAuth login flow that can never complete in a headless tmux pane. The hang —
not a worse result — is the real hazard. So `run` must only go headless when it's safe, and
fall back to the interactive TTY flow otherwise (where the user can attach and authenticate).

## Matrix

The design (below) treats all agents uniformly — headless only when auth is observed — so the
matrix is here as the verified *facts* (especially the failure modes) that justify that rule, not
as per-agent special-casing.

| Agent | Headless OK on OAuth/subscription (no API key)? | Failure mode when under-authed |
|---|---|---|
| **Claude Code** (`claude -p`) | **Yes** — plain `-p` reads OAuth/keychain and runs on a `/login` subscription; `/login` is disabled in `-p` so it can't hang (only `--bare` is key-only) | **Clean** non-zero exit (`Not logged in`) |
| **Gemini CLI** (`gemini -p`) | No — can't initiate OAuth login headless (needs a browser); only a pre-cached token or API key works | **HANGS** on the OAuth/browser wait (#1696, #13853) |
| **Aider** (`--message`) | N/A — BYO-API-key only, no OAuth concept | **HANGS ~5 min** on OpenRouter OAuth onboarding (verified in `aider/onboarding.py`: no isatty guard, 300 s timeout) |
| **Codex** (`codex exec`) | Conditional — works with a pre-seeded `~/.codex/auth.json`; `exec` won't log in itself | Clean error, but a **historical hang** bug (#1569) |
| **OpenCode** (`opencode run`) | Likely — shares `auth.json` with the TUI; under-auth appears to error cleanly | Clean error (not a hang) |

## The `claude -p` vs `--bare` distinction (verified directly)

Per code.claude.com/docs/en/headless: plain `claude -p` "loads the same context an interactive
session would, including anything configured in … `~/.claude`," and **`--bare` is the variant that
"skips OAuth and keychain reads"** and requires `ANTHROPIC_API_KEY`/`apiKeyHelper`. So a
subscription `/login` works under plain `-p` but **not** under `--bare`. Two consequences:
- yoloAI's claude headless command must **never** add `--bare` (it would silently become
  API-key-only and break subscription users). Enforced by the comment at claude's `HeadlessCmd`.
- The docs note `--bare` "will become the default for `-p` in a future release" — a forward change
  we can't predict. The design below does **not** depend on it.

## Bottom line (the design rule — failsafe-forward)

Headless is a startup optimization; the interactive TTY flow always works. So `yoloai run` goes
headless **only when the agent has authentication we can observe** — an API key, an auth credential
file/Keychain entry, or an auth hint (local model server) — reusing the same `envsetup` checks as
the create-time missing-auth gate (one auth-presence convention). With auth present an agent won't
hit a login prompt, so it can't hang; without it, run interactively so the user can attach and
authenticate.

**Why observed-auth and not a per-agent "headless-safe" flag:** a flag encodes an *assumption* about
an agent's headless behavior (e.g. "Claude is fine without a key") that an upstream release can
invalidate — and our code can't retroactively know. Betting on observed auth instead depends on
nothing that can change upstream: for Claude the reachable behavior is identical (create already
requires Claude to have auth, or it errors first), but the fragile assumption is gone, and the
worst case if any agent's auth stops working stays a clean failure, never a hang. Utility agents
(`test`, `idle`) require no API key → `HasAnyAPIKey` true → always viable.

## Known residual (accepted, per "not worth chasing")

"Credential present" ≠ "credential valid" — an expired token still presents a file/var, so an agent
that re-auths on expiry (Gemini/Codex) could still hang headless. The durable fix is a headless
launch with no answerable interactive TTY (so a login attempt fails fast instead of stalling) —
tracked as a follow-up tied to the session-carve's no-TTY mode. Until then the auth-presence gate
covers the common no-auth case, and `--tty` is the manual escape hatch.

## Sources

Claude Code: code.claude.com/docs/en/authentication, /headless; anthropics/claude-code#27900,
#60155. Gemini: google-gemini.github.io/gemini-cli authentication docs; google-gemini/gemini-cli
#1696, #13853. Codex: developers.openai.com/codex (noninteractive, ci-cd-auth); openai/codex#1569.
Aider: aider.chat/docs/config/api-keys.html; source `aider/onboarding.py`, `aider/io.py`.
OpenCode: opencode.ai/docs (config, providers, github); sst/opencode#3467, #417.
