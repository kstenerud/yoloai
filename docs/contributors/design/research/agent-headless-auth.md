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

| Agent | Headless OK on OAuth/subscription (no API key)? | Failure mode when under-authed | Verdict for `run` |
|---|---|---|---|
| **Claude Code** (`claude -p`) | **Yes** — runs on a `/login` subscription; `/login` is disabled in `-p` so it can't hang | **Clean** non-zero exit (`Not logged in`) | **Headless always safe** — `HeadlessSafeWithoutAPIKey = true` |
| **Gemini CLI** (`gemini -p`) | No — can't initiate OAuth login headless (needs a browser); only a pre-cached token or API key works | **HANGS** on the OAuth/browser wait (#1696, #13853) | Headless only with an API key |
| **Aider** (`--message`) | N/A — BYO-API-key only, no OAuth concept | **HANGS ~5 min** on OpenRouter OAuth onboarding (verified in `aider/onboarding.py`: no isatty guard, 300 s timeout) | Headless only with an API key |
| **Codex** (`codex exec`) | Conditional — works with a pre-seeded `~/.codex/auth.json`; `exec` won't log in itself | Clean error, but a **historical hang** bug (#1569) | Headless only with an API key (conservative; seeded auth.json under-covered, accepted) |
| **OpenCode** (`opencode run`) | Likely — shares `auth.json` with the TUI; under-auth appears to error cleanly | Clean error (not a hang) | Headless only with an API key (conservative) |

## Bottom line (the design rule)

- **Claude** is the one agent safe to run headless on any auth (including a subscription with no
  API-key env var), because it fails cleanly and `/login` is disabled in `-p` (no TTY hang). →
  `HeadlessSafeWithoutAPIKey = true`.
- **Every other real agent** can hang headless without a usable key, so headless is gated on an
  API key being present (`envsetup.HasAnyAPIKey`, which checks the agent's API-key **env vars**).
  No key → fall back to the TTY flow.
- **Utility agents** (`test`, `idle`) declare no `APIKeyEnvVars`, so `HasAnyAPIKey` returns true and
  they stay headless — no auth, no hang.

## Known conservative gaps (accepted, per "not worth chasing")

- `HasAnyAPIKey` checks env vars, **not** OAuth credential files. A Codex/OpenCode user with a
  valid seeded `auth.json` (but no key env var) falls back to TTY unnecessarily — slower, never
  broken. (Claude's file-based subscription is covered by the explicit flag instead.)
- "Credential present" ≠ "credential valid" — an expired token still presents a file/var, so a
  hang is still theoretically possible. The TTY fallback keeps such a case **visible and
  attachable** rather than an invisible headless stall, which is the graceful behavior the user
  asked for.

## Sources

Claude Code: code.claude.com/docs/en/authentication, /headless; anthropics/claude-code#27900,
#60155. Gemini: google-gemini.github.io/gemini-cli authentication docs; google-gemini/gemini-cli
#1696, #13853. Codex: developers.openai.com/codex (noninteractive, ci-cd-auth); openai/codex#1569.
Aider: aider.chat/docs/config/api-keys.html; source `aider/onboarding.py`, `aider/io.py`.
OpenCode: opencode.ai/docs (config, providers, github); sst/opencode#3467, #417.
