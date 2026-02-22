# Coding Standard

Reference for consistent code style and practices across the yoloai codebase.

Based on PEP 8, PEP 257, PEP 484, Google Python Style Guide, and community conventions — filtered for a modern Python 3.10+ CLI project where navigability, clarity, and maintainability matter most.

See also: global `CLAUDE.md` for language-agnostic naming philosophy and comment principles.

## Language and Runtime

- **Python 3.10+** (minimum supported version)
- Use type hints throughout — all function signatures, return types, and non-obvious locals
- **Type checker:** mypy (strict mode). Type hints without a checker are cosmetic. Configure in `pyproject.toml`.
- Use `from __future__ import annotations` in all modules. This makes all annotations strings at runtime, which avoids forward reference issues and enables newer syntax on older Python. Note: this can break libraries that inspect annotations at runtime (e.g., Pydantic v1) — test accordingly if adding such dependencies.

## Formatting and Linting

- **Formatter:** ruff format (black-compatible, faster)
- **Linter:** ruff (replaces flake8, isort, pyflakes, etc.)
- **Line length:** 100 characters (PEP 8 allows up to 99 for teams; we use 100 for consistency with the CLI standard)
- **Quotes:** double quotes for strings (ruff format default)
- Configure in `pyproject.toml` — no separate config files for formatting/linting

### Ruff Rule Sets

Explicitly enable these rule sets in `pyproject.toml`:

| Code | Rules | Purpose |
|------|-------|---------|
| `E`, `W` | pycodestyle | Basic style errors and warnings |
| `F` | pyflakes | Unused imports, undefined names |
| `I` | isort | Import ordering |
| `UP` | pyupgrade | Upgrade syntax to modern Python |
| `C4` | flake8-comprehensions | Simplify comprehensions |
| `SIM` | flake8-simplify | Simplify code constructs |
| `TCH` | flake8-type-checking | Move type-only imports behind `TYPE_CHECKING` |
| `PTH` | flake8-use-pathlib | Prefer `pathlib` over `os.path` |
| `RET` | flake8-return | Simplify return statements |
| `D` | pydocstyle | Docstring conventions (Google style) |

Use `per-file-ignores` for exceptions:
- `__init__.py`: allow unused imports (`F401`) — re-exports are intentional
- `tests/**`: relax type annotation requirements (`ANN`), allow assertions (`S101`)

### Pre-commit

Use [pre-commit](https://pre-commit.com/) to enforce formatting and linting before commits. Hooks: `ruff check --fix`, `ruff format`, `mypy`.

## Project Structure

```
yoloai/
├── pyproject.toml          # Project metadata, deps, tool config
├── src/
│   └── yoloai/
│       ├── __init__.py
│       ├── py.typed         # PEP 561 marker for type checker support
│       ├── cli/             # CLI entry points and argument parsing
│       ├── core/            # Business logic (sandbox lifecycle, etc.)
│       ├── agents/          # Agent preset definitions
│       ├── docker/          # Docker client interactions
│       └── config/          # Config file parsing, profiles
├── tests/
│   ├── unit/                # Fast, no external deps
│   │   ├── test_sandbox.py  # Mirrors src/yoloai/core/sandbox.py
│   │   └── ...
│   ├── integration/         # Needs Docker
│   └── conftest.py          # Shared fixtures (top-level)
└── resources/               # Dockerfiles, templates, static assets
```

## File Organization

- **One primary class per module.** Small helper types used only by that class can live in the same file. File name matches the primary type: class `SandboxManager` → `sandbox_manager.py`.
- **Files over 400 lines:** consider whether there are multiple responsibilities that should be split.
- **Functions over 40 lines:** consider whether it can be decomposed into named steps.
- These aren't hard limits — a long file with one coherent responsibility is fine. A short file with three unrelated concerns is not.

## Testing

- **Framework:** pytest
- **Test location:** `tests/` directory. Within `unit/` and `integration/`, mirror `src/yoloai/` structure.
- **Naming:** `test_<module>.py` files, `test_<behavior>` functions. Test names describe the scenario: `test_create_sandbox_fails_when_docker_unavailable` not `test_create`.
- **Fixtures:** prefer factory functions over complex fixture hierarchies. Use `conftest.py` at the appropriate directory level — shared fixtures at the top, module-specific fixtures in subdirectories.
- **Mocking:** mock at the boundary (Docker client, filesystem), not internal functions
- **Markers:** use `@pytest.mark.integration` for tests requiring Docker. Register custom markers in `pyproject.toml`.
- **Parametrize:** use `@pytest.mark.parametrize` to reduce test duplication for multiple inputs/scenarios.
- Run with: `pytest tests/unit` (fast), `pytest tests/integration` (needs Docker)
- All new functionality requires tests. Bug fixes require a regression test.

## Naming

Follow PEP 8 conventions:

| Thing | Convention | Example |
|-------|-----------|---------|
| Modules | `snake_case` | `sandbox_lifecycle.py` |
| Classes | `PascalCase` | `SandboxManager` |
| Functions/methods | `snake_case` | `create_sandbox()` |
| Constants | `UPPER_SNAKE_CASE` | `DEFAULT_DISK_LIMIT` |
| Private | `_leading_underscore` | `_build_docker_args()` |
| Type variables | `PascalCase`, short | `T`, `ConfigT` |
| Type aliases | `PascalCase` | `AgentConfig` |

Names describe **what**, not **how** — except where "how" distinguishes specializations (e.g., `hash_lookup` vs `linear_lookup`).

### Clarity over brevity

Names must be understandable to someone unfamiliar with the codebase. When you encounter a variable mid-function, its role should be obvious without scrolling to its declaration.

- **Spell words out:** `container_name` not `ctr_nm`, `directory` not `dir`
- **Accepted short forms** (universal enough to need no explanation): `i`, `j`, `k` (loop indices), `args` (arguments), `ctx` (context), `src`/`dst` (source/destination), `tmp` (temporary), `err` (error), `fmt` (format), `fn` (function), `idx` (index), `msg` (message), `cmd` (command), `cfg` (config)
- **A name that needs a comment to explain it is too short or too vague** — rename it instead of adding the comment
- **Parameters are part of the public interface** — a function signature should read almost like documentation: `create_sandbox(name: str, agent: AgentPreset, directories: list[Path])` over `create_sandbox(n: str, a: AgentPreset, d: list[Path])`

## Imports

Order (enforced by ruff `I` rules):
1. Standard library
2. Third-party packages
3. Local imports

Blank line between each group. Use absolute imports from the package root: `from yoloai.core.sandbox import Sandbox`, not relative imports. This is stricter than PEP 8 (which permits explicit relative imports) but simpler — every import shows its full path.

## Error Handling

- Use custom exception hierarchy rooted at `YoloaiError`
- Catch specific exceptions, not bare `except:`
- Validate at system boundaries (CLI input, config files, Docker responses), trust internal code
- Error messages: lowercase, no trailing period, actionable (same as CLI standard)
- Never swallow exceptions silently — log or re-raise
- Include context in exceptions: what was being done, what went wrong

```python
class YoloaiError(Exception):
    """Base for all yoloai errors."""

class ConfigError(YoloaiError):
    """Invalid or missing configuration."""

class SandboxError(YoloaiError):
    """Sandbox lifecycle failure."""

class AgentError(YoloaiError):
    """Agent setup or execution failure."""
```

## Docstrings and Comments

- **Module-level:** Every source file starts with `# ABOUTME: <purpose>` (project convention for quick scanning). Additionally, public modules should have a PEP 257 module docstring for `help()` and documentation generators.
- **Public API:** Docstrings on public classes and functions. Use imperative mood: "Create a sandbox" not "Creates a sandbox" (PEP 257).
- **Docstring format:** Google style. Use `Args:`, `Returns:`, `Raises:` sections for functions with non-obvious parameters or behavior.
- **No docstrings** on private helpers, test functions, or obvious one-liners
- **Comments:** Reserve for non-obvious "why" — not "what". Keep current or delete.
- **No commented-out code.** Use version control.

```python
def create_sandbox(name: str, agent: str, directories: list[Path]) -> Sandbox:
    """Create a new sandbox with the given agent preset.

    Args:
        name: Unique sandbox identifier.
        agent: Agent preset name (e.g., "claude", "codex").
        directories: Host directories to mount in the sandbox.

    Returns:
        The created sandbox, not yet started.

    Raises:
        ConfigError: If the agent preset is unknown.
        SandboxError: If a sandbox with this name already exists.
    """
```

## Logging

- Use the `logging` module, not `print()` for diagnostic output
- Logger per module: `logger = logging.getLogger(__name__)`
- CLI layer configures the root logger based on `--verbose` / `--quiet`
- Log levels: `DEBUG` for tracing, `INFO` for normal operations, `WARNING` for recoverable issues, `ERROR` for failures

## Configuration and Constants

- No magic strings — use constants or enums
- Config parsing isolated in `config/` module; rest of code receives typed dataclasses
- Default values defined in one place, not scattered
- Environment variable names prefixed with `YOLOAI_` for yoloai-specific vars

## Dependencies

- Minimize external dependencies. Justify each one.
- Pin in `pyproject.toml` with compatible ranges (`>=1.0,<2` or `~=1.0`)
- Core deps (always needed): Click (CLI), Docker SDK, PyYAML
- Dev deps: pytest, ruff, mypy, pre-commit
- No vendoring unless required for distribution

## CLI Implementation

- Use **Click** for argument parsing (declarative, composable, well-documented)
- One file per command in `cli/` — keeps each command self-contained
- Commands are thin — parse args, call into `core/`, format output
- No business logic in CLI layer

## Docker Interactions

- Use the **Docker SDK for Python** (`docker` package), not subprocess calls to `docker` CLI
- Wrap Docker SDK calls in a thin abstraction layer for testability
- Handle Docker daemon not running with a clear error message
- All container names prefixed with `yoloai-`

## What to Avoid

- **Premature abstraction** — don't create a base class or interface until you have two implementations. One concrete type is simpler than one interface + one implementation.
- **Over-engineering** — three similar lines of code are better than a premature helper function. Build what's needed now.
- **`print()` for diagnostics** — use `logging`. `print()` is for program output only.
- **Bare `except:`** — always catch specific exception types
- **Mutable default arguments** — use `None` and assign inside the function
- **Wildcard imports** (`from module import *`) — makes the namespace unpredictable
- **Global mutable state** — pass dependencies through constructors or function parameters
