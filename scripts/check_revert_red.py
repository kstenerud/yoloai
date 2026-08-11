#!/usr/bin/env python3
# ABOUTME: CI gate. For each source file a branch changes, reverts it to the base
# ABOUTME: version and asserts some test goes red — the mechanical form of AGENTS.md
# ABOUTME: rule 10, which otherwise depends on the author remembering to check.

"""Revert each changed file and require something to fail.

AGENTS.md rule 10 says every behavior change carries a test that fails when that
change is reverted, and says the load-bearing word is *every*: count the behavior
changes, count the tests that would go red, make the numbers match. It also says to
actually revert each one and watch it fail, because a test that merely covers the
line looks identical until you try.

That is a mechanical procedure described in prose and left to a human to run, which
is the shape of every rule this repo has had to build a gate for. This runs it.

**What it proves and what it does not.** Red-on-revert shows a test is wired to the
code. It cannot show the code is wired to the *program* — reverting a line inside a
function nothing can reach still turns its test red, and six tests in this repo's
history certified exactly that kind of dead path. Rule 10's guidance about naming the
backends that pass a capability guard is the answer to that one, and it stays human.

**Scope, deliberately narrow, for the reason `check_citation_provenance.py` gives
about itself: a noisy gate gets disabled.**

* Only files whose changes are claimed to be behavioral, read off the conventional
  commit type we already write. `feat`, `fix` and `perf` must go red; `refactor`,
  `docs`, `test`, `ci`, `build`, `chore` need not, because a refactor that changed
  behaviour is a mislabelled commit and that is a different gate's problem. A file
  touched by several commits requires red if *any* of them claims behaviour.
* Only languages whose tests this can actually scope and run. Anything else is
  reported as unchecked rather than passed — silence is what makes a green mean two
  things at once.

**A build failure counts as red, and is reported separately.** It proves the change
is load-bearing; it does not prove a test asserts its behaviour. Conflating the two
would let a compile-time dependency stand in for a missing test.

That distinction is much weaker in Python than in Go, and the difference is worth
knowing before reading a report. A Python test names what it imports, so removing a
newly-added function breaks collection before any assertion runs: **an added Python
function can essentially only ever report `RED_BUILD`**, however well it is tested.
Go reaches `RED_TEST` for the same change whenever the symbol survives the revert.
Measured by running this gate against its own commit.

Everything happens in a throwaway git worktree. This tool checks out old versions of
files over a tree, and doing that in the user's working tree would put uncommitted
work one interrupted run away from being lost.
"""

from __future__ import annotations

import argparse
import re
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path

# A commit whose type claims a behaviour change owes a red-on-revert.
BEHAVIOR_TYPES = frozenset({"feat", "fix", "perf"})
# Everything else in the AGENTS.md rule-4 vocabulary. Listed rather than inferred by
# negation so an unrecognised type is a question, not a silent exemption.
EXEMPT_TYPES = frozenset({"docs", "test", "refactor", "build", "ci", "chore", "style", "revert"})

SUBJECT_RE = re.compile(r"^(?P<type>[a-z]+)(?:\([^)]*\))?(?P<bang>!)?:")

GO_SOURCE = re.compile(r"\.go$")
GO_TEST = re.compile(r"_test\.go$")
PY_SOURCE = re.compile(r"\.py$")
PY_TEST = re.compile(r"(^|/)(test_[^/]+\.py|[^/]+_test\.py)$")

# Directories pytest is pointed at; a Python change outside them has no scope to run.
PY_ROOTS = ("runtime/monitor", "runtime/docker/resources", "scripts")


@dataclass(frozen=True)
class Change:
    path: str
    status: str  # A, M, D


@dataclass(frozen=True)
class Outcome:
    path: str
    verdict: str  # RED_TEST | RED_BUILD | GREEN | CLAIMED | UNCHECKED
    detail: str = ""

    @property
    def is_failure(self) -> bool:
        return self.verdict == "GREEN"


def _git(*args: str, cwd: str | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *args], capture_output=True, text=True, cwd=cwd, check=False
    )


def rev_exists(ref: str) -> bool:
    return _git("rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}").returncode == 0


def commit_type(subject: str) -> str | None:
    """The conventional-commit type, or None when the subject does not carry one."""
    m = SUBJECT_RE.match(subject.strip())
    return m.group("type") if m else None


def claims_behavior_change(types: set[str | None]) -> bool:
    """Whether any commit touching a file claims to change behaviour.

    An unrecognised or missing type counts as claiming one. A subject this cannot
    parse is a commit-lint problem, and defaulting it to exempt would make "write an
    unparseable subject" a way out of the gate.
    """
    return any(t not in EXEMPT_TYPES for t in types)


def changed_sources(base: str, head: str) -> list[Change]:
    out = _git("diff", "--name-status", f"{base}...{head}").stdout
    changes = []
    for line in out.splitlines():
        parts = line.split("\t")
        if len(parts) < 2:
            continue
        status, path = parts[0][:1], parts[-1]
        if GO_TEST.search(path) or PY_TEST.search(path):
            continue  # a test file's own revert is not the question
        if GO_SOURCE.search(path) or PY_SOURCE.search(path):
            changes.append(Change(path=path, status=status))
    return changes


def types_touching(base: str, head: str, path: str) -> set[str | None]:
    out = _git("log", "--no-merges", "--format=%s", f"{base}..{head}", "--", path).stdout
    return {commit_type(line) for line in out.splitlines() if line.strip()}


def verified_out_of_suite(base: str, head: str, path: str) -> str:
    """Any `Verified-By:` claim on the commits touching `path`.

    Some behaviour genuinely cannot go red in `go test`: `ab67389a` is a real one —
    a chown fix whose symptom appeared only under `sudo -E` against rootless podman,
    reproduced by hand and covered by `make smoketest`, which no unit suite runs.

    Without a way to say that, the only route past this gate would be relabelling a
    `fix` as a `chore`, and a gate satisfiable only by lying about the commit type is
    worse than no gate. The trailer turns "I could not unit-test this" from a silent
    bypass into a claim on the record that review can weigh. It deliberately takes
    free text: the point is that a human wrote down what they actually did.
    """
    body = _git("log", "--no-merges", "--format=%B", f"{base}..{head}", "--", path).stdout
    claims = [
        line.split(":", 1)[1].strip()
        for line in body.splitlines()
        if line.lower().startswith("verified-by:")
    ]
    return "; ".join(c for c in claims if c)


def scope_for(path: str) -> list[str] | None:
    """The narrowest test command covering `path`, or None when there is no scope.

    Package-scoped on purpose: the happy path is a fast red. Widening to the full
    suite happens only for a file that survives its own package, which is the
    suspicious case and the only one worth paying for.
    """
    if GO_SOURCE.search(path):
        return ["go", "test", "./" + str(Path(path).parent) + "/..."]
    if PY_SOURCE.search(path) and any(path.startswith(r + "/") for r in PY_ROOTS):
        root = next(r for r in PY_ROOTS if path.startswith(r + "/"))
        return ["python3", "-m", "pytest", "-x", "-q", f"{root}/tests"]
    return None


def wide_scope(path: str) -> list[str] | None:
    if GO_SOURCE.search(path):
        return ["go", "test", "./..."]
    return None


def is_build_failure(output: str) -> bool:
    """Whether a failing run failed to compile rather than to assert.

    Keyed on the toolchains' own markers rather than on error text: `go test` prints
    a `[build failed]` suffix on the FAIL line, and pytest reports an import-time
    problem as a collection error. Matching compiler prose ("undefined:", "cannot
    use") would misread a test that legitimately asserts on such a message.
    """
    markers = ("[build failed]", "ERROR collecting", "ImportError", "SyntaxError")
    return any(m in output for m in markers)


def probe(tree: str, base: str, head: str, change: Change, timeout: int) -> Outcome:
    """Revert one file in the worktree, run its tests, restore it."""
    claim = verified_out_of_suite(base, head, change.path)
    if claim:
        return Outcome(change.path, "CLAIMED", f"Verified-By: {claim}")
    scope = scope_for(change.path)
    if scope is None:
        return Outcome(change.path, "UNCHECKED", "no test scope covers this path")
    if change.status == "D":
        return Outcome(change.path, "UNCHECKED", "deleted; reverting would re-add it")

    try:
        if change.status == "A":
            (Path(tree) / change.path).unlink(missing_ok=True)
        else:
            restored = _git("checkout", base, "--", change.path, cwd=tree)
            if restored.returncode != 0:
                return Outcome(change.path, "UNCHECKED", "could not check out the base version")

        attempts = [(scope, "")]
        wide = wide_scope(change.path)
        if wide is not None and wide != scope:
            attempts.append((wide, ", found by the whole suite"))
        for cmd, label in attempts:
            done = subprocess.run(
                cmd, cwd=tree, capture_output=True, text=True, timeout=timeout, check=False
            )
            if done.returncode == 0:
                continue
            combined = done.stdout + done.stderr
            if is_build_failure(combined):
                return Outcome(change.path, "RED_BUILD", f"reverting it breaks the build{label}")
            return Outcome(change.path, "RED_TEST", f"a test fails when it is reverted{label}")
        return Outcome(change.path, "GREEN", "every test still passes with this change reverted")
    finally:
        _git("checkout", head, "--", change.path, cwd=tree)


def report(outcomes: list[Outcome]) -> str:
    lines = []
    for verdict, heading in (
        ("GREEN", "Changed with nothing to notice — no test goes red when these are reverted"),
        ("RED_BUILD", "Load-bearing, but only at compile time"),
        ("RED_TEST", "Covered — a test fails when these are reverted"),
        ("CLAIMED", "Verified outside the suite, on the author's word — review this"),
        ("UNCHECKED", "Not checked by this gate"),
    ):
        rows = [o for o in outcomes if o.verdict == verdict]
        if not rows:
            continue
        lines.append(f"\n{heading}:")
        lines.extend(f"  {o.path}  — {o.detail}" for o in rows)
    return "\n".join(lines)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--base", required=True, help="ref the branch forked from")
    ap.add_argument("--head", default="HEAD")
    ap.add_argument("--timeout", type=int, default=900, help="seconds per test run")
    args = ap.parse_args()

    for name, ref in (("--base", args.base), ("--head", args.head)):
        if not rev_exists(ref):
            print(f"{name} {ref!r} is not a commit this repository can resolve.")
            return 1

    changes = changed_sources(args.base, args.head)
    owed = [c for c in changes if claims_behavior_change(types_touching(args.base, args.head, c.path))]
    exempt = len(changes) - len(owed)
    if not owed:
        print(f"No behavioral source changes in {args.base}..{args.head} ({exempt} exempt by type).")
        return 0

    tmp = tempfile.mkdtemp(prefix="revert-red-")
    tree = str(Path(tmp) / "wt")
    added = _git("worktree", "add", "--detach", tree, args.head)
    if added.returncode != 0:
        print(f"Could not create a worktree to test in:\n{added.stderr}")
        shutil.rmtree(tmp, ignore_errors=True)
        return 1
    try:
        outcomes = [probe(tree, args.base, args.head, c, args.timeout) for c in owed]
    finally:
        _git("worktree", "remove", "--force", tree)
        shutil.rmtree(tmp, ignore_errors=True)

    print(f"Reverted {len(owed)} behavioral source change(s); {exempt} exempt by commit type.")
    print(report(outcomes))

    failures = [o for o in outcomes if o.is_failure]
    if not failures:
        return 0
    print(
        "\nEach file above changed, and reverting it broke nothing. One of three things\n"
        "is true, and only you can say which:\n\n"
        "  - it needs a test that fails when the change is undone (AGENTS.md rule 10);\n"
        "  - the commit that touched it is mislabelled, and is not `feat`/`fix`/`perf`;\n"
        "  - it genuinely cannot go red in this suite — a smoketest-only symptom, a\n"
        "    multi-principal path the CLI cannot reach. Then say so in the commit with\n"
        "    a `Verified-By:` trailer naming what you actually ran. That is a claim on\n"
        "    the record for review to weigh, not a bypass.\n\n"
        "Red-on-revert is necessary and not sufficient (AGENTS.md rule 10): it proves a\n"
        "test is wired to the code, never that the code is wired to the program."
    )
    return 1


if __name__ == "__main__":
    sys.exit(main())
