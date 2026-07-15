> **ABOUTME:** Systematically capture each agent's real network traffic across a full session
> and verify the `--network-isolated` allowlist covers it; Gemini already had a gap.

# Comprehensive network allowlist audit

- **Status:** UNSPECIFIED — idea only; not started.
- **Depends on:** —

All agents need a systematic audit of actual network traffic: capture traffic during full sessions (startup, auth, operation, token refresh, telemetry) and verify the allowlist covers everything. Gemini was missing `oauth2.googleapis.com` for OAuth token refresh; other agents likely have similar gaps.

Most important for `--network-isolated` mode where missing domains cause silent failures.

See [OPEN_QUESTIONS.md](../questions-unresolved.md) §97.
