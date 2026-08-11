#!/usr/bin/env python3
# ABOUTME: Tests for the research-bounds gate — that it fires on a new results file
# ABOUTME: with no bounding section, stays quiet on superseded runs and on files
# ABOUTME: outside a research results directory, and accepts both passes' spellings.

from __future__ import annotations

import sys
from pathlib import Path

_REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(_REPO / "scripts"))

from check_research_bounds import (  # noqa: E402
    needs_bounds,
    offenders,
    rev_exists,
    states_bounds,
)

LINUX = "docs/contributors/design/research/linux-enforcement/results/l1-cgroup-key.txt"
MACOS = "docs/contributors/design/research/macos-isolation-spike/results/pf-spoof.txt"


def test_a_new_results_file_needs_bounds() -> None:
    assert needs_bounds(LINUX) is True
    assert needs_bounds(MACOS) is True


def test_a_superseded_run_does_not() -> None:
    """An invalidated run supports nothing, so it has nothing to bound."""
    base = "docs/contributors/design/research/linux-enforcement/results/"
    assert needs_bounds(base + "p1-no-fastpath-run1-invalid.txt") is False
    assert needs_bounds(base + "pf-spoof-run2.txt") is False
    assert needs_bounds(base + "dns-intercept-run3.txt") is False


def test_files_outside_a_research_results_dir_are_not_our_business() -> None:
    assert needs_bounds("docs/contributors/design/plans/some-plan.md") is False
    assert needs_bounds("scripts/check_research_bounds.py") is False
    assert needs_bounds("docs/contributors/design/research/linux-enforcement/l1.sh") is False
    # A harness one level up from results/ is not a result.
    assert needs_bounds("docs/contributors/design/research/x/results/notes.md") is False


def test_both_passes_spellings_are_accepted() -> None:
    """The two passes decorated the heading differently; neither should be penalised."""
    assert states_bounds("=== WHAT WAS NOT TRIED ===\n  - IPv6\n") is True
    assert states_bounds("== WHAT WAS NOT TRIED ==\n") is True
    assert states_bounds("--- what was not tried ---\n") is True
    assert states_bounds("NOT TRIED:\n  - concurrent churn\n") is True


def test_a_file_with_no_bounding_section_is_flagged() -> None:
    assert states_bounds("guest=172.17.0.2\nVERDICT: blocked\n") is False


def test_offenders_flags_only_the_unbounded_new_result(tmp_path: Path) -> None:
    good = tmp_path / LINUX
    good.parent.mkdir(parents=True)
    good.write_text("counters: 3\n\n=== WHAT WAS NOT TRIED ===\n  - IPv6\n")
    bad = good.parent / "l2-split-horizon.txt"
    bad.write_text("guest=172.17.0.2\nRESULT: reproduced\n")
    plan = tmp_path / "docs/contributors/design/plans/p.md"
    plan.parent.mkdir(parents=True)
    plan.write_text("no bounds here and that is fine\n")

    found = offenders(
        [LINUX, str(bad.relative_to(tmp_path)), "docs/contributors/design/plans/p.md"],
        tmp_path,
    )
    assert found == [str(bad.relative_to(tmp_path))]


def test_a_path_that_does_not_exist_is_skipped(tmp_path: Path) -> None:
    """A file added then removed in the same branch must not crash the gate."""
    assert offenders([LINUX], tmp_path) == []


def test_an_unresolvable_ref_is_recognised_as_such() -> None:
    assert rev_exists("HEAD") is True
    assert rev_exists("origin/") is False
    assert rev_exists("") is False


def test_the_gate_refuses_to_run_rather_than_passing_on_a_bad_ref() -> None:
    """The exact invocation CI used on every push to main, for a year.

    `origin/${{ github.base_ref }}` interpolates to `origin/` on a push event. git
    reports the unresolvable ref on stderr and writes nothing to stdout, so the gate
    saw an empty file list and exited 0 — a green that had examined nothing. Passing
    here would be indistinguishable from a branch that added no results files, which
    is why the check is on the exit code and not on the message.
    """
    import subprocess

    done = subprocess.run(
        [sys.executable, str(_REPO / "scripts/check_research_bounds.py"), "--base", "origin/"],
        cwd=_REPO,
        capture_output=True,
        text=True,
        check=False,
    )
    assert done.returncode == 1, "the gate passed on a ref it could not resolve"
    assert "resolve" in (done.stdout + done.stderr)


def test_the_real_corpus_newest_results_carry_bounds() -> None:
    """The runs written after the discipline landed should satisfy their own gate."""
    root = _REPO / "docs/contributors/design/research/linux-enforcement/results"
    for name in ("k1-interface-as-sole-key.txt", "p1b-revocation-decay.txt"):
        f = root / name
        if f.exists():
            assert states_bounds(f.read_text()), f"{name} lost its bounding section"
