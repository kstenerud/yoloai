> **ABOUTME:** Three unresolved Codex questions — proxy env support, required network domains,
> and tmux TUI behavior — blocking production-ready Codex network isolation.

# Codex research items

- **Status:** UNSPECIFIED — three open research questions, listed below; none investigated yet.
- **Depends on:** —

Three unresolved questions needed before Codex network isolation is production-ready:

- **Proxy support (#37):** Whether Codex's static Rust binary honors `HTTP_PROXY`/`HTTPS_PROXY` env vars is unverified. Critical for `--network-isolated` mode — if it ignores proxy env vars, iptables-only enforcement is the only option.
- **Required network domains (#38):** Only `api.openai.com` is confirmed. Additional domains (telemetry, model downloads) may be required. Needs traffic capture during a full Codex session.
- **TUI behavior in tmux (#39):** Interactive mode (`codex --yolo` without `exec`) behavior inside tmux is unverified. May affect idle detection and prompt delivery.

See [OPEN_QUESTIONS.md](../questions-unresolved.md) §37–39.
