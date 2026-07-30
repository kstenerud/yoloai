# ABOUTME: Tests that a slow sandbox destroy degrades to a reported line instead
# ABOUTME: of an exception that kills the matrix, and that provider cycling keeps
# ABOUTME: one --out-dir per child (DF160).

from __future__ import annotations

import subprocess
import types
from pathlib import Path
from typing import Any, Callable

import pytest

import smoke_test
from smoke_test import _destroy_named_sandboxes, _provider_child_argv, _strip_opt


@pytest.fixture
def ctx(tmp_path: Path) -> smoke_test.RunContext:
    """A real RunContext, not a stand-in.

    The helper under test takes one, and a local stub would drift from the real
    dataclass without anything noticing — the same "tests a shape the product
    does not have" trap AGENTS.md rule 10 names.
    """
    c = smoke_test.RunContext(
        yoloai_bin="./yoloai",
        tmpdir=tmp_path,
        log_dir=tmp_path,
        run_id="test",
        fixture_dir=tmp_path,
    )
    c.sandboxes = ["alive-1", "slow-2", "alive-3"]
    return c


def _fake_run(slow: set[str]) -> Callable[..., Any]:
    """subprocess.run stand-in that times out for the named sandboxes."""

    def run(argv: list[str], **kwargs: Any) -> Any:
        name = argv[-1]
        if name in slow:
            raise subprocess.TimeoutExpired(cmd=argv, timeout=kwargs.get("timeout", 30))
        return types.SimpleNamespace(returncode=0)

    return run


def test_destroy_timeout_does_not_raise(monkeypatch: pytest.MonkeyPatch, ctx: smoke_test.RunContext) -> None:
    """A destroy that times out must not escape.

    This runs in a worker thread, so an exception propagates to the main thread
    via future.result() and aborts the entire matrix — discarding the summary and
    JUnit output for every test that already passed. Observed on macOS/podman: a
    dind sandbox took >30s to destroy and the run died with a Python traceback
    rather than a failure report.
    """
    monkeypatch.setattr(subprocess, "run", _fake_run({"slow-2"}))
    _destroy_named_sandboxes(ctx, ["alive-1", "slow-2", "alive-3"])  # must not raise


def test_destroy_timeout_leaves_the_survivor_for_end_of_run_cleanup(
    monkeypatch: pytest.MonkeyPatch, ctx: smoke_test.RunContext
) -> None:
    """A sandbox that did not die stays on the cleanup list.

    Dropping a name whose sandbox still exists is how a leak survives the run:
    the end-of-run sweep and its `yoloai system prune` fallback only revisit what
    is still listed. The ones that genuinely died must still be dropped, or the
    fallback re-destroys them pointlessly.
    """
    monkeypatch.setattr(subprocess, "run", _fake_run({"slow-2"}))
    _destroy_named_sandboxes(ctx, ["alive-1", "slow-2", "alive-3"])
    assert ctx.sandboxes == ["slow-2"]


def test_destroy_oserror_is_also_survivable(monkeypatch: pytest.MonkeyPatch, ctx: smoke_test.RunContext) -> None:
    """Same reasoning for a failed exec — the worker must still return."""

    def run(argv: list[str], **kwargs: Any) -> Any:
        raise OSError("no such binary")

    monkeypatch.setattr(subprocess, "run", run)
    _destroy_named_sandboxes(ctx, ["alive-1"])
    assert ctx.sandboxes == ["alive-1", "slow-2", "alive-3"]


@pytest.mark.parametrize(
    "argv,want",
    [
        (["--debug", "--out-dir", "/tmp/x", "--jobs", "2"], ["--debug", "--jobs", "2"]),
        (["--out-dir=/tmp/x", "--debug"], ["--debug"]),
        (["--debug"], ["--debug"]),
        (["--out-dir", "/tmp/x"], []),
    ],
)
def test_strip_opt_removes_both_spellings(argv: list[str], want: list[str]) -> None:
    """The provider cycle passes its own --out-dir to each child.

    Leaving the caller's in place would pass the flag twice; argparse takes the
    last, so whether the umbrella layout happens at all would depend on argument
    order rather than on intent.
    """
    assert _strip_opt(argv, "--out-dir") == want


def test_strip_opt_leaves_a_lookalike_value_alone() -> None:
    """Only the option is stripped, never a value that resembles it."""
    assert _strip_opt(["--backend", "--out-dir-ish"], "--out-dir") == [
        "--backend",
        "--out-dir-ish",
    ]


# --- the wiring, not just the pieces -----------------------------------------
# A unit test on _strip_opt stays green if nothing calls it, which is exactly the
# gap AGENTS.md rule 10 now names: red-on-revert proves a test is wired to the
# code, never that the code is wired to the program. These pin the assembly.

UMBRELLA = Path("/runs/yoloai-smoketest-x-all-providers")


def test_child_argv_replaces_the_callers_out_dir_with_the_umbrella() -> None:
    """The umbrella layout is real only if the caller's --out-dir is removed.

    Passing it twice lets argparse take the last one, so whether every provider
    lands under a single uploadable directory would depend on argument order.
    """
    argv = _provider_child_argv(
        ["--debug", "--out-dir", "/somewhere/else", "--all-docker-providers"],
        UMBRELLA,
        "orbstack",
        is_first=True,
    )
    assert argv.count("--out-dir") == 1
    assert "/somewhere/else" not in argv
    assert argv[argv.index("--out-dir") + 1] == str(UMBRELLA / "orbstack")


def test_child_argv_drops_the_cycling_flag_so_children_do_not_recurse() -> None:
    argv = _provider_child_argv(["--all-docker-providers"], UMBRELLA, "orbstack", is_first=True)
    assert "--all-docker-providers" not in argv


def test_only_the_first_provider_runs_the_full_matrix() -> None:
    """Later providers cover docker tiers alone — podman/seatbelt/tart are
    daemon-independent and already ran. A directory holding only docker results
    is therefore expected, which is the misreading that sent the wrong half of a
    failing run upstream."""
    first = _provider_child_argv([], UMBRELLA, "orbstack", is_first=True)
    later = _provider_child_argv([], UMBRELLA, "desktop-linux", is_first=False)
    assert "--backend" not in first
    assert later.count("--backend") == 2
    assert "docker" in later and "docker-priv" in later
