# yoloai

Sandboxed Claude CLI runner. Runs Claude Code with `--dangerously-skip-permissions` inside disposable Docker containers with copy/diff/apply workflow.

## Project Status

Design and research phase. No code yet.

## Key Files

- `DESIGN.md` — Architecture, commands, config format, workflows, security considerations, resolved decisions.
- `RESEARCH.md` — Competitive landscape (8 tools analyzed in depth, 9 more cataloged), community pain points, security incidents, alternative filesystem isolation approaches, feature comparison table.
- `CRITIQUE.md` — Rolling critique document. After a critique pass, findings are applied to DESIGN.md and RESEARCH.md, then CRITIQUE.md is emptied for the next round.

## Architecture (from DESIGN.md)

- Python 3.10+ CLI script, no external deps beyond stdlib + Docker.
- Docker containers with persistent state in `~/.yoloai/sandboxes/<name>/`.
- Containers are ephemeral; state (work dirs, claude-state, logs, meta.json) lives on host.
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

## Design Principles

- Copy/diff/apply is the core differentiator — protect originals, review before landing.
- Overlayfs + git is the preferred `:copy` strategy (instant setup, git-based diff). `copy_strategy: auto | overlay | full` config option.
- Safe defaults: read-only mounts, no implicit `claude_files` inheritance, name required (no auto-generation), dirty repo warning (not error).
- CLI for one-offs, config for repeatability (same options in both).
- Security requires dedicated research — don't finalize ad-hoc. `CAP_SYS_ADMIN` tradeoff is documented.
