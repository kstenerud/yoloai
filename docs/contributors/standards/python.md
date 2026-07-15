ABOUTME: Python conventions for yoloAI's runtime/monitor/ surface. Typed pure
ABOUTME: functions split from I/O seams (W3/W4), pytest + mypy --strict, the
ABOUTME: small Python surface exists because the in-container monitor needs
ABOUTME: structured types Go can't give us from the host.

# Python Standard

Reference for Python code in yoloAI: `runtime/monitor/`. Python is intentionally a narrow surface; the project is Go-first.

See also: `../principles/general-principles.md §2` (innovation tokens — Python earned its place when the in-container idle monitor needed typed structures); `../principles/development-principles.md §1` (engineering values — apply to Python the same way they apply to Go); `../principles/testing-principles.md §5` (test at the right layer — pure functions vs. I/O seams); `MAKEFILE.md §The make check contract` (pytest + mypy run in `make check`).

## Why Python exists at all in yoloAI

Python is a *single deliberate token spend* (`../principles/general-principles.md §2`). `runtime/monitor/` is not the only Python in the project — 23 tracked `.py` files exist repo-wide, including `scripts/` (smoke harness, `govulncheck.py`) and the `runtime/docker/resources/` in-container scripts — but `runtime/monitor/` is the surface this standard covers: the pluggable-idle-detection surface the token spend was justified for. Reasons:

- The status monitor runs *inside the sandbox container* alongside the agent. It needs structured types, JSON handling, and pytest-style test coverage — not a Go binary.
- The base Docker image already includes Python 3 + pip for agent installation (Claude Code, Codex, Gemini CLI all install via npm/pip-adjacent tooling). Adding Python *here* costs nothing additional.
- The pluggable idle-detection design (D14) required structured types for `IdleSupport` strategies, and Python's typing + pytest made the W3/W4 architecture remediation possible.

The token rule: no additional Python surfaces without a D-entry justifying the spend.

## File layout

```
runtime/monitor/
├── sandbox-setup.py       # entrypoint script (run inside container)
├── setup_helpers.py       # pure functions extracted from sandbox-setup (W3)
├── status-monitor.py      # status monitor daemon (writes agent-status.json)
├── tmux_io.py             # I/O seam for tmux interactions (W4)
├── monitor.go             # Go side of the monitor surface
├── diagnose-idle.sh       # diagnostic shell wrapper
└── tests/
    ├── conftest.py
    ├── requirements-dev.txt
    └── test_*.py
```

## Python version

Python 3.11+. The base image ships `python3` from Debian bookworm-slim (currently 3.11). Don't use 3.12-only syntax (e.g., `type` statements for type aliases) until the base image is bumped — there's no business case yet.

## Typing — `mypy --strict`

Every file in `runtime/monitor/` that ships in the binary (not test code) is type-annotated and passes `mypy --strict`. The Makefile `python-typecheck` target enforces this:

```make
python-typecheck:
    @if python3 -m mypy --version >/dev/null 2>&1; then \
        python3 -m mypy --strict runtime/monitor/setup_helpers.py runtime/monitor/tmux_io.py runtime/monitor/tests/; \
    else \
        echo "Python type-check skipped (install mypy via 'make setup-dev-python' to enable)"; \
    fi
```

The pattern:

```python
from __future__ import annotations

from typing import Any


def read_runtime_config(path: str, expected_schema_version: int = RUNTIME_CONFIG_SCHEMA_VERSION) -> dict[str, Any]:
    """Docstring."""
    ...
```

`from __future__ import annotations` is required (allows `list[str]` / `dict[str, Any]` annotation syntax on Python 3.9+ that gets parsed as string-form).

## Pure functions vs I/O seams

The W3/W4 split (commits `0d50c54`, `41561fe`, 2026-05-20) is the canonical pattern:

- `setup_helpers.py` — *pure functions*. Read files when given a path; never touch tmux, subprocess, or process environment. Type-checked under mypy --strict; tested as pure functions.
- `tmux_io.py` — *I/O seam*. Encapsulates tmux interactions. Tested with explicit fixtures; can be faked.
- `sandbox-setup.py` — *wrapper*. Composes pure functions and I/O seams; has the actual side effects.

This is the "test at the right layer" principle (`../principles/testing-principles.md §5`) applied to Python. The split was the W3 + W4 work item; it pays back every time a test runs against `setup_helpers.py` without spawning processes.

## Testing — pytest

- **Framework**: pytest (`runtime/monitor/tests/requirements-dev.txt` pins the version).
- **Pattern**: table-driven where applicable; pytest fixtures for any common setup.
- **Coverage**: not gated on a percentage (per `../principles/testing-principles.md §1`). The gate is "does the test catch a real failure mode."
- **Skip discipline**: `make python-test` skips silently if pytest isn't installed; CI installs pytest via `setup-dev-python` and treats the target as required.

```python
import pytest

def test_read_runtime_config_missing_schema_version(tmp_path):
    path = tmp_path / "runtime-config.json"
    path.write_text('{"some_field": "value"}')
    # Missing schema_version is tolerated (legacy file).
    result = read_runtime_config(str(path), expected_schema_version=1)
    assert result == {"some_field": "value"}


def test_read_runtime_config_mismatched_schema_version(tmp_path):
    path = tmp_path / "runtime-config.json"
    path.write_text('{"schema_version": 99, "some_field": "value"}')
    with pytest.raises(RuntimeError, match="schema_version"):
        read_runtime_config(str(path), expected_schema_version=1)
```

## Style

- **PEP 8** with `black`-like formatting in mind. No formatter is currently enforced in CI (the project is Go-first); but new Python code should not deviate from PEP 8 without reason.
- **f-strings** over `%`-formatting and `str.format`. Modern Python.
- **Type hints on every public function**. Internal helpers may skip type hints if they're trivial; the mypy `--strict` target will enforce on the listed files.
- **No `*` imports.** Explicit imports only.
- **Docstrings on public functions and classes.** One-line summary; expand if non-obvious.

## Error handling

- **Raise specific exceptions**, not bare `Exception`. The W2 schema-version check raises `RuntimeError` with a specific message; downstream code can catch it.
- **No silent fallbacks** on data shape. The `read_runtime_config` example is the canonical pattern: if `schema_version` is *present* but *wrong*, raise; if *missing*, tolerate (it's legacy data).
- This is `../principles/development-principles.md §5` (fail fast) applied in Python.

## Imports

Standard import groups, separated by blank lines:

```python
from __future__ import annotations

import json
import os
import subprocess
from typing import Any

import pytest  # third-party, separate group

from .setup_helpers import read_runtime_config  # local, separate group
```

`isort`-style grouping. Not currently enforced by CI, but new code should follow the convention.

## When to write Python vs Go

Python is for *in-container code that needs structured types* (the status monitor, runtime-config schema validation, setup helpers). Everything else — CLI, host-side orchestration, runtime backends, sandbox lifecycle — is Go.

If you're tempted to add a Python file outside `runtime/monitor/`, stop and ask: is this a new Python surface? If yes, that's a D-entry decision (`../working-notes.md`) — Python is a token spend.

## ABOUTME header

Every Python source file in `runtime/monitor/` opens with an ABOUTME comment block (`MARKDOWN.md §ABOUTME`):

```python
# ABOUTME: One-line description of what this file does
# ABOUTME: Continue on second line if needed; keep under 80 chars each.
"""Module docstring.
...
"""
```

The ABOUTME comments come *before* the module docstring; the module docstring is for human-readable narrative.

## Pip dependencies

- **Test deps** in `runtime/monitor/tests/requirements-dev.txt`. Pinned versions.
- **No runtime pip deps.** The status monitor uses only the standard library. This is a deliberate constraint — adding a runtime pip dep would require pip-install in the base Docker image, and the install would happen on every base-image rebuild. The complexity isn't worth it for the scope.

If a runtime dep is needed, that's a D-entry decision.

## What NOT to put in `runtime/monitor/`

- **Async code.** No `asyncio` patterns. The monitor is a single-process daemon; threading + queues if concurrency is needed (currently it isn't).
- **Web frameworks / servers.** Not the surface.
- **Complex parsing.** The monitor reads runtime-config.json and emits agent-status.json; that's the data surface.
- **Anything resembling a build system.** The Makefile invokes pytest + mypy directly; no setup.py / pyproject.toml beyond what testing tooling expects.

## Cross-references

- `../principles/general-principles.md §2` — innovation tokens (Python is a deliberate spend).
- `../principles/development-principles.md §5` (fail fast) and `§7` (justify-every-discard) — same in Python as in Go.
- `../principles/testing-principles.md §5` (right layer) — pure functions vs I/O seams.
- `MAKEFILE.md` — `make python-test`, `make python-typecheck`, `make setup-dev-python`.
- `../working-notes.md` D14 (pluggable idle detection — origin of the Python surface).
- `../working-notes.md` D19 (W3, W4 — architecture remediation that made the Python surface testable).
