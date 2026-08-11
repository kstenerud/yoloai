#!/usr/bin/env python3
# ABOUTME: Gate that checks the other gates actually look at things: test files
# ABOUTME: pytest would never collect, and check_*.py scripts with no tests at all.

"""Assert that a gate's input set contains what you think it contains.

A green gate means two things at once — "this code is fine" and "this code was
examined" — and only the first is ever stated. When the second is false the green is
free, and it looks identical to a real pass.

That is not hypothetical here. `scripts/research_harness_v1.py` passed a complete
`make check` while untracked, because `PY_SCRIPTS` was a plain `git ls-files` glob
and mypy therefore never opened it. pytest *did* run, which made the green look
complete. The root cause is fixed in the Makefile — the Python globs now use
`--cached --others --exclude-standard`, matching what the shellcheck target already
did — so this module covers the omissions that remain.

Two checks, both deliberately narrow:

1. **A Python test file pytest would never collect.** `python-test` runs three
   explicit directories. A test file outside them is not a test that fails; it is a
   test that never runs, which is worse, because the suite stays green.
2. **A `check_*.py` gate with no tests.** A gate that has only ever been observed
   passing is indistinguishable from one that cannot fail. Whether its tests prove
   it *fires* is a review question; whether it has any is not.

The pytest roots are parsed out of the Makefile rather than restated here, so this
check cannot drift from the thing it is checking.
"""

from __future__ import annotations

import argparse
import ast
import re
import subprocess
import sys
from pathlib import Path
from typing import Callable

PYTEST_INVOCATION_RE = re.compile(r"^\s*\$\(PYTEST\)\s+(\S+)", re.MULTILINE)
TEST_FILE_RE = re.compile(r"(^|/)(test_[^/]+\.py|[^/]+_test\.py)$")
GATE_RE = re.compile(r"^scripts/(check_[a-z0-9_]+)\.py$")

# pytest passes fixtures by parameter name; anything else means the function takes
# real arguments and pytest could not call it. `scripts/smoke_test.py` has five
# `def test_*(t: Test, spec: BackendSpec)` harness steps that are invoked directly —
# a name collision, not five dead tests, and flagging them would be the noise that
# gets a gate switched off.
KNOWN_FIXTURES = frozenset(
    {"tmp_path", "tmp_path_factory", "monkeypatch", "capsys", "capfd", "caplog", "request", "tmpdir"}
)


def _tracked_and_new(root: Path, pattern: str) -> list[str]:
    """Files git knows about plus untracked ones, honouring .gitignore.

    The same form the Makefile uses. Plain `git ls-files` would reproduce the exact
    blind spot this module exists to close.
    """
    out = subprocess.run(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", pattern],
        cwd=root,
        capture_output=True,
        text=True,
        check=False,
    ).stdout
    return sorted({line for line in out.splitlines() if line.strip()})


def pytest_roots(makefile: str) -> list[str]:
    """The directories `python-test` actually points pytest at."""
    return [m.rstrip("/") for m in PYTEST_INVOCATION_RE.findall(makefile)]


def has_collectible_tests(source: str) -> bool:
    """True when the module defines a test function pytest could actually call."""
    try:
        tree = ast.parse(source)
    except SyntaxError:
        return False
    for node in ast.walk(tree):
        if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        if not node.name.startswith("test_"):
            continue
        names = [a.arg for a in node.args.args]
        if all(n in KNOWN_FIXTURES for n in names):
            return True
    return False


def uncollected_tests(
    py_files: list[str], roots: list[str], source_of: Callable[[str], str]
) -> list[str]:
    """Files named like tests, holding callable tests, that pytest never reaches.

    `source_of` is required rather than optional on purpose. An earlier cut left the
    content filter to the caller, and the very next function written — this module's
    own self-test — forgot it and re-flagged `smoke_test.py`. A name alone is not
    evidence that a file holds tests (see KNOWN_FIXTURES), so the check that proves
    it cannot be somebody else's job to remember.
    """
    out = []
    for f in py_files:
        if not TEST_FILE_RE.search(f):
            continue
        if any(f.startswith(r + "/") for r in roots):
            continue
        if has_collectible_tests(source_of(f)):
            out.append(f)
    return out


def untested_gates(py_files: list[str]) -> list[str]:
    gates = {m.group(1) for f in py_files if (m := GATE_RE.match(f))}
    # Both conventions are in use: test_check_breaking_changes.py alongside
    # test_citation_provenance.py. Accept either rather than churn existing names.
    tested: set[str] = set()
    for f in py_files:
        m = re.match(r"^scripts/tests/test_([a-z0-9_]+)\.py$", f)
        if not m:
            continue
        stem = m.group(1)
        tested.add(stem if stem.startswith("check_") else f"check_{stem}")
    return sorted(f"scripts/{g}.py" for g in gates - tested)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--root", default=".", help="repo root (tests override this)")
    args = ap.parse_args()
    root = Path(args.root)

    makefile = (root / "Makefile").read_text()
    roots = pytest_roots(makefile)
    if not roots:
        print("Could not find any $(PYTEST) invocation in the Makefile.")
        print("This check derives its expectations from there; it cannot run blind.")
        return 1

    py = _tracked_and_new(root, "*.py")
    problems = 0

    stray = uncollected_tests(py, roots, lambda f: (root / f).read_text(errors="replace"))
    if stray:
        problems += 1
        print("Test files pytest is never pointed at:\n")
        for f in stray:
            print(f"  {f}")
        print(
            f"\n`python-test` collects only: {', '.join(roots)}\n"
            "A test outside those directories does not fail — it never runs, and the\n"
            "suite stays green. Move it under a collected directory, or point pytest\n"
            "at its directory in the Makefile.\n"
        )

    bare = untested_gates(py)
    if bare:
        problems += 1
        print("Gates with no tests:\n")
        for f in bare:
            print(f"  {f}")
        print(
            "\nA gate that has only ever been observed passing is indistinguishable\n"
            "from one that cannot fail. Add `scripts/tests/test_<name>.py`, and make\n"
            "at least one case feed it known-bad input and assert it fires.\n"
        )

    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
