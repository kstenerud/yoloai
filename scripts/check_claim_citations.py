#!/usr/bin/env python3
# ABOUTME: CI gate. A `TestArch_` claim citation in a gated doc must name a real
# ABOUTME: test, and every `TestArch_` test must be cited by some gated doc.

"""Tie a load-bearing architectural claim to the test that makes it checkable.

`docs/` asserts things about the code and nothing checks any of them. The specimen
that motivated this: `docs/contributors/design/config.md:167` stated, in bold, "no
exceptions" — and that was false for the entire config for two commits' worth of
history (DF207/DF208). Both the owner and an agent read the sentence, believed it,
and built on it. The documentation manufactured a shared false model, and nothing
short of independently re-deriving the claim from the code would have caught it.

The fix is a convention, not a smarter reader: a load-bearing claim's authoritative
form is a `TestArch_`-prefixed Go test (or a `test_arch_`-prefixed Python one, for a
claim about one of the gates), and the prose cites it by name in backticks —

    **Profiles are self-contained** (`TestArch_ProfileIgnoresPersonalDefaults`) —
    personal defaults do not carry into a profile.

— so "is this still true?" is answered by `make check`, not by trust. This module
is the two cheap, text-based halves that keep the citation and the test in sync:

1. **Every cited test exists.** A citation naming a `TestArch_X` that no
   `*_test.go` defines is a claim resting on nothing — either the test was never
   written, or it was renamed and the prose was not.
2. **Every claim test is cited.** A `TestArch_X` with no citing doc is an orphan:
   its prose was deleted (or never written), so the corpus silently lost a claim
   without anyone deciding to drop it.

Deliberately narrow, the same way `check_citation_provenance.py` is (D122): the
`TestArch_` prefix exists for no other purpose, so grepping for it cannot fire on
ordinary prose, and GATED_DOCS covers only the documents whose claims are meant to
be load-bearing in this sense — not findings, not plans, not the user guide. A gate
that fires on prose it was never meant to police is the kind that gets disabled.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

# The narrow, load-bearing surface. Nothing else — see the module docstring for
# why findings/plans/GUIDE.md are deliberately excluded. Keep this list in sync
# with AGENTS.md's "Preparing a PR" rule for this gate.
GATED_DOCS = (
    "docs/contributors/architecture/*.md",
    "docs/contributors/design/config.md",
    "docs/contributors/principles/*.md",
    "docs/contributors/decisions/working-notes.md",
)

# A citation is the bare test name in backticks: `TestArch_Foo`, or `test_arch_foo`
# for a claim a Python test pins. Backticks only — the name written in running
# prose without them is not a citation, it is the convention paragraph explaining
# the convention (this module's own docstring, for instance), and matching that
# would make the gate fire on itself.
CITATION_RE = re.compile(r"`(TestArch_[A-Za-z0-9_]+|test_arch_[a-z0-9_]+)`")

# A claim test's definition. Two languages, one convention: Go claims are
# `func TestArch_X`, Python claims `def test_arch_x`. The Python half exists
# because a dozen gates in scripts/ are Python, they are the most-retuned code
# in the tree, and until this was added no claim about any of them could be
# cited at all — a decision could assert what a gate does with nothing keeping
# it true (D146). Both patterns are applied to every test file: `^func` cannot
# appear in a .py file and `^def` cannot appear in a .go one, so no dispatch on
# extension is needed and defined_tests_in stays pure.
TEST_FUNC_RE = re.compile(r"^func (TestArch_[A-Za-z0-9_]+)\(", re.MULTILINE)
PY_TEST_FUNC_RE = re.compile(r"^def (test_arch_[a-z0-9_]+)\(", re.MULTILINE)


def _definition_hint(name: str) -> str:
    """How the gate tells a reader to look for `name`'s definition."""
    if name.startswith("test_arch_"):
        return f"no `def {name}(` under any tests/ directory"
    return f"no `func {name}(` in any *_test.go"


def _tracked_and_new(root: Path, pattern: str) -> list[str]:
    """Files git knows about plus untracked ones, honouring .gitignore.

    Same form check_gate_coverage.py uses, for the same reason: a plain
    `git ls-files` glob misses a file that has not been `git add`ed yet, which is
    exactly the state a new claim test or a new doc edit is in while being written.
    """
    out = subprocess.run(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", pattern],
        cwd=root,
        capture_output=True,
        text=True,
        check=False,
    ).stdout
    return sorted({line for line in out.splitlines() if line.strip()})


def gated_doc_files(root: Path) -> list[str]:
    """Every file GATED_DOCS resolves to, tracked or new."""
    files: set[str] = set()
    for pattern in GATED_DOCS:
        files.update(_tracked_and_new(root, pattern))
    return sorted(files)


def citations_in(text: str) -> list[tuple[str, int]]:
    """(test name, 1-based line number) for every citation in text."""
    out = []
    for lineno, line in enumerate(text.splitlines(), start=1):
        for m in CITATION_RE.finditer(line):
            out.append((m.group(1), lineno))
    return out


def defined_tests_in(sources: dict[str, str]) -> set[str]:
    """Every claim-test name defined across a map of path -> file content."""
    names: set[str] = set()
    for text in sources.values():
        names.update(TEST_FUNC_RE.findall(text))
        names.update(PY_TEST_FUNC_RE.findall(text))
    return names


def claim_test_files(root: Path) -> list[str]:
    """Every file a claim test can be defined in: Go test files, and Python files
    under a tests/ directory.

    A Python test is one by the directory it sits in, not by its name — the same
    rule check_revert_red.py uses, and for the same reason: scripts/smoke_test.py
    is a 4000-line harness rather than a test, and every real Python test in this
    repo lives under a tests/ root.
    """
    go = _tracked_and_new(root, "*_test.go")
    py = [f for f in _tracked_and_new(root, "*.py") if "/tests/" in f or f.startswith("tests/")]
    return sorted(set(go) | set(py))


def defined_tests(root: Path) -> set[str]:
    """Every claim test defined anywhere in the repo."""
    sources = {
        f: (root / f).read_text(encoding="utf-8", errors="replace")
        for f in claim_test_files(root)
    }
    return defined_tests_in(sources)


def missing_tests(doc_texts: dict[str, str], tests: set[str]) -> list[tuple[str, int, str]]:
    """(doc, line, name) for every citation whose test does not exist.

    Pure: doc_texts maps a doc path to its already-read content, and tests is the
    set of defined TestArch_ names — no filesystem or git access, so this is the
    part a unit test drives directly.
    """
    out: list[tuple[str, int, str]] = []
    for doc, text in doc_texts.items():
        for name, line in citations_in(text):
            if name not in tests:
                out.append((doc, line, name))
    return out


def orphaned_tests(doc_texts: dict[str, str], tests: set[str]) -> list[str]:
    """TestArch_ tests that no gated doc cites. Pure, same shape as missing_tests."""
    cited: set[str] = set()
    for text in doc_texts.values():
        cited.update(name for name, _ in citations_in(text))
    return sorted(tests - cited)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--root", default=".", help="repo root (tests override this)")
    args = ap.parse_args()
    root = Path(args.root)

    docs = gated_doc_files(root)
    doc_texts = {doc: (root / doc).read_text(encoding="utf-8", errors="replace") for doc in docs}
    tests = defined_tests(root)
    problems = 0

    missing = missing_tests(doc_texts, tests)
    if missing:
        problems += 1
        print("Claim citations naming a test that does not exist:\n")
        for doc, line, name in missing:
            print(f"  {doc}:{line}: `{name}` — {_definition_hint(name)}")
        print(
            "\nA claim citation is a claim that the test backing it is real.\n"
            "Either the test was renamed or deleted and the doc was not updated, or\n"
            "the citation was written before the test. Add the test, or fix the name.\n"
        )

    orphaned = orphaned_tests(doc_texts, tests)
    if orphaned:
        problems += 1
        print("Claim tests with no citing claim in a gated document:\n")
        for name in orphaned:
            print(f"  {name}")
        print(
            "\nAn uncited claim test is an orphaned claim: its prose was removed\n"
            "(or never written), so the corpus silently lost a claim nobody decided\n"
            "to drop. Cite it from the doc whose claim it backs, in backticks, or\n"
            "rename it off the claim prefix if it never was a load-bearing claim.\n"
        )

    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
