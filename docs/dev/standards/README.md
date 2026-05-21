ABOUTME: Index for yoloAI's per-technology coding standards. Standards explain
ABOUTME: WHAT and HOW; principles under ../principles/ explain WHY. When in
ABOUTME: doubt, the principle wins.

# Standards

Concrete rules for what each kind of file in the yoloAI codebase looks like. Each standard is opinionated; a pick exists for every "what's the convention" question.

Standards live one level below `principles/`. Principles explain **why**; standards explain **what** and **how**.

## Index

| Standard                       | Scope                                                                                                       |
| ------------------------------ | ----------------------------------------------------------------------------------------------------------- |
| [GO.md](GO.md)                 | Go source code (`cmd/`, `internal/`, `runtime/`, `sandbox/`); gofmt, golangci-lint set, package layout, naming, error handling, dependency policy. |
| [CLI.md](CLI.md)               | The `yoloai` CLI: Cobra subcommand layout, flag naming, argument order, exit codes, help text format, stdout/stderr discipline. |
| [SHELL.md](SHELL.md)           | Shell scripts (`.claude/hooks/*.sh`, `scripts/audit/*.sh`, `runtime/docker/resources/entrypoint.sh`, `runtime/monitor/*.sh`); three contexts, strict mode, ShellCheck. |
| [PYTHON.md](PYTHON.md)         | `runtime/monitor/` Python surface; typed pure functions vs I/O seams, pytest, mypy `--strict`. |
| [MAKEFILE.md](MAKEFILE.md)     | `Makefile`; target naming, `.PHONY` discipline, the `make check` contract, test tiers, tool pinning.        |
| [DOCKERFILE.md](DOCKERFILE.md) | Base image at `runtime/docker/resources/Dockerfile` and profile Dockerfiles at `~/.yoloai/profiles/<name>/Dockerfile`; hadolint, apt patterns, layer ordering. |
| [MARKDOWN.md](MARKDOWN.md)     | Documentation prose; ABOUTME headers, heading discipline, table style, cross-reference paths, file-naming conventions. |

## How to use these docs

- **Writing code.** Open the relevant standard alongside what you're working on. Standards are short by design.
- **Reviewing code.** Cite the standard by name and section (e.g., `GO.md §Errors — wrap with %w, never %s`). Style disagreements get resolved here, not in the agent's session log.
- **Onboarding a contributor.** Principles + standards + `CLAUDE.md` is the contract. Anything not in the standards is open.

## How standards change

Standards are versioned in git with the rest of the codebase. To change a rule:

1. Open a PR with the standards edit.
2. Either include the codebase changes that bring existing code into compliance, or note the migration plan in the PR description.
3. The PR description includes the rationale. If the change is non-trivial (touches an axis multiple files depend on), add a D-entry to `../working-notes.md`.

Standards can change without principles changing. A principle is a strategic disposition; a standard is a concrete choice. Standards reflect the current best tradeoff and can evolve as the toolchain or ecosystem does.

## Authority

When a standard conflicts with a principle, the principle wins — fix the standard. When two standards conflict, the more specific one wins (e.g., `CLI.md` > `GO.md` for CLI-specific concerns). When in doubt, ask in the PR.
