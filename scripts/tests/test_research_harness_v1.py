#!/usr/bin/env python3
# ABOUTME: Tests for research harness v1. Each pins an invariant that a real invalid
# ABOUTME: run from the 2026-08 enforcement research violated. They stay in make check
# ABOUTME: permanently: they are what makes "v1 still works" a checked property.

from __future__ import annotations

import io
import sys
from pathlib import Path

import pytest

_REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(_REPO / "scripts"))

from research_harness_v1 import (  # noqa: E402
    HARNESS_VERSION,
    Harness,
    HarnessError,
    counter_moved,
)


def _armed(name: str = "T") -> Harness:
    """A harness with the two things report() insists on, so tests can vary one thing."""
    h = Harness(name)
    h.control("rig is live", True)
    h.not_tried("nothing")
    return h


def test_report_refuses_without_a_control() -> None:
    """L5c/L10c shape: a negative with no control is indistinguishable from a broken rig."""
    h = Harness("no-control")
    h.not_tried("x")
    m = h.measure("denied host", False)
    h.expect("denied host is blocked", m, want=False)
    with pytest.raises(HarnessError, match="no control was declared"):
        h.report(io.StringIO())


def test_report_refuses_when_a_control_did_not_hold() -> None:
    """P1 run 1: the denied-host control was a dead address, so 'blocked' was free."""
    h = Harness("dead-control")
    h.control("denied host answers with no policy", False)
    h.not_tried("x")
    with pytest.raises(HarnessError, match="did not hold"):
        h.report(io.StringIO())


def test_report_refuses_without_a_not_tried_block() -> None:
    """The Linux pass had no bounding sections and produced five over-wide conclusions."""
    h = Harness("unbounded")
    h.control("rig is live", True)
    with pytest.raises(HarnessError, match="not_tried"):
        h.report(io.StringIO())


def test_expectation_cannot_reference_an_unrecorded_measurement() -> None:
    """The pre-written verdict, structurally: no claim without a value behind it."""
    h = _armed()
    other = Harness("elsewhere").measure("borrowed", True)
    with pytest.raises(HarnessError, match="never recorded"):
        h.expect("claims something", other)


def test_verdict_is_derived_from_the_value_not_the_prose() -> None:
    """l10b:20 printed 'blocked' nine lines below its own data saying otherwise."""
    h = _armed()
    m = h.measure("transfer still flowing", True)
    e = h.expect("revocation stopped the transfer", m, want=False)
    assert e.holds is False
    buf = io.StringIO()
    assert h.report(buf) is False
    text = buf.getvalue()
    assert "[FAIL] revocation stopped the transfer" in text
    assert "transfer still flowing=True" in text


def test_report_renders_and_passes_when_expectations_hold() -> None:
    h = _armed()
    m = h.measure("denied host reachable", False)
    h.expect("the denied host is blocked", m, want=False)
    buf = io.StringIO()
    assert h.report(buf) is True
    out = buf.getvalue()
    assert "[PASS] the denied host is blocked" in out
    assert "WHAT WAS NOT TRIED" in out


def test_require_aborts_on_a_missing_precondition() -> None:
    """L10c: apk could not fetch dig, so both arms reported 'blocked' for free."""
    h = Harness("precondition")
    with pytest.raises(HarnessError, match="[Ee]very result below would be free"):
        h.require("dig is installed", False, "apk add failed")


def test_require_is_silent_when_the_precondition_holds() -> None:
    _armed().require("dig is installed", True)


def test_run_raises_on_nonzero_exit_by_default() -> None:
    """busybox rejected a flag, printed usage, and the bash version sailed on."""
    h = Harness("run")
    with pytest.raises(HarnessError, match="command failed"):
        h.run(["sh", "-c", "exit 3"])


def test_run_can_opt_out_of_checking_explicitly() -> None:
    h = Harness("run")
    proc = h.run(["sh", "-c", "exit 3"], check=False)
    assert proc.returncode == 3


def test_run_passes_stdin_as_text_not_through_a_shell() -> None:
    """`docker exec` without -i swallowed a heredoc and fed nft an empty ruleset."""
    h = Harness("stdin")
    proc = h.run(["cat"], stdin="table inet t {}\n")
    assert proc.stdout == "table inet t {}\n"


def test_counter_moved_distinguishes_a_decision_from_a_silence() -> None:
    """A rate of zero with a zero counter is a free negative, not a result."""
    assert counter_moved(0, 12) is True
    assert counter_moved(5, 5) is False


def test_failures_lists_only_broken_expectations() -> None:
    h = _armed()
    good = h.measure("allowlisted reachable", True)
    bad = h.measure("denied reachable", True)
    h.expect("allowlisted host works", good)
    h.expect("denied host is blocked", bad, want=False)
    assert [e.claim for e in h.failures()] == ["denied host is blocked"]


def test_version_is_pinned() -> None:
    """v1's contract is frozen; a breaking change is a new file, not an edit here."""
    assert HARNESS_VERSION == 1
