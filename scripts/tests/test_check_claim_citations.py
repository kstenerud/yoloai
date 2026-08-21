# ABOUTME: Tests for the claim-citations gate — a cited test must exist, a
# ABOUTME: TestArch_ test must be cited, backtick-only matching, and that the
# ABOUTME: gate holds against the tree that ships it.

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

_REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(_REPO / "scripts"))

from check_claim_citations import (  # noqa: E402
    GATED_DOCS,
    citations_in,
    defined_tests_in,
    gated_doc_files,
    missing_tests,
    orphaned_tests,
)


# --- citations_in --------------------------------------------------------------


def test_a_backticked_name_is_a_citation() -> None:
    text = "**Profiles are self-contained** (`TestArch_ProfileIgnoresPersonalDefaults`) — etc."
    assert citations_in(text) == [("TestArch_ProfileIgnoresPersonalDefaults", 1)]


def test_a_bare_name_without_backticks_is_not_a_citation() -> None:
    """Otherwise this module's own docstring, which names the convention in prose,
    would cite itself."""
    assert citations_in("The convention uses the TestArch_ prefix.") == []


def test_line_numbers_are_1_based_and_per_line() -> None:
    text = "line one\n`TestArch_Foo`\nline three\n`TestArch_Bar`\n"
    assert citations_in(text) == [("TestArch_Foo", 2), ("TestArch_Bar", 4)]


def test_two_citations_on_one_line_are_both_found() -> None:
    text = "See `TestArch_A` and also `TestArch_B`."
    assert citations_in(text) == [("TestArch_A", 1), ("TestArch_B", 1)]


# --- defined_tests_in ------------------------------------------------------------


def test_a_testarch_func_is_found() -> None:
    src = "package p\n\nfunc TestArch_Foo(t *testing.T) {\n}\n"
    assert defined_tests_in({"p_test.go": src}) == {"TestArch_Foo"}


def test_a_non_testarch_func_is_not_collected() -> None:
    src = "func TestSomethingElse(t *testing.T) {}\nfunc helper() {}\n"
    assert defined_tests_in({"p_test.go": src}) == set()


def test_tests_are_pooled_across_files() -> None:
    sources = {
        "a_test.go": "func TestArch_A(t *testing.T) {}\n",
        "b_test.go": "func TestArch_B(t *testing.T) {}\n",
    }
    assert defined_tests_in(sources) == {"TestArch_A", "TestArch_B"}


# --- missing_tests / orphaned_tests (pure, no filesystem) ----------------------


def test_a_citation_naming_a_real_test_is_not_missing() -> None:
    docs = {"d.md": "claim (`TestArch_Real`)"}
    assert missing_tests(docs, {"TestArch_Real"}) == []


def test_a_citation_naming_no_test_is_reported_with_its_location() -> None:
    docs = {"d.md": "line one\nclaim (`TestArch_Ghost`)\n"}
    assert missing_tests(docs, set()) == [("d.md", 2, "TestArch_Ghost")]


def test_a_cited_test_is_not_orphaned() -> None:
    docs = {"d.md": "(`TestArch_Real`)"}
    assert orphaned_tests(docs, {"TestArch_Real"}) == []


def test_an_uncited_test_is_orphaned() -> None:
    assert orphaned_tests({"d.md": "no claims here"}, {"TestArch_Lonely"}) == ["TestArch_Lonely"]


def test_a_test_cited_by_one_of_several_docs_is_not_orphaned() -> None:
    docs = {"a.md": "nothing", "b.md": "(`TestArch_Real`)"}
    assert orphaned_tests(docs, {"TestArch_Real"}) == []


# --- gated_doc_files: the scope is narrow and does not silently drift ----------


def test_gated_docs_are_exactly_the_documented_narrow_set() -> None:
    assert GATED_DOCS == (
        "docs/contributors/architecture/*.md",
        "docs/contributors/design/config.md",
        "docs/contributors/principles/*.md",
        "docs/contributors/decisions/working-notes.md",
    )


def test_findings_and_plans_are_not_in_scope() -> None:
    """The gate must never fire on a findings/plans doc — that noise is what gets
    a gate disabled (D122's reasoning, restated for this one)."""
    files = gated_doc_files(_REPO)
    assert not any("findings" in f for f in files)
    assert not any("/plans/" in f for f in files)


def test_the_gated_set_is_non_empty_on_the_real_tree() -> None:
    """An empty result means a pattern stopped resolving (a directory moved) —
    silence would look identical to "nothing to check"."""
    assert len(gated_doc_files(_REPO)) >= 10


# --- the gate holds on the tree that ships it -----------------------------------


def test_the_real_repo_satisfies_its_own_check() -> None:
    result = subprocess.run(
        [sys.executable, str(_REPO / "scripts/check_claim_citations.py"), "--root", str(_REPO)],
        capture_output=True,
        text=True,
        check=False,
    )
    assert result.returncode == 0, result.stdout + result.stderr
