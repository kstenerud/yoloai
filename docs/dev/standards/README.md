ABOUTME: Index for yoloAI's per-technology coding standards. Standards explain
ABOUTME: WHAT and HOW; principles under ../principles/ explain WHY. When in
ABOUTME: doubt, the principle wins.

# Standards

Concrete rules for what each kind of file in the yoloAI codebase looks like. Each standard is opinionated; a pick exists for every "what's the convention" question.

Standards live one level below `principles/`. Principles explain **why**; standards explain **what** and **how**.

## Index

| Standard                                       | Status              | Scope                                                                                                       |
| ---------------------------------------------- | ------------------- | ----------------------------------------------------------------------------------------------------------- |
| [GO.md](GO.md)                                 | Phase 3 (planned)   | Go source code (`cmd/`, `internal/`, `runtime/`); gofmt, golangci-lint set, package layout, naming, errors. |
| [CLI.md](CLI.md)                               | Phase 3 (planned)   | The `yoloai` CLI: Cobra subcommands, flag naming, argument order, exit codes, help text format.             |
| [SHELL.md](SHELL.md)                           | Phase 3 (planned)   | Shell scripts (`.claude/hooks/*.sh`, container entrypoint, install scripts); bash strict mode, ShellCheck.  |
| [PYTHON.md](PYTHON.md)                         | Phase 3 (planned)   | `runtime/monitor/` and other Python helpers; typing, pytest, mypy, pure-function discipline.                |
| [MAKEFILE.md](MAKEFILE.md)                     | Phase 3 (planned)   | `Makefile`; target naming, `make check` contract, `.PHONY` discipline.                                      |
| [DOCKERFILE.md](DOCKERFILE.md)                 | Phase 3 (planned)   | Profile Dockerfiles in `~/.yoloai/profiles/<name>/Dockerfile` and `runtime/docker/Dockerfile.base`.         |
| [MARKDOWN.md](MARKDOWN.md)                     | Phase 3 (planned)   | Documentation prose; heading / table / cross-reference discipline; ABOUTME header for source files.         |

## During Phase 1 / 2

The two existing standards still live at their old paths:

- `../CODING-STANDARD.md` — Go code style (will move to `standards/GO.md` in Phase 3).
- `../CLI-STANDARD.md` — CLI conventions (will move to `standards/CLI.md` in Phase 3).

When a principles doc cites the standard, it cites the eventual `standards/` path. The standards file itself is moved in Phase 3 with no content drift.

## How to use these docs

- **Writing code.** Open the relevant standard alongside what you're working on. Standards are short by design.
- **Reviewing code.** Cite the standard by name and section (e.g., `GO.md §Errors — wrap with `%w`, never `%s`). Style disagreements get resolved here, not in the agent's session log.
- **Onboarding a contributor.** Principles + standards + `CLAUDE.md` is the contract. Anything not in the standards is open.

## How standards change

Standards are versioned in git with the rest of the codebase. To change a rule:

1. Open the PR with the standards edit.
2. Either include the codebase changes that bring existing code into compliance, or note the migration plan in the PR description.
3. The PR description includes the rationale. If the change is non-trivial (touches an axis multiple files depend on), add a D-entry to `../working-notes.md`.

Standards can change without principles changing. A principle is a strategic disposition; a standard is a concrete choice. Standards reflect the current best tradeoff and can evolve as the toolchain or ecosystem does.

## Authority

When a standard conflicts with a principle, the principle wins — fix the standard. When two standards conflict, the more specific one wins (e.g., `CLI.md` > `GO.md` for CLI-specific concerns). When in doubt, ask in the PR.
