# ABOUTME: Tests for the next-free-rationale-ID script. Proves it sees every sink,
# ABOUTME: refuses an inconsistent corpus, and does not mistake a DF continuation
# ABOUTME: heading for a duplicate definition.
"""Tests for scripts/next-id.sh.

The point of the script is that it cannot be partial, so the tests that matter
are the ones a hand-written grep would fail:

- the D corpus is two files, and the archive is the one that gets forgotten
  (a real duplicate D118 was minted exactly that way);
- the DF corpus is four sinks;
- a duplicate must stop it rather than yield a number derived from a corpus that
  is already broken;
- but a DF *continuation* heading is not a duplicate, and reading it as one would
  wedge the script permanently, since DF8 and DF18 have continuations today.

Each fixture is a real git repo because the script locates its corpus via
`git rev-parse --show-toplevel`.
"""

from __future__ import annotations

import subprocess
from pathlib import Path

import pytest

_SCRIPT = Path(__file__).resolve().parent.parent / "next-id.sh"

_DECISIONS = "docs/contributors/decisions"
_FINDINGS = "docs/contributors/design"


def _repo(tmp_path: Path, files: dict[str, str]) -> Path:
    """Build a git repo holding `files` (repo-relative path -> contents)."""
    subprocess.run(["git", "init", "-q"], cwd=tmp_path, check=True)
    for rel, contents in files.items():
        p = tmp_path / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(contents, encoding="utf-8")
    return tmp_path


def _run(repo: Path, kind: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [str(_SCRIPT), kind],
        cwd=repo,
        capture_output=True,
        text=True,
        check=False,
    )


# --- the corpus is complete by construction ----------------------------------

def test_d_scans_the_archive_and_not_just_the_live_log(tmp_path: Path) -> None:
    """The defect this script exists to kill: the highest D lives in the archive.

    Grepping only working-notes.md answers 118 here, which is already taken.
    """
    repo = _repo(tmp_path, {
        f"{_DECISIONS}/working-notes.md": "## D117 — a live decision\n",
        f"{_DECISIONS}/working-notes-archive.md": "## D118 — an archived decision\n",
    })
    r = _run(repo, "D")
    assert r.returncode == 0, r.stderr
    assert r.stdout.strip() == "119"


def test_df_scans_all_four_sinks(tmp_path: Path) -> None:
    repo = _repo(tmp_path, {
        f"{_FINDINGS}/findings-unresolved.md": "### DF10 — unresolved\n",
        f"{_FINDINGS}/findings-resolved.md": "### DF11 — resolved\n",
        f"{_FINDINGS}/findings-deferred.md": "### DF12 — deferred\n",
        f"{_FINDINGS}/findings-abandoned.md": "### DF13 — abandoned\n",
    })
    r = _run(repo, "DF")
    assert r.returncode == 0, r.stderr
    assert r.stdout.strip() == "14"


def test_prints_a_bare_integer_so_it_composes(tmp_path: Path) -> None:
    repo = _repo(tmp_path, {f"{_DECISIONS}/working-notes.md": "## D4 — a decision\n"})
    r = _run(repo, "D")
    assert r.stdout == "5\n"


# --- an inconsistent corpus stops it -----------------------------------------

def test_a_duplicate_definition_is_an_error_not_a_number(tmp_path: Path) -> None:
    repo = _repo(tmp_path, {
        f"{_DECISIONS}/working-notes.md": "## D7 — a live decision\n",
        f"{_DECISIONS}/working-notes-archive.md": "## D7 — the same number, archived\n",
    })
    r = _run(repo, "D")
    assert r.returncode == 1
    assert "7" in r.stderr
    assert r.stdout == ""


def test_a_missing_corpus_is_an_error(tmp_path: Path) -> None:
    repo = _repo(tmp_path, {"README.md": "no decisions here\n"})
    r = _run(repo, "D")
    assert r.returncode == 1
    assert "corpus" in r.stderr


# --- but a continuation heading is not a duplicate ---------------------------

def test_df_continuation_headings_are_not_duplicates(tmp_path: Path) -> None:
    """DF8 and DF18 carry continuations in the real tree.

    Reading these as duplicates would make the script refuse to allocate a DF ID
    ever again, which is how a gate dies on arrival.
    """
    repo = _repo(tmp_path, {
        f"{_FINDINGS}/findings-unresolved.md": "### DF18 — live-daemon error paths\n",
        f"{_FINDINGS}/findings-resolved.md": (
            "### DF8 — the original finding\n"
            "### DF8 (4th data point, 2026-05-26): failed once, passed on retry\n"
            "### DF8 FIX V3 LANDED 2026-05-26\n"
            "### DF18 (run-coverage half) — Seatbelt and Tart now have run coverage\n"
        ),
    })
    r = _run(repo, "DF")
    assert r.returncode == 0, r.stderr
    assert r.stdout.strip() == "19"


# --- usage -------------------------------------------------------------------

@pytest.mark.parametrize("argv", [[], ["X"], ["D", "DF"]])
def test_usage_error_on_bad_arguments(tmp_path: Path, argv: list[str]) -> None:
    repo = _repo(tmp_path, {f"{_DECISIONS}/working-notes.md": "## D1 — a decision\n"})
    r = subprocess.run(
        [str(_SCRIPT), *argv], cwd=repo, capture_output=True, text=True, check=False
    )
    assert r.returncode == 2
    assert "usage" in r.stderr
