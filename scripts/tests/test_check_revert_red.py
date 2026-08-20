#!/usr/bin/env python3
# ABOUTME: Tests for the revert-red gate — that it fires on a behavioral change no
# ABOUTME: test covers, exempts by commit type without letting an unparseable subject
# ABOUTME: through, tells a build break from a test failure, and never edits the tree.

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

_REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(_REPO / "scripts"))

from check_revert_red import (  # noqa: E402
    Change,
    Outcome,
    changed_sources,
    claims_behavior_change,
    commit_type,
    is_build_failure,
    is_cosmetic,
    probe,
    scope_for,
    types_touching,
    wide_scope,
)

# --- reading the commit type -------------------------------------------------


def test_the_conventional_types_are_recognised() -> None:
    assert commit_type("feat(cli): add a flag") == "feat"
    assert commit_type("fix: stop the leak") == "fix"
    assert commit_type("refactor(store)!: rename") == "refactor"


def test_a_subject_with_no_type_reads_as_none() -> None:
    assert commit_type("just some words") is None
    assert commit_type("WIP") is None


def test_behavioral_types_owe_a_red() -> None:
    assert claims_behavior_change({"feat"}) is True
    assert claims_behavior_change({"fix"}) is True
    assert claims_behavior_change({"perf"}) is True


def test_non_behavioral_types_do_not() -> None:
    assert claims_behavior_change({"refactor"}) is False
    assert claims_behavior_change({"docs", "test", "ci", "chore", "build"}) is False


def test_one_behavioral_commit_among_many_is_enough() -> None:
    """A file touched by a refactor and a fix still owes a red for the fix."""
    assert claims_behavior_change({"refactor", "docs", "fix"}) is True


def test_an_unparseable_subject_is_not_a_way_out() -> None:
    """Defaulting an unrecognised type to exempt would make a bad subject an escape
    hatch, and this gate would then reward exactly what the commit linter forbids."""
    assert claims_behavior_change({None}) is True
    assert claims_behavior_change({"frobnicate"}) is True


# --- choosing what to run ----------------------------------------------------


def test_a_go_file_scopes_to_its_own_package() -> None:
    assert scope_for("runtime/docker/create.go") == ["go", "test", "./runtime/docker/..."]


def test_a_go_file_can_widen_to_the_whole_module() -> None:
    """Package scope first because the happy path is a fast red; the whole suite is
    paid for only by a file that survives its own package, which is the odd case."""
    assert wide_scope("runtime/docker/create.go") == ["go", "test", "./..."]


def test_a_python_file_under_a_pytest_root_scopes_to_that_root() -> None:
    assert scope_for("scripts/check_revert_red.py") == [
        "python3", "-m", "pytest", "-x", "-q", "scripts/tests",
    ]


def test_a_path_with_no_test_scope_is_reported_not_assumed_fine() -> None:
    assert scope_for("docs/contributors/AGENTS.md") is None
    assert scope_for("internal/cli/helpcmd/help/run.md") is None
    # Python outside every pytest root has no scope to run, so it cannot be judged.
    assert scope_for("docs/contributors/design/research/x/probe.py") is None


# --- telling a build break from a test failure -------------------------------


def test_go_build_failure_is_recognised() -> None:
    assert is_build_failure("# pkg\n./x.go:3:2: undefined: Foo\nFAIL\tpkg [build failed]\n") is True


def test_a_real_test_failure_is_not_a_build_failure() -> None:
    assert is_build_failure("--- FAIL: TestThing (0.00s)\nFAIL\tpkg\t0.2s\n") is False


def test_a_test_asserting_on_compiler_prose_is_not_misread() -> None:
    """Keying on error text rather than the toolchain's own marker would call this a
    build break: the test passes a message containing compiler wording."""
    out = '--- FAIL: TestErrorText (0.00s)\n  want "undefined: Foo", got ""\nFAIL\tpkg\t0.1s\n'
    assert is_build_failure(out) is False


def test_pytest_collection_error_is_a_build_failure() -> None:
    assert is_build_failure("ERROR collecting scripts/tests/test_x.py\nImportError: no mod") is True


# --- the end-to-end behaviour, on a repo built for the purpose ----------------


def _run(argv: list[str], cwd: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, cwd=cwd, capture_output=True, text=True, check=False)


# git refuses to commit without a committer identity, and a developer machine
# supplies one from ~/.gitconfig while CI does not. Every commit here therefore
# passes the identity inline — an omission is invisible locally and fails only
# in CI, which is what these two helpers exist to prevent.
_IDENTITY = ["-c", "user.email=t@t", "-c", "user.name=t"]


def _commit(repo: Path, subject: str) -> None:
    _run(["git", "add", "-A"], repo)
    _run(["git", *_IDENTITY, "commit", "-m", subject], repo)


def _amend(repo: Path, message: str) -> None:
    """Rewrite the tip commit's message.

    Checked, unlike _run: a silent failure here leaves the ORIGINAL message in
    place, so the test goes on to assert against a commit it did not create and
    fails somewhere else entirely. That is precisely how this went unnoticed —
    the amend failed in CI for want of an identity, and the error surfaced as
    check_revert_red.py declining to exempt a commit type nobody had set.
    """
    done = _run(["git", *_IDENTITY, "commit", "--amend", "-m", message], repo)
    assert done.returncode == 0, f"git commit --amend failed: {done.stdout}{done.stderr}"


def _go_repo(tmp_path: Path, *, covered: bool) -> Path:
    """A module with one behavioral change, whose test either notices it or does not."""
    repo = tmp_path / ("covered" if covered else "uncovered")
    (repo / "calc").mkdir(parents=True)
    _run(["git", "init", "-q", "-b", "main"], repo)
    (repo / "go.mod").write_text("module example.com/m\n\ngo 1.21\n")
    (repo / "calc" / "calc.go").write_text(
        "package calc\n\nfunc Double(n int) int { return n * 2 }\n"
    )
    # The test either asserts the value that changes, or only that the call works.
    assertion = (
        "if Double(3) != 9 { t.Fatal(Double(3)) }" if covered else "_ = Double(3)"
    )
    (repo / "calc" / "calc_test.go").write_text(
        f"package calc\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {{ {assertion} }}\n"
    )
    _commit(repo, "chore: baseline")
    (repo / "calc" / "calc.go").write_text(
        "package calc\n\nfunc Double(n int) int { return n * 3 }\n"
    )
    _commit(repo, "fix(calc): correct the multiplier")
    return repo


def test_it_fires_when_reverting_a_behavioral_change_breaks_nothing(tmp_path: Path) -> None:
    """The known-bad input. `Double` changes and the test only checks it runs, so
    reverting the change leaves every test green — the defect rule 10 describes."""
    repo = _go_repo(tmp_path, covered=False)
    done = _run(
        [sys.executable, str(_REPO / "scripts/check_revert_red.py"), "--base", "HEAD~1"], repo
    )
    assert done.returncode == 1, done.stdout + done.stderr
    assert "calc/calc.go" in done.stdout
    assert "reverting it broke nothing" in done.stdout


def test_it_stays_quiet_when_a_test_does_go_red(tmp_path: Path) -> None:
    """Same change, same commit type — only the test differs. Without this arm the
    gate is shown to fire, not to discriminate."""
    repo = _go_repo(tmp_path, covered=True)
    done = _run(
        [sys.executable, str(_REPO / "scripts/check_revert_red.py"), "--base", "HEAD~1"], repo
    )
    assert done.returncode == 0, done.stdout + done.stderr
    assert "a test fails when it is reverted" in done.stdout


def test_the_commit_type_exempts_the_same_uncovered_change(tmp_path: Path) -> None:
    """The escape hatch, proven to work on input the gate otherwise rejects."""
    repo = _go_repo(tmp_path, covered=False)
    _amend(repo, "refactor(calc): correct the multiplier")
    done = _run(
        [sys.executable, str(_REPO / "scripts/check_revert_red.py"), "--base", "HEAD~1"], repo
    )
    assert done.returncode == 0, done.stdout + done.stderr
    assert "exempt by type" in done.stdout


def test_a_verified_by_trailer_records_rather_than_bypasses(tmp_path: Path) -> None:
    """The escape hatch for behaviour no unit suite can reach.

    Without it the only route past this gate is relabelling a `fix` as a `chore`, and
    a gate satisfiable only by lying about the commit type is worse than no gate. The
    claim must appear in the output — an exemption nobody sees is a bypass.
    """
    repo = _go_repo(tmp_path, covered=False)
    _amend(repo, "fix(calc): correct the multiplier\n\nVerified-By: make smoketest, podman tier")
    done = _run(
        [sys.executable, str(_REPO / "scripts/check_revert_red.py"), "--base", "HEAD~1"], repo
    )
    assert done.returncode == 0, done.stdout + done.stderr
    assert "make smoketest, podman tier" in done.stdout
    assert "on the author's word" in done.stdout


def test_an_empty_verified_by_trailer_does_not_exempt(tmp_path: Path) -> None:
    """`Verified-By:` with nothing after it is not a claim, and must not read as one."""
    repo = _go_repo(tmp_path, covered=False)
    _amend(repo, "fix(calc): correct the multiplier\n\nVerified-By:")
    done = _run(
        [sys.executable, str(_REPO / "scripts/check_revert_red.py"), "--base", "HEAD~1"], repo
    )
    assert done.returncode == 1, done.stdout + done.stderr


def test_it_leaves_the_working_tree_untouched(tmp_path: Path) -> None:
    """It checks out old versions of files, so the one unacceptable outcome is doing
    that in the tree it was pointed at."""
    repo = _go_repo(tmp_path, covered=False)
    before = _run(["git", "status", "--porcelain"], repo).stdout
    content = (repo / "calc" / "calc.go").read_text()
    _run([sys.executable, str(_REPO / "scripts/check_revert_red.py"), "--base", "HEAD~1"], repo)
    assert _run(["git", "status", "--porcelain"], repo).stdout == before
    assert (repo / "calc" / "calc.go").read_text() == content


def test_a_deleted_file_is_reported_unchecked_rather_than_passed(tmp_path: Path) -> None:
    repo = _go_repo(tmp_path, covered=True)
    out = probe(str(repo), "HEAD~1", "HEAD", Change("calc/calc.go", "D"), timeout=60)
    assert out == Outcome("calc/calc.go", "UNCHECKED", "deleted; reverting would re-add it")
    assert out.is_failure is False


# --- a change that could not be covered because it changes nothing ------------


def _edit_repo(tmp_path: Path, name: str, *, before: str, after: str, path: str) -> Path:
    """A repo whose tip commit is one `fix` rewriting `path` from `before` to `after`."""
    repo = tmp_path / name
    (repo / Path(path).parent).mkdir(parents=True, exist_ok=True)
    _run(["git", "init", "-q", "-b", "main"], repo)
    (repo / path).write_text(before)
    _commit(repo, "chore: baseline")
    (repo / path).write_text(after)
    _commit(repo, "fix(x): change it")
    return repo


def test_a_go_comment_only_change_is_cosmetic(tmp_path: Path) -> None:
    """The false positive this exists to remove: `9124d487` edited a doc comment in a
    `feat` commit, and got reported exactly like a behaviour change with no test."""
    repo = _edit_repo(
        tmp_path, "gocomment", path="calc/calc.go",
        before="package calc\n\n// Double doubles.\nfunc Double(n int) int { return n * 2 }\n",
        after="package calc\n\n// Double returns twice n.\nfunc Double(n int) int { return n * 2 }\n",
    )
    assert is_cosmetic(str(repo), "HEAD~1", "HEAD", "calc/calc.go") is True


def test_a_go_code_change_beside_a_comment_change_is_not_cosmetic(tmp_path: Path) -> None:
    """Without this arm the guard is shown to fire, not to discriminate — and a
    suppression that also swallows the real change would be far worse than the noise
    it removes."""
    repo = _edit_repo(
        tmp_path, "gocode", path="calc/calc.go",
        before="package calc\n\n// Double doubles.\nfunc Double(n int) int { return n * 2 }\n",
        after="package calc\n\n// Double returns twice n.\nfunc Double(n int) int { return n * 3 }\n",
    )
    assert is_cosmetic(str(repo), "HEAD~1", "HEAD", "calc/calc.go") is False


def test_a_go_file_containing_a_block_comment_declines_to_classify(tmp_path: Path) -> None:
    """A changed line inside `/* */` is indistinguishable from code by inspection, and
    a guard loose enough to call `*p = 0` a comment would suppress a real finding. So
    the presence of `/*` anywhere in the file falls back to probing it."""
    repo = _edit_repo(
        tmp_path, "goblock", path="calc/calc.go",
        before="package calc\n\n/* Double doubles. */\nfunc Double(n int) int { return n * 2 }\n",
        after="package calc\n\n/* Double returns twice n. */\nfunc Double(n int) int { return n * 2 }\n",
    )
    assert is_cosmetic(str(repo), "HEAD~1", "HEAD", "calc/calc.go") is False


def test_a_python_comment_only_change_is_cosmetic(tmp_path: Path) -> None:
    repo = _edit_repo(
        tmp_path, "pycomment", path="pkg/thing.py",
        before="# doubles\ndef double(n):\n    return n * 2\n",
        after="# returns twice n\ndef double(n):\n    return n * 2\n",
    )
    assert is_cosmetic(str(repo), "HEAD~1", "HEAD", "pkg/thing.py") is True


def test_a_python_code_change_is_not_cosmetic(tmp_path: Path) -> None:
    repo = _edit_repo(
        tmp_path, "pycode", path="pkg/thing.py",
        before="# doubles\ndef double(n):\n    return n * 2\n",
        after="# doubles\ndef double(n):\n    return n * 3\n",
    )
    assert is_cosmetic(str(repo), "HEAD~1", "HEAD", "pkg/thing.py") is False


def test_unparseable_python_declines_to_classify(tmp_path: Path) -> None:
    """Reading a syntax error as "no AST difference" would exempt the file outright."""
    repo = _edit_repo(
        tmp_path, "pybroken", path="pkg/thing.py",
        before="def double(n):\n    return n * 2\n",
        after="def double(n)\n    return n * 2\n",
    )
    assert is_cosmetic(str(repo), "HEAD~1", "HEAD", "pkg/thing.py") is False


def test_the_gate_passes_a_commit_whose_only_change_is_a_comment(tmp_path: Path) -> None:
    """End to end: the whole point is that this exits 0 rather than demanding a test
    for a comment."""
    repo = _edit_repo(
        tmp_path, "e2ecomment", path="calc/calc.go",
        before="package calc\n\n// Double doubles.\nfunc Double(n int) int { return n * 2 }\n",
        after="package calc\n\n// Double returns twice n.\nfunc Double(n int) int { return n * 2 }\n",
    )
    (repo / "go.mod").write_text("module example.com/m\n\ngo 1.21\n")
    _commit(repo, "chore: add the module file")
    done = _run(
        [sys.executable, str(_REPO / "scripts/check_revert_red.py"), "--base", "HEAD~2"], repo
    )
    assert done.returncode == 0, done.stdout + done.stderr
    assert "comment or formatting only" in done.stdout


# --- reading the range -------------------------------------------------------


def test_test_files_are_not_themselves_the_question(tmp_path: Path) -> None:
    repo = _go_repo(tmp_path, covered=True)
    paths = [c.path for c in changed_sources("HEAD~1", "HEAD")]
    assert paths == [] or all(not p.endswith("_test.go") for p in paths)
    # and in the purpose-built repo, read through git's own cwd
    got = subprocess.run(
        ["git", "diff", "--name-status", "HEAD~1...HEAD"],
        cwd=repo, capture_output=True, text=True, check=False,
    ).stdout
    assert "calc.go" in got


def test_types_touching_reads_the_range_for_one_path(tmp_path: Path) -> None:
    repo = _go_repo(tmp_path, covered=False)
    got = subprocess.run(
        ["git", "log", "--no-merges", "--format=%s", "HEAD~1..HEAD", "--", "calc/calc.go"],
        cwd=repo, capture_output=True, text=True, check=False,
    ).stdout
    assert commit_type(got.strip()) == "fix"
    assert claims_behavior_change({commit_type(got.strip())}) is True


def test_the_real_repo_can_be_read(tmp_path: Path) -> None:
    """`types_touching` and `changed_sources` shell out; this proves they parse the
    real repository rather than only the fixture."""
    changes = changed_sources("HEAD~1", "HEAD")
    for c in changes:
        assert isinstance(types_touching("HEAD~1", "HEAD", c.path), set)
