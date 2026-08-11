#!/usr/bin/env python3
# ABOUTME: CI gate. Fails a branch that adds a hardware-research results file
# ABOUTME: without stating what the run did not try.

"""Require a bounding section on every newly-added research results file.

The evidence for this gate is unusually direct. The 2026-08 host-enforcement
research ran two parallel passes against the same design questions. The macOS pass
ended every raw run with a `WHAT WAS NOT TRIED` block and its index carried a
"What these files do NOT support" section. The Linux pass had neither.

The macOS pass produced no over-broad conclusions. The Linux pass produced five,
and two of them reached a decision record: one nftables matcher became "there is no
per-sandbox non-address key on Linux", and three docker cycles became "Linux
interface names do not recycle". Both were false, both were found by an audit rather
than the author, and both had a correct measurement underneath a sentence that
outran it.

That is the failure this gate targets, and it is worth being precise about what it
can and cannot do. It cannot tell that a conclusion is wider than its run — the
sentence and the citation are both well-formed. What it can do is force the author
to enumerate the untested, which is the only lever that empirically correlated with
not making the mistake. It caught one in this very workstream: the veth-recycling
contradiction was found only because a previous run's block named the backend it had
not covered.

Scope is deliberately narrow, for the reason `check_citation_provenance.py` gives
about itself: a noisy gate gets disabled. It fires only on files **added** in the
branch, only under a research `results/` directory, and not on runs whose filename
already marks them as superseded — an invalidated run supports nothing, so it has no
bounds to state.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

RESULTS_RE = re.compile(r"^docs/contributors/design/research/[^/]+/results/.+\.txt$")

# A run kept only to show how it lied. It supports no claim, so it bounds nothing.
SUPERSEDED_RE = re.compile(r"(invalid|superseded|-run\d+)", re.IGNORECASE)

# Both passes wrote the heading slightly differently; accept either, and any
# decoration around it (`== WHAT WAS NOT TRIED ==`, `--- what was not tried ---`).
BOUNDS_RE = re.compile(r"WHAT\s+(?:WAS|IS)\s+NOT\s+TRIED|NOT\s+TRIED", re.IGNORECASE)


def _git(*args: str) -> str:
    return subprocess.run(
        ["git", *args], capture_output=True, text=True, check=False
    ).stdout


def rev_exists(ref: str) -> bool:
    """Whether git can resolve `ref` to a commit.

    Called before diffing because the failure it prevents is silent. `git diff`
    against an unresolvable ref writes to stderr and produces **no stdout**, so a
    gate that reads stdout sees an empty file list and reports a clean pass. CI
    supplied exactly that for a year: the workflow interpolated
    `origin/${{ github.base_ref }}`, which on a `push` event is the empty string, so
    the gate ran as `--base origin/` and passed having examined nothing.
    """
    return (
        subprocess.run(
            ["git", "rev-parse", "--verify", "--quiet", f"{ref}^{{commit}}"],
            capture_output=True,
            check=False,
        ).returncode
        == 0
    )


def added_files(base: str, head: str) -> list[str]:
    """Paths added between base and head. A modified file is not our business."""
    out = _git("diff", "--diff-filter=A", "--name-only", f"{base}...{head}")
    return [line for line in out.splitlines() if line.strip()]


def needs_bounds(path: str) -> bool:
    return bool(RESULTS_RE.match(path)) and not SUPERSEDED_RE.search(Path(path).name)


def states_bounds(text: str) -> bool:
    return bool(BOUNDS_RE.search(text))


def offenders(paths: list[str], root: Path) -> list[str]:
    out = []
    for p in paths:
        if not needs_bounds(p):
            continue
        f = root / p
        if not f.exists():
            continue
        if not states_bounds(f.read_text(errors="replace")):
            out.append(p)
    return out


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--base", required=True, help="ref the branch forked from")
    ap.add_argument("--head", default="HEAD")
    ap.add_argument("--root", default=".", help="repo root (tests override this)")
    args = ap.parse_args()

    for name, ref in (("--base", args.base), ("--head", args.head)):
        if not rev_exists(ref):
            print(f"{name} {ref!r} is not a commit this repository can resolve.")
            print(
                "Refusing to run: diffing against an unresolvable ref produces an empty\n"
                "file list, which is indistinguishable from a clean branch. A gate that\n"
                "cannot see its input must fail, not pass."
            )
            return 1

    bad = offenders(added_files(args.base, args.head), Path(args.root))
    if not bad:
        return 0

    print("Research results files added without stating what the run did not try:\n")
    for p in bad:
        print(f"  {p}")
    print(
        "\nAdd a `WHAT WAS NOT TRIED` section naming what the run does not cover:\n"
        "  - the backends, platforms or protocols it did not exercise\n"
        "  - the arms that were skipped, and why\n"
        "  - anything a reader could otherwise take the result to have settled\n\n"
        "This is not paperwork. The pass that wrote these sections produced no\n"
        "over-broad conclusions; the pass that did not produced five, two of which\n"
        "reached a decision record before an audit caught them.\n\n"
        "If the file is a superseded or invalidated run, say so in its name\n"
        "(`-run2`, `-invalid`) — a run that supports nothing has nothing to bound."
    )
    return 1


if __name__ == "__main__":
    sys.exit(main())
