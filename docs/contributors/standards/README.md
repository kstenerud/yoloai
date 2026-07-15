ABOUTME: Index for yoloAI's per-technology coding standards. Standards explain
ABOUTME: WHAT and HOW; principles under ../principles/ explain WHY. When in
ABOUTME: doubt, the principle wins.

# Standards

Concrete rules for what each kind of file in the yoloAI codebase looks like. Each standard is opinionated; a pick exists for every "what's the convention" question.

Standards live one level below `principles/`. Principles explain **why**; standards explain **what** and **how**.

## Index

| Standard                       | Scope                                                                                                       |
| ------------------------------ | ----------------------------------------------------------------------------------------------------------- |
| [GO.md](go.md)                 | Go source code (`cmd/`, `internal/`, `runtime/`, `sandbox/`); gofmt, golangci-lint set, package layout, naming, error handling, dependency policy. |
| [CLI.md](cli.md)               | The `yoloai` CLI: Cobra subcommand layout, flag naming, argument order, exit codes, help text format, stdout/stderr discipline. |
| [SHELL.md](shell.md)           | Shell scripts (`.claude/hooks/*.sh`, `scripts/audit/*.sh`, `runtime/docker/resources/entrypoint.sh`, `runtime/monitor/*.sh`); three contexts, strict mode, ShellCheck. |
| [PYTHON.md](python.md)         | `runtime/monitor/` Python surface; typed pure functions vs I/O seams, pytest, mypy `--strict`. |
| [MAKEFILE.md](makefile.md)     | `Makefile`; target naming, `.PHONY` discipline, the `make check` contract, test tiers, tool pinning.        |
| [DOCKERFILE.md](dockerfile.md) | Base image at `runtime/docker/resources/Dockerfile` and profile Dockerfiles at `~/.yoloai/profiles/<name>/Dockerfile`; hadolint, apt patterns, layer ordering. |
| [MARKDOWN.md](markdown.md)     | Documentation prose; ABOUTME headers, heading discipline, table style, cross-reference paths, file-naming conventions. |

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

When a standard conflicts with a principle, the principle wins — fix the standard. When two standards conflict, the more specific one wins (e.g., `CLI.md` > `GO.md` for CLI-specific concerns). When a standard conflicts with the executable artifact it documents (`Makefile`, `ci.yml`, `.claude/hooks/*.sh`), **the artifact wins** — fix the standard in the same commit that changes the artifact, not later. This has drifted before: `check:`'s prerequisite list changed three times (`e827d11b`, `eb231d87`, `05dc06bd`) without a matching `MAKEFILE.md` edit, and the eventual catch-up commit (`c7f08466`) fixed `MAKEFILE.md`'s test-tier table (`python-test` made required, no silent skip, per D112) but left `PYTHON.md`'s "skips silently if pytest isn't installed" claim stale. When in doubt, ask in the PR.
