# yoloai

Sandboxed AI coding agent runner. Runs AI coding CLI agents (Claude Code, Codex) inside disposable Docker containers with copy/diff/apply workflow. Additional agents (Aider, Goose, etc.) in future versions.

## Project Status

Design and research phase. No code yet.

## Key Files

- `DESIGN.md` — Architecture, commands, config format, workflows, security considerations, resolved decisions.
- `RESEARCH.md` — Competitive landscape (8 tools analyzed in depth, 9 more cataloged), community pain points, security incidents, alternative filesystem isolation approaches, feature comparison table, multi-agent CLI research, credential management research, network isolation research.
- `CRITIQUE.md` — Rolling critique document. After a critique pass, findings are applied to DESIGN.md and RESEARCH.md, then CRITIQUE.md is emptied for the next round.
- `CLI-STANDARD.md` — CLI design conventions: argument ordering (options first), flag naming, exit codes, error messages, help text format.
- `CODING-STANDARD.md` — Code style: Go 1.22+, gofmt, golangci-lint, Cobra, project structure, naming, error handling, dependency policy.

## Architecture (from DESIGN.md)

- Go binary, no runtime deps — just the binary and Docker.
- Docker containers with persistent state in `~/.yoloai/sandboxes/<name>/`.
- Containers are ephemeral; state (work dirs, agent-state, logs, meta.json) lives on host. Credentials injected via file-based bind mount (not env vars).
- Agent abstraction: per-agent definitions specify install, launch command, API key env vars, state directory, network allowlist, and prompt delivery mode. v1 ships Claude and Codex.
- Directories mounted at mirrored host paths by default (path consistency). Custom paths via `=<path>` override.
- `:copy` directories use overlayfs by default (instant setup, deltas-only) with full-copy fallback. Both use git for diff/apply.
- `:rw` directories are live bind-mounts. Default (no suffix) is read-only.
- Profile system: user-supplied Dockerfiles in `~/.yoloai/profiles/<name>/`.
- Config in `~/.yoloai/config.yaml` with defaults + named profiles.

## Workflow Conventions

- **Critique cycle:** Write a critique in CRITIQUE.md, apply corrections to DESIGN.md/RESEARCH.md, mark critique as done, empty CRITIQUE.md for the next round.
- **Research before design changes:** When a design question comes up (e.g., "should we use overlayfs?"), research it first in RESEARCH.md with verified facts, then update DESIGN.md based on findings.
- **Factual accuracy matters:** Star counts, feature claims, and security assertions must be verified. Don't repeat marketing language or unverifiable numbers.
- **Cross-platform awareness:** Always consider Linux, macOS (Docker Desktop + VirtioFS), and Windows/WSL. Note platform-specific tradeoffs explicitly.
- **Commit granularity:** One commit per logical change. Research, design updates, and critique application get separate commits.

## Critique Principles

- **Research must be verified.** Agents can hallucinate and make mistakes. Don't trust claims without checking sources.
- **Focus on what affects the design.** Small research inaccuracies (e.g., numbers off by 10%) aren't worth critiquing if they don't change any design decision.
- **User sentiment is high-signal.** Community pain points and praise tell us where competitors succeed and fail. Learn from their examples.
- **The design must be backed by research.** Assumptions are dangerous and difficult to back out of once implementation starts. If a design claim lacks research backing, flag it.
- **Cross-reference both directions.** Check that design claims have research backing, and that research recommendations have been incorporated into the design.
- **Platform-specific claims need platform-specific verification.** Something that works on Linux may not work on macOS Docker Desktop (e.g., `--storage-opt size=`). Always note which platforms a claim applies to.
- **Security claims need the highest scrutiny.** A wrong security assumption (e.g., "env vars are safe for secrets") can undermine user trust and is hardest to fix after launch.
- **Separate facts from tradeoffs.** Research establishes facts; the design makes tradeoff decisions based on those facts. A critique should distinguish "this fact is wrong" from "this tradeoff deserves discussion."

## Design Principles

- Copy/diff/apply is the core differentiator — protect originals, review before landing.
- Overlayfs + git is the preferred `:copy` strategy (instant setup, git-based diff). `copy_strategy: auto | overlay | full` config option.
- Safe defaults: read-only mounts, no implicit `agent_files` inheritance, name required (no auto-generation), dirty repo warning (not error).
- CLI for one-offs, config for repeatability (same options in both).
- Security requires dedicated research — don't finalize ad-hoc. `CAP_SYS_ADMIN` tradeoff is documented.
