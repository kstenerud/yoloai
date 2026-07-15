> **ABOUTME:** Shell-script conventions for yoloAI — why audit scripts, Claude Code hook scripts,
> and the container entrypoint trampoline each need a different strict-mode and shebang
> contract, plus the ShellCheck and composition discipline they share. Read this before adding a
> script rather than picking a strict-mode convention by guessing.

# Shell Script Standard

Reference for shell scripts in yoloAI: `.claude/hooks/*.sh`, `scripts/*.sh`, `runtime/docker/resources/entrypoint.sh`, `runtime/monitor/diagnose-idle.sh`.

See also: `../principles/development-principles.md §5` (fail fast — applies to scripts too); `../principles/general-principles.md §3` (don't reinvent — compose with grep, awk, jq, git rather than rewriting in Python); `MAKEFILE.md` for `make check` integration.

## Three contexts, three sets of rules

yoloAI has three classes of shell script, each with different constraints:

| Context                                            | Shebang             | Strict mode          | Why                                                                                                                                  |
| -------------------------------------------------- | ------------------- | -------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| **Dev tooling** (`scripts/*.sh`)                   | `#!/usr/bin/env bash` | `set -euo pipefail` | Runs on developer machines + CI. Fail loudly on any error. ABOUTME header. Output goes to stdout for the developer.                  |
| **Hook scripts** (`.claude/hooks/*.sh`)            | `#!/bin/bash`         | `set -u`             | Runs from Claude Code's hook surface. Must not block on errors (exit 0 even on minor issues); the hook is a stamping / signaling mechanism, not a quality gate itself. |
| **Container entrypoint trampoline** (`runtime/docker/resources/entrypoint.sh`) | `#!/bin/sh`           | `set -e`             | Runs inside a minimal Debian container. `sh` (not `bash`) for portability — the trampoline must work before any package install. Hands off to Python ASAP. |

The split is deliberate; do not unify the rules. Mixing the strict-mode of the audit script into a hook would cause Claude Code completions to block on transient failures; using bashisms in the entrypoint trampoline would risk breaking the container boot on a base image without bash.

## ABOUTME header (audit + diagnostic scripts only)

Audit scripts and standalone diagnostic scripts carry an ABOUTME header (project convention from `~/.claude/CLAUDE.md`):

```bash
#!/usr/bin/env bash
# ABOUTME: One-line description of what this script does
# ABOUTME: and why it exists (the why is more important than the what).
set -euo pipefail
```

Hook scripts and the container entrypoint trampoline are exempt — they are part of the project's runtime contract, not standalone utilities; their purpose is documented in their adjacent docs (`docs/contributors/architecture/README.md`, `docs/contributors/design/bugreport.md`).

## Strict mode

For audit scripts and any non-trivial bash script:

```bash
set -euo pipefail
```

- `-e` — exit on first command failure.
- `-u` — error on unset variable references (catches typos like `$pat_h`).
- `-o pipefail` — `cmd1 | cmd2` exits non-zero if *any* command in the pipeline fails (default behaviour without this option only checks `cmd2`).

For hook scripts (where exiting non-zero would block Claude Code completion):

```bash
set -u
```

`-u` alone catches typos without forcing a non-zero exit on every minor error. The hook is responsible for its own error handling.

For the container entrypoint trampoline (where `sh` is in use):

```bash
set -e
```

`sh` doesn't reliably support `-u` and `-o pipefail` across Debian's `/bin/sh` symlink targets. `-e` is the portable baseline.

## ShellCheck

Audit scripts and hook scripts pass `shellcheck` clean. The container entrypoint trampoline is `sh`-targeted, so `shellcheck` with `-s sh` is the right invocation if checked.

Suppressions (`# shellcheck disable=SCnnnn`) follow the same justification rule as Go lint suppressions (per `../principles/development-principles.md §6`): the comment immediately above the suppression explains *why the finding does not apply*, not "ShellCheck complained."

```bash
# shellcheck disable=SC2086  # intentional word-splitting for the IPV4 list
ipv4_args=$(echo $IPV4_HOSTS | tr ',' ' ')
```

## Compose with unix tools — don't shell-script around them

Shell is the glue, not the implementation. The right pattern:

- **Use `jq` for JSON.** Don't parse JSON with `grep` + `sed`.
- **Use `git` directly for repo queries.** Don't reimplement `git diff` logic.
- **Use `find` for file matching.** Don't loop with `for f in *` over a deep tree.
- **Use `awk` for column-shaped data.** Don't write fragile `cut` + `tr` chains.

`scripts/next-id.sh` is a canonical example — it composes `grep`, `sort` and `tr` over a globbed
corpus to answer one question, and its ABOUTME says why the glob (not a hand-written file list)
is load-bearing.

## When NOT to use shell

If the script needs:

- **State across calls** beyond a single environment variable — write Go (or Python for `runtime/monitor/` style).
- **Structured error handling with typed errors** — write Go.
- **More than ~100 lines of logic** — write Go.
- **Cross-platform support** (Windows / macOS / Linux) — write Go.

The pluggable idle-detection rewrite (D14) is the canonical example: an early shell-based polling approach was rejected because the failure-mode reasoning needed structured types. The implementation moved to Python (which has typing, pytest, mypy).

## Hook script specifics

`.claude/hooks/*.sh` are the project's quality-gate enforcement (D20). Constraints:

- **Exit 0 on every path except deliberate block.** Claude Code interprets non-zero as "hook failed; block completion." Reserve that for the cases that should block (e.g., `make check` failed in `on-stop.sh`).
- **No interactive prompts.** Hooks run non-interactively; reading stdin will hang the session.
- **Stamp-and-signal, not perform-and-decide.** `post-edit.sh` stamps `.claude/.cache/make-check-pending` when a source file is edited. `on-stop.sh` reads the stamp and runs `make check`. The two-step design keeps each hook short and single-purpose.

## Audit script output

Audit scripts write to stdout. Use markdown-style section headers (`## fmt.Errorf totals`) so the output is readable directly and pastable into PR descriptions. Reserve stderr for warnings and tool-error output (the same convention `standards/CLI.md` describes for Go binaries).

## File naming

- Dev tooling: `scripts/<topic>.sh` — one topic per file.
- Hook scripts: `.claude/hooks/<event>.sh` (e.g., `post-edit.sh`, `on-stop.sh`).
- Container scripts: `runtime/<backend>/resources/<purpose>.sh`.
- Diagnostic scripts: `runtime/<area>/<verb>-<noun>.sh` (e.g., `diagnose-idle.sh`).

Lowercase-kebab-case. No `.bash` extension; `.sh` for all, with the shebang naming the actual interpreter.

## Cross-references

- `../principles/development-principles.md §5` (fail fast) — `set -e` is the shell version.
- `../principles/development-principles.md §6` (warnings are signal) — ShellCheck findings are warnings; same justification rule applies.
- `../principles/general-principles.md §3` (don't reinvent the wheel) — compose with unix tools rather than reimplementing them in shell.
- `MAKEFILE.md` — `make check` invokes shell-style targets; the conventions here apply.
- `PYTHON.md` — when a script grows past shell's comfortable size, the move is to Python in `runtime/monitor/` style.
