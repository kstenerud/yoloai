#!/usr/bin/env python3
# ABOUTME: Tests for the gate-coverage check — that it finds a test pytest would
# ABOUTME: never collect, finds a gate with no tests, derives its expectations from
# ABOUTME: the Makefile rather than restating them, and stays quiet on a clean tree.

from __future__ import annotations

import sys
from pathlib import Path

_REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(_REPO / "scripts"))

from check_gate_coverage import (  # noqa: E402
    has_collectible_tests,
    pytest_roots,
    uncollected_tests,
    untested_gates,
)

MAKEFILE = """
python-test: python-typecheck
\t$(PYTEST) runtime/monitor/tests/ -v
\t$(PYTEST) scripts/tests/ -v
"""


def test_roots_come_from_the_makefile_not_from_here() -> None:
    """Restating the list would let this check drift from the thing it checks."""
    assert pytest_roots(MAKEFILE) == ["runtime/monitor/tests", "scripts/tests"]


def test_no_roots_is_not_silently_treated_as_nothing_to_do() -> None:
    assert pytest_roots("all: build\n\techo hi\n") == []


COLLECTIBLE = "def test_x() -> None:\n    assert True\n"


def test_a_test_outside_a_collected_directory_is_flagged() -> None:
    """It does not fail — it never runs, and the suite stays green."""
    roots = ["scripts/tests"]
    files = [
        "scripts/tests/test_ok.py",
        "runtime/docker/resources/test_firewall.py",
        "scripts/tests/helpers_test.py",
    ]
    found = uncollected_tests(files, roots, lambda _f: COLLECTIBLE)
    assert found == ["runtime/docker/resources/test_firewall.py"]


def test_both_pytest_naming_conventions_are_recognised() -> None:
    src = lambda _f: COLLECTIBLE  # noqa: E731
    assert uncollected_tests(["a/test_thing.py"], [], src) == ["a/test_thing.py"]
    assert uncollected_tests(["a/thing_test.py"], [], src) == ["a/thing_test.py"]


def test_a_test_named_file_holding_no_callable_tests_is_not_flagged() -> None:
    """smoke_test.py's shape: the name matches, the functions take real arguments."""
    src = "def test_dind(t: Test, spec: BackendSpec) -> None:\n    pass\n"
    assert uncollected_tests(["scripts/smoke_test.py"], [], lambda _f: src) == []


def test_non_test_files_are_not_our_business() -> None:
    src = lambda _f: COLLECTIBLE  # noqa: E731
    assert uncollected_tests(["scripts/research_harness_v1.py", "runtime/x.py"], [], src) == []


def test_a_zero_arg_test_function_is_collectible() -> None:
    assert has_collectible_tests("def test_thing() -> None:\n    assert True\n") is True


def test_a_fixture_taking_test_is_collectible() -> None:
    assert has_collectible_tests("def test_thing(tmp_path) -> None:\n    pass\n") is True


def test_a_function_taking_real_arguments_is_not_a_pytest_test() -> None:
    """scripts/smoke_test.py's `def test_dind(t, spec)` steps are called directly.

    Flagging those as five dead tests would be the noise that gets a gate disabled.
    """
    src = "def test_dind(t: Test, spec: BackendSpec) -> None:\n    pass\n"
    assert has_collectible_tests(src) is False


def test_a_module_with_no_test_functions_is_not_a_test_file() -> None:
    assert has_collectible_tests("def helper() -> None:\n    pass\n") is False


def test_unparseable_source_is_not_treated_as_collectible() -> None:
    assert has_collectible_tests("def (((") is False


def test_a_gate_without_tests_is_flagged() -> None:
    files = [
        "scripts/check_breaking_changes.py",
        "scripts/tests/test_check_breaking_changes.py",
        "scripts/check_lonely.py",
    ]
    assert untested_gates(files) == ["scripts/check_lonely.py"]


def test_both_gate_test_naming_conventions_count() -> None:
    """The repo uses test_check_<n>.py and test_<n>.py; neither should be penalised."""
    assert untested_gates(
        ["scripts/check_citation_provenance.py", "scripts/tests/test_citation_provenance.py"]
    ) == []
    assert untested_gates(
        ["scripts/check_breaking_changes.py", "scripts/tests/test_check_breaking_changes.py"]
    ) == []


def test_a_fully_tested_gate_set_is_quiet() -> None:
    files = [
        "scripts/check_a.py",
        "scripts/tests/test_check_a.py",
        "scripts/check_b.py",
        "scripts/tests/test_check_b.py",
    ]
    assert untested_gates(files) == []


def test_non_gate_scripts_are_not_required_to_have_tests() -> None:
    assert untested_gates(["scripts/govulncheck.py", "scripts/smoke_test.py"]) == []


def test_the_real_repo_satisfies_its_own_check() -> None:
    """The check must hold on the tree that ships it, or it is decoration."""
    import subprocess

    py = sorted(
        {
            line
            for line in subprocess.run(
                ["git", "ls-files", "--cached", "--others", "--exclude-standard", "*.py"],
                cwd=_REPO,
                capture_output=True,
                text=True,
                check=False,
            ).stdout.splitlines()
            if line.strip()
        }
    )
    roots = pytest_roots((_REPO / "Makefile").read_text())
    assert roots, "the Makefile stopped naming pytest directories"
    assert uncollected_tests(py, roots, lambda f: (_REPO / f).read_text(errors="replace")) == []
    assert untested_gates(py) == []
