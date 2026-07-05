ABOUTME: Makefile conventions for yoloAI. GNU Make, single top-level Makefile,
ABOUTME: targets are the developer interface, make check is the contract, every
ABOUTME: target .PHONY-listed, help-comments visible via ## prefix.

# Makefile Standard

Reference for the project Makefile. yoloAI has a single Makefile at the repo root; every developer-facing build / test / quality command runs through it.

See also: `../principles/development-principles.md §10` (the quality gate — `make check` is the gate); `../principles/testing-principles.md §5` (test tiers — Makefile targets expose each tier); `SHELL.md` (shell semantics inside Makefile recipes); `GO.md` (Go toolchain conventions the targets invoke).

## Single Makefile

One Makefile at the repo root. No sub-Makefiles. Sub-package builds are invoked through Go's build system, not through recursive Make. The Makefile is GNU Make (BSD make is not supported).

## Targets are the developer interface

If a developer (or CI, or an AI agent) needs to do *anything* with the project — build, run tests, run lint, install dev deps, set capabilities, run smoke tests — there is a Makefile target for it. The README and `CLAUDE.md` reference target names; they do not reference raw `go test` invocations.

Why: convention over configuration (`../principles/development-principles.md §1`). A new contributor types `make help`-equivalent targets to discover what's available. CI invokes the same targets the developer does; "works locally" implies "passes CI."

## `.PHONY` discipline

Every target that is not a file is listed in a single `.PHONY` line:

```make
.PHONY: build test fmt lint tidy-check govulncheck hadolint actionlint check cover \
        integration e2e integration-podman python-test python-typecheck \
        setup-dev-python smoketest smoketest-quick releasetest setcap clean
```

File-producing targets (like `$(BINARY)`) are not phony; their dependencies and timestamps matter.

The `.PHONY` line is exhaustive — every phony target appears there, in one place. Splitting `.PHONY` declarations across the file is rejected (per `../principles/general-principles.md §3` — convention over fragmentation).

## Help comments

Targets that are part of the developer interface carry a `##` prefix help comment immediately above:

```make
## check: run all CI checks locally (same as PR checks)
check: lint tidy-check hadolint actionlint test python-test
```

The convention is `## <target>: <one-line description>`. Multi-line help blocks (for targets with notable side effects or platform constraints) use the same `##` prefix on each continuation line. The `smoketest` and `integration-podman` targets in the current Makefile demonstrate the multi-line form.

Build infrastructure targets (`$(BINARY)`, internal helpers) do not need help comments.

## The `make check` contract

`make check` is the single quality gate (D20, `../principles/development-principles.md §10`):

```make
check: lint tidy-check hadolint actionlint test python-test
```

Concretely, `make check` runs:

1. **`lint`** — gofmt verification + golangci-lint.
2. **`tidy-check`** — verifies `go mod tidy` produces no changes; fails if `go.mod`/`go.sum` are out of date.
3. **`hadolint`** — Dockerfile linter for the base image.
4. **`actionlint`** — GitHub Actions workflow linter.
5. **`test`** — full unit-test suite.
6. **`python-test`** (which runs `python-typecheck` first) — pytest + mypy on the typed Python surface in `runtime/monitor/`.

All must pass. Locally, `make check` is invoked manually before commit and automatically by the Claude Code Stop hook (`.claude/hooks/on-stop.sh`). CI runs the same target.

`make check` does NOT run integration / e2e / smoke tests — those have their own targets (`integration`, `e2e`, `smoketest`) and CI jobs. The gate is "every cheap-to-run check passes."

### What goes in `make check`?

A check belongs in `make check` if and only if:

- It runs in under 30 seconds on a modern developer machine.
- It does not require external infrastructure (no Docker daemon, no Podman daemon, no network).
- It catches a defect that would ship if not caught.

The `python-test` target is the boundary case — it does need Python deps installed, but those installs are near-instant via `uv` (self-provisioned on demand). Per D112 the Python surface is app code, so `uv` is **required**: the target FAILS if `uv` is absent (no silent skip). `uv`/`python3` install everywhere including CI, so they get no carve-out.

## Test tiers — separate targets

Per `../principles/testing-principles.md §5` (test at the right layer), the project has distinct tiers, each with its own Make target:

| Target               | Tier         | Requires                          | Runs in CI?              |
| -------------------- | ------------ | --------------------------------- | ------------------------ |
| `test`               | Unit         | Go toolchain only                 | Yes (in `make check`)    |
| `integration`        | Integration  | Docker daemon                     | Yes (separate CI job)    |
| `integration-podman` | Integration  | Podman with socket                | Yes (W6, commit `b99b46e`) |
| `e2e`                | End-to-end   | Docker + tmux + agent             | No (developer machines)  |
| `smoketest`          | Smoke (full) | Whole host matrix (strict)        | No (pre-release; root on Linux) |
| `smoketest-quick`    | Smoke (quick)| Docker/container core path (strict) | No                     |
| `releasetest`        | Composite    | Everything                        | No (pre-release)         |

Targets do not duplicate logic; `releasetest` invokes the others.

## Variable conventions

- ALL_CAPS for variables: `BINARY`, `VERSION`, `GOFILES`, `EMBEDFILES`, `LDFLAGS`.
- `?=` for variables that the user might want to override (`VERSION ?= dev`).
- `:=` for variables computed once at make-time (`COMMIT := $(shell git rev-parse --short HEAD ...)`).
- `=` (recursive) avoided unless intentional — usually slower and surprises beginners.

## Recipe conventions

- One command per line. Don't chain with `&&` unless the chaining is genuinely atomic.
- `@` prefix when the command's own output is what matters (e.g., `@echo` for sectioning, `@if ... ; then ...; fi` for conditional logic).
- Multi-line recipes use `\` for continuation. The next line is indented by a single tab (Make's requirement; not spaces).
- Recipes that compose multiple shell commands inherit the bash strict-mode discussion in `SHELL.md` *only when explicitly opted in*: by default, each recipe line is a separate shell invocation, and `set -e` is implicit per Make's behaviour for non-zero exit codes.

## Output and observability

- Targets emit visible progress for long-running operations (`@echo "==> Building base image..."`).
- The `cover` target is a worked example of post-processing `go test -coverprofile` output to make the result readable.
- Errors go to stderr via the underlying tool; don't redirect or wrap unless there's a reason.

## What NOT to put in the Makefile

- **Business logic.** The Makefile is the developer interface, not the implementation. Build and test orchestration only.
- **State persisted across invocations** beyond the build artefact and Make's own timestamp mechanism. Anything that needs persistent state lives in `~/.yoloai/` (managed by the binary).
- **Network-fetched runtime dependencies.** Tool versions are pinned (`golangci-lint@v2.11.3`, `govulncheck@latest` is the lone exception — vulncheck-of-the-week semantics are intentional).

## Tool version pinning

External Go tools invoked via `go run`:

- `golangci-lint@v2.11.3` — pinned to a specific version. The version is also pinned in CI; any update is a deliberate change.
- `govulncheck@latest` — intentionally floating; we want the latest vulnerability data.
- `actionlint@latest` — same reasoning.
- `hadolint` runs as a Docker image, not pinned in the Makefile (the Docker tag is pinned by image policy).

Per `../principles/general-principles.md §2` (boring tech, innovation tokens), tool churn is minimised — every version bump is a small risk for marginal benefit. Pin first; floating versions earn their place.

## CI parity

The GitHub Actions workflow invokes `make check`, `make integration`, `make integration-podman` directly. There is no separate CI script. This is the single-source-of-truth principle applied to build orchestration (`../principles/general-principles.md §3` — don't reinvent the wheel — same target name in CI and local).

## Cross-references

- `../principles/development-principles.md §10` — code quality gate (`make check` is the implementation).
- `../principles/testing-principles.md §5` — test layers (Make targets expose them).
- `../principles/general-principles.md §3` — convention over configuration (Make as developer interface).
- `SHELL.md` — shell semantics that recipes inherit.
- `GO.md` — Go toolchain conventions the targets invoke.
