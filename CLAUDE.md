# yoloAI

Sandboxed AI coding agent runner. Runs AI coding CLI agents (Claude Code, Gemini, Codex) inside disposable Docker containers with copy/diff/apply workflow. Additional agents (Aider, Goose, etc.) in future versions.

## Project Status

Public beta. Breaking changes are allowed but must be tracked in `docs/BREAKING-CHANGES.md`.

## Key Files

User-facing docs live in `docs/`:

- `docs/GUIDE.md` — Full usage reference: commands, flags, workdir modes, agents/models, configuration, sandbox state, security, development.
- `docs/BREAKING-CHANGES.md` — Tracks breaking changes made during beta. Each entry documents previous behavior, new behavior, rationale, and migration steps. Include in release notes.
- `docs/ROADMAP.md` — Future plans: agents, network isolation, profiles, overlayfs, etc.

Design specs live in `docs/design/`:

- `docs/design/README.md` — Goal, value prop, architecture, directory layout, prerequisites, resolved decisions.
- `docs/design/commands.md` — Command table, agent definitions, all command specs.
- `docs/design/config.md` — Docker images, config.yaml format, recipes, profiles.
- `docs/design/setup.md` — First-run experience, tmux configuration.
- `docs/design/security.md` — Credential management, security considerations.

Development docs live in `docs/dev/`:

- `docs/dev/ARCHITECTURE.md` — Code navigation guide: package map, file index, key types, command→code map, data flows, "where to change" recipes, testing. Keep in sync when architecture changes.
- `docs/dev/CODING-STANDARD.md` — Code style: Go 1.22+, gofmt, golangci-lint, Cobra, project structure, naming, error handling, dependency policy.
- `docs/dev/CLI-STANDARD.md` — CLI design conventions: argument ordering (options first), flag naming, exit codes, error messages, help text format.
- `docs/dev/RESEARCH.md` — Competitive landscape, community pain points, security incidents, feature comparison, multi-agent CLI research, credential management research, network isolation research.
- `docs/dev/CRITIQUE.md` — Rolling critique document. After a critique pass, findings are applied to design docs and RESEARCH.md, then CRITIQUE.md is emptied for the next round.
- `docs/dev/OPEN_QUESTIONS.md` — Questions encountered during design/implementation that need resolution.
- `docs/dev/plans/TODO.md` — Consolidated list of designed-but-unimplemented features with design references.
- `docs/dev/old/PLAN.md` — Historical implementation plan (phases, architecture decisions). Reference for how yoloAI was built.

## Architecture (from design docs)

- Go binary, no runtime deps — just the binary and Docker (or Tart for macOS VMs, or Seatbelt for lightweight macOS sandboxing).
- Pluggable runtime backend via `runtime.Runtime` interface in `internal/runtime/`. Three backends: Docker (`internal/runtime/docker/`), Tart (`internal/runtime/tart/`), and Seatbelt (`internal/runtime/seatbelt/`). CLI dispatches via `newRuntime()` in `internal/cli/helpers.go`. No backend-specific types leak outside their packages.
- Docker containers or Tart VMs with persistent state in `~/.yoloai/sandboxes/<name>/`.
- Containers are ephemeral; state (work dirs, agent-state, logs, meta.json) lives on host. Credentials injected via file-based bind mount (not env vars).
- Agent abstraction: per-agent definitions specify install, launch command, API key env vars, state directory, network allowlist, and prompt delivery mode. Ships Aider, Claude, Codex, Gemini, and OpenCode agents.
- CLI separates workdir (primary project dir, positional) from aux dirs (`-d` flag). Directories mounted at mirrored host paths by default. Custom paths via `=<path>` override.
- `:copy` directories use overlayfs by default (instant setup, deltas-only) with full-copy fallback. Both use git for diff/apply.
- `:rw` directories are live bind-mounts. Default (no suffix) is read-only.
- Profile system: each profile is a directory in `~/.yoloai/profiles/<name>/` containing a `Dockerfile` and `config.yaml`. The base profile at `~/.yoloai/profiles/base/` is auto-created if missing and serves as the default. "base" is a reserved profile name.
- Config in `~/.yoloai/profiles/base/config.yaml` with flat schema (e.g., `backend`, `agent`, `tart.image`). Operational state (`setup_complete`) lives in `~/.yoloai/state.yaml`, separate from user preferences.

## Code Quality Gate

**Before considering any code change complete, run `make check`.** This runs gofmt verification, golangci-lint, go mod tidy check, and all tests. All must pass before committing. If `make check` fails, fix the issues before proceeding. Subagents implementing code changes must include `make check` as a final step.

## Workflow Conventions

- **Critique cycle:** Write a critique in `docs/dev/CRITIQUE.md`, apply corrections to design docs and `docs/dev/RESEARCH.md`, mark critique as done, empty CRITIQUE.md for the next round.
- **Research before design changes:** When a design question comes up (e.g., "should we use overlayfs?"), research it first in `docs/dev/RESEARCH.md` with verified facts, then update design docs based on findings.
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
- **Don't reinvent the wheel.** Before designing a feature, check if existing tools (git, docker, unix utilities) already provide a workflow that solves the problem. Leverage them rather than building a bespoke solution.
- **Ecosystem ergonomics.** The tool should fit naturally within unix philosophy, git workflows, and the CLI ecosystem. Compose well with pipes, familiar tools, and established conventions. A tool that complements the ecosystem is far better than one that needs workarounds to fit user workflows.
