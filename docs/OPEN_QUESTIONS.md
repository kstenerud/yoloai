# Open Questions

Questions encountered during design and implementation that need resolution. Resolve each before the relevant implementation phase begins.

## Pre-Implementation (resolve before coding starts)

1. **Go module path** — `github.com/<org>/yoloai` is a placeholder in CODING-STANDARD.md. What is the actual GitHub org/username?

2. **Node.js version** — Plan proposes Node.js 22 LTS via NodeSource APT repo. Is this confirmed as current LTS and compatible with Claude Code?

3. **tini** — Plan proposes `docker run --init` (Docker's built-in tini) rather than installing tini in the image. Simpler, but means the flag must always be passed. Any concerns?

4. **gosu** — Plan proposes installing from GitHub releases (static binary). Standard choice for Debian-based Docker images (used by official MySQL/Postgres). Agreed?

5. **Claude ready indicator** — MVP uses a fixed 3-second delay (configurable via `YOLOAI_STARTUP_DELAY` env var) before feeding the prompt via tmux send-keys. Polling for a ready indicator is deferred. Acceptable for dogfooding?

6. **Caret encoding scope** — MVP only encodes `/` → `^2F` (the only filesystem-unsafe character in absolute POSIX paths). The full caret encoding spec supports more characters, but they don't appear in paths. Sufficient?

## Deferred Items Worth Reconsidering

These were deferred from MVP but might be cheap to add and valuable for dogfooding:

7. **`--model` flag** — Lets you pick a cheaper/faster model for iteration. Trivial to implement (pass through to agent command). Worth including in MVP?

8. **`yoloai exec`** — Run ad-hoc commands inside the sandbox. Useful for debugging during dogfooding. Simple wrapper around `docker exec`.

9. **Dangerous directory detection** — Error on `$HOME`, `/`, system dirs. Small validation function. Prevents footgun during early use.

10. **Dirty git repo warning** — Warn if workdir has uncommitted changes. Prevents surprise data in the copy. Small git status check.

## Post-MVP (resolve before relevant feature is implemented)

11. **Codex proxy support** — Whether Codex's static Rust binary honors `HTTP_PROXY`/`HTTPS_PROXY` env vars is unverified (DESIGN.md line 340, RESEARCH.md). Critical for `--network-isolated` mode with Codex. If it ignores proxy env vars, would need iptables-only enforcement.

12. **Codex required network domains** — Only `api.openai.com` is confirmed (DESIGN.md line 341/445). Additional domains (telemetry, model downloads) may be required.

13. **Codex TUI behavior in tmux** — Interactive mode (`codex --yolo` without `exec`) behavior in tmux is unverified (RESEARCH.md).

14. **Image cleanup mechanism** — Docker images accumulate indefinitely. Cleanup is deferred pending research into Docker's image lifecycle (DESIGN.md line 642). Needs design for safe pruning that doesn't break running sandboxes.
