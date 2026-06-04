# yoloAI

Sandboxed AI coding agent runner. Runs AI coding CLI agents (Claude Code, Gemini, Codex) inside disposable Docker containers with copy/diff/apply workflow. Additional agents (Aider, Goose, etc.) in future versions.

## Project Status

Public beta. Breaking changes are allowed but must be tracked in `docs/BREAKING-CHANGES.md`.

## Key Files

Docs are organized by **role** — the layer you're operating in. Pick the tier, then read that dir's `README.md` to route.

`docs/` — **users** (running yoloAI):

- `docs/GUIDE.md` — Full usage reference: commands, flags, workdir modes, agents/models, configuration, sandbox state, security, development.
- `docs/BREAKING-CHANGES.md` — Tracks breaking changes made during beta. Each entry documents previous behavior, new behavior, rationale, and migration steps. Include in release notes.
- `docs/ROADMAP.md` — Future plans: agents, network isolation, profiles, overlayfs, etc.

`docs/integrators/` — **integrators** (building on yoloAI as a library/daemon/API): public API reference and embedding guides. Currently a stub (`docs/integrators/README.md`); populated as the public surface stabilizes.

`docs/contributors/` — **contributors** (working on yoloAI itself):

- `architecture/README.md` — Code navigation guide: package map, file index, key types, command→code map, data flows, "where to change" recipes, testing. (`overview.md` is the conceptual-layering companion.) Keep in sync when architecture changes.
- `principles/README.md` — Index of principle docs (general / development / testing / security-sandbox). Principles explain **why** — cite the relevant section when you make a non-obvious design or code choice. A principle wins over any conflicting standard.
- `standards/README.md` — Index of per-technology standards: `go.md`, `cli.md`, `shell.md`, `python.md`, `makefile.md`, `dockerfile.md`, `markdown.md`. Standards explain **what** and **how**.
- `decisions/README.md` — Append-only decision log (D-numbered entries); `decisions/archive.md` holds the older ones. New non-trivial decisions land here first; principles and standards cite D-entries by number. Retroactive entries are flagged `(retroactive)`.
- `design/` — the shaping cluster: feature/design specs (`README.md`, `commands.md`, `config.md`, `setup.md`, `security.md`, …), plus `design/plans/` (designed-but-unimplemented features; `plans/README.md` is the index) and `design/research/` (research topics; `research/README.md` is the index, with backing research for principles under `research/principles/`). The review queues `unresolved-{critiques,questions,findings}.md` (each with a `resolved-*.md` history sink) also live here.
- `backend-idiosyncrasies.md` — **Read this before diagnosing any backend problem.** Catalogs observed behaviors that contradict official documentation, required non-obvious workarounds, or have caused bugs before. Includes a symptom index for fast lookup.
- `archive/` — completed/superseded plans, research, investigations, and design specs kept for history (includes the original `old/PLAN.md` and phase notes). Not live references.

**Doc conventions.** Every directory's `README.md` is its index. Filenames are lowercase kebab-case and name the subject, not its status. Three content-retirement patterns: **item-queues** keep active items in `unresolved-<topic>.md` and drain each item to one of three co-located sinks — `resolved-<topic>.md` (done: answered/fixed/applied), `deferred-<topic>.md` (parked "not now"; carries a **`Trigger:`** revival condition and can flow back to unresolved), or `abandoned-<topic>.md` (permanently "won't do"; carries a **`Why:`**) — for critiques, questions, and findings; **append-only logs** (`decisions/`) grow and age-split; **file-documents** (plans, specs, research spikes) move whole to `archive/` when complete.

## Architecture (from design docs)

- Go binary, no runtime deps — just the binary and Docker (or Tart for macOS VMs, or Seatbelt for lightweight macOS sandboxing). One narrow exception: in-place `reset` (resetting a sandbox while the agent keeps running, container backends only) shells out to host `rsync` for a differential workdir resync — see `rsyncDir` in `internal/sandbox/lifecycle/reset.go` for why a wipe-then-copy can't be used there. It is not needed for normal create/run/restart; the lifecycle tests skip when `rsync` is absent.
- Pluggable runtime backend via `runtime.Runtime` interface in `internal/runtime/`. Three backends: Docker (`internal/runtime/docker/`), Tart (`internal/runtime/tart/`), and Seatbelt (`internal/runtime/seatbelt/`). CLI dispatches via `newRuntime()` in `internal/cli/helpers.go`. No backend-specific types leak outside their packages.
- Docker containers or Tart VMs with persistent state in `~/.yoloai/sandboxes/<name>/`.
- Containers are ephemeral; state (work dirs, agent-state, logs, meta.json) lives on host. Credentials injected via file-based bind mount (not env vars).
- Agent abstraction: per-agent definitions specify install, launch command, API key env vars, state directory, network allowlist, and prompt delivery mode. Ships Aider, Claude, Codex, Gemini, and OpenCode agents.
- CLI separates workdir (primary project dir, positional) from aux dirs (`-d` flag). Directories mounted at mirrored host paths by default. Custom paths via `=<path>` override.
- `:copy` directories use full directory copies with git for diff/apply.
- `:overlay` directories use Linux overlayfs inside the container for instant setup with diff/apply workflow. Changes are captured in an upper layer; no file copying. Docker-only, requires CAP_SYS_ADMIN. Container must be running for diff/apply (git commands exec inside container).
- `:rw` directories are live bind-mounts. Default (no suffix) is read-only.
- Profile system: each profile is a directory in `~/.yoloai/profiles/<name>/` containing a `Dockerfile` and `config.yaml`. The base profile at `~/.yoloai/profiles/base/` is auto-created if missing and serves as the default. "base" is a reserved profile name.
- Two config files: global config (`~/.yoloai/config.yaml`) for user preferences (tmux_conf, model_aliases) and profile config (`~/.yoloai/profiles/base/config.yaml`) for profile-overridable defaults (agent, model, backend, env, etc.). `IsGlobalKey()` routes config commands to the correct file. Operational state (`setup_complete`) lives in `~/.yoloai/state.yaml`.

## Code Quality Gate

**Before considering any code change complete, run `make check`.** This runs gofmt verification, golangci-lint, go mod tidy check, all Go tests, and the Python test/typecheck targets. All must pass before committing. If `make check` fails, fix the issues before proceeding. Subagents implementing code changes must include `make check` as a final step.

The Python portion (`make python-test` + `make python-typecheck`) covers the typed pure-function surface in `runtime/monitor/setup_helpers.py` and its tests under `runtime/monitor/tests/`, plus the smoke harness. mypy/pytest run from a [uv](https://docs.astral.sh/uv/)-managed `.venv` pinned to exact versions by `internal/runtime/monitor/tests/requirements-dev.lock` — this decouples the checks from whatever ambient `python3 -m mypy` is installed (an out-of-spec ambient mypy was producing phantom `--strict` errors). When `uv` is present the targets self-provision the venv on demand (no manual step); when `uv` is absent they skip silently, so fresh clones still get a green `make check`. `make setup-dev-python` is an optional explicit provision (used by CI, fails loudly if uv is missing). After editing `requirements-dev.txt`, regenerate the lock with `uv pip compile --generate-hashes`.

For Claude Code users, this is enforced automatically: `.claude/settings.json` registers hooks that stamp the project when a source file is edited and run `make check` at end of turn if the stamp exists. On failure, the Stop hook blocks completion and feeds the output back. The hook scripts live at `.claude/hooks/post-edit.sh` and `.claude/hooks/on-stop.sh` and are committed so any clone of the repo picks them up.

## Workflow Conventions

- **Critique cycle:** Write a critique in `docs/contributors/design/unresolved-critiques.md`, apply corrections to design docs and research files in `docs/contributors/design/research/`, then drain the item to one of the three sinks: `resolved-critiques.md` once applied, `deferred-critiques.md` if parked "not now" (must add a `Trigger:` line stating what revives it), or `abandoned-critiques.md` if dropped (must add a `Why:` line). A deferred item flows back to the unresolved file when its trigger fires. Findings discovered mid-work follow the same flow via `unresolved-findings.md` → `resolved-/deferred-/abandoned-findings.md`; open questions likewise via `unresolved-questions.md`.
- **Research before design changes:** When a design question comes up (e.g., "should we use overlayfs?"), research it first in the appropriate file under `docs/contributors/design/research/` with verified facts, then update design docs based on findings.
- **Factual accuracy matters:** Star counts, feature claims, and security assertions must be verified. Don't repeat marketing language or unverifiable numbers.
- **Cross-platform awareness:** Always consider Linux, macOS (Docker Desktop + VirtioFS), and Windows/WSL. Note platform-specific tradeoffs explicitly.
- **Commit granularity:** One commit per logical change. Research, design updates, and critique application get separate commits.
- **Backend debugging:** Before diagnosing a backend problem (containerd, Kata, CNI, Docker, Podman, Tart, Seatbelt), read `docs/contributors/backend-idiosyncrasies.md`. Use the symptom index to jump directly to the relevant entry. Do not repeat investigation that is already documented there.
- **Recording new idiosyncrasies:** When you discover a backend behavior that contradicts documentation, required a surprising workaround, or could cause the same bug again — add an entry to `docs/contributors/backend-idiosyncrasies.md`. Add a row to the symptom index. Keep entries concise: symptom, explanation, fix, code pointer. Do this before committing the fix.

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
- `:copy` uses full copies; `:overlay` is an explicit opt-in for instant setup with overlayfs.
- Safe defaults: read-only mounts, no implicit `agent_files` inheritance, name required (no auto-generation), dirty repo warning (not error).
- CLI for one-offs, config for repeatability (same options in both).
- Security requires dedicated research — don't finalize ad-hoc. `CAP_SYS_ADMIN` tradeoff is documented.
- **Don't reinvent the wheel.** Before designing a feature, check if existing tools (git, docker, unix utilities) already provide a workflow that solves the problem. Leverage them rather than building a bespoke solution.
- **Ecosystem ergonomics.** The tool should fit naturally within unix philosophy, git workflows, and the CLI ecosystem. Compose well with pipes, familiar tools, and established conventions. A tool that complements the ecosystem is far better than one that needs workarounds to fit user workflows.
