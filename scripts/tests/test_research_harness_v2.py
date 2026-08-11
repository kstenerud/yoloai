#!/usr/bin/env python3
# ABOUTME: Tests for research harness v2. Each pins an invariant that a real invalid
# ABOUTME: run from the 2026-08 enforcement research violated — including the class
# ABOUTME: v1 could not see, where the probe itself never discriminated.

from __future__ import annotations

import io
import sys
from pathlib import Path

import pytest

_REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(_REPO / "scripts"))

from research_harness_v2 import (  # noqa: E402
    HARNESS_VERSION,
    Harness,
    HarnessError,
    Probe,
    counter_moved,
)


def _armed(name: str = "T") -> Harness:
    """A harness with the two things report() insists on, so tests can vary one thing."""
    h = Harness(name)
    h.control("rig is live", True)
    h.not_tried("nothing")
    return h


def _flipping(h: Harness, name: str = "reach") -> tuple[Probe, list[bool]]:
    """A probe that answers True then False, i.e. one that genuinely discriminates."""
    answers = [True, False]
    probe = h.probe(name, lambda: answers.pop(0))
    return probe, answers


# -- the v2 invariant: a probe must be shown reporting failure ----------------


def test_expect_refuses_a_probe_that_was_never_baselined() -> None:
    """The probe may discriminate, but this run never showed that it does."""
    h = _armed("unbaselined")
    probe = h.probe("guest reaches denied host", lambda: False)
    after = probe.sample()
    with pytest.raises(HarnessError, match="was never baselined"):
        h.expect("the denied host is blocked", after, want=False)


def test_expect_refuses_when_the_baseline_matches_the_claim() -> None:
    """pf-spoof run 2: every probe answered 'cannot' and nothing ever answered 'can'.

    The sandbox lacked CAP_NET_ADMIN, so the run recorded 7 PASS / 0 FAIL for the
    exact inverse of the truth. Its outputs were real; its instrument only ever said
    one thing.
    """
    h = _armed("one-answer")
    probe = h.probe("guest can change its address", lambda: False)
    probe.baseline(want=False)
    after = probe.sample()
    with pytest.raises(HarnessError, match="never shown producing the opposite"):
        h.expect("the guest cannot change its address", after, want=False)


def test_a_baseline_that_reads_the_wrong_way_voids_the_run() -> None:
    """P1 run 1: the allowlisted destination was already unreachable before any policy."""
    h = Harness("bad-baseline")
    probe = h.probe("allowlisted host reachable", lambda: False)
    with pytest.raises(HarnessError, match="baseline for probe"):
        probe.baseline(want=True)


def test_a_baselined_probe_carries_an_expectation() -> None:
    """The whole point: one callable, shown answering both ways, in one run."""
    h = _armed("discriminating")
    probe, _ = _flipping(h)
    probe.baseline(want=True)
    after = probe.sample("after policy loads")
    e = h.expect("policy blocks the denied host", after, want=False)
    assert e.holds
    assert h.report(io.StringIO())


def test_an_unbaselined_claim_needs_a_reason_and_is_rendered() -> None:
    """A capability probe has no mechanism-absent state. The waiver is visible, not silent."""
    h = _armed("waived")
    m = h.measure("NET_ADMIN alone is enough for the helper", True)
    h.expect("the helper needs only NET_ADMIN", m, unbaselined="a capability, not a before/after")
    out = io.StringIO()
    assert h.report(out)
    assert "UNBASELINED CLAIMS" in out.getvalue()
    assert "a capability, not a before/after" in out.getvalue()


def test_a_standalone_measurement_cannot_carry_a_claim_silently() -> None:
    h = _armed("standalone")
    m = h.measure("denied host", False)
    with pytest.raises(HarnessError, match="rests on a standalone measurement"):
        h.expect("the denied host is blocked", m, want=False)


def test_a_probe_name_cannot_be_reused() -> None:
    """pf-no-state run 5: control from one cell, verdict from another that looked the same."""
    h = _armed("dup")
    probe = h.probe("reach", lambda: True)
    probe.baseline(want=True)
    with pytest.raises(HarnessError, match="already declared"):
        h.probe("reach", lambda: False)


def test_a_probe_returning_a_non_bool_is_refused_at_the_source() -> None:
    h = _armed("stringy-probe")
    probe = h.probe("reach", lambda: "blocked")  # type: ignore[arg-type,return-value]
    with pytest.raises(HarnessError, match="not a bool"):
        probe.baseline(want=True)


# -- v1 defect 1: expect() judged truthiness ---------------------------------


def test_expect_refuses_a_string_value() -> None:
    """v1 passed `''` — a probe that produced no output at all — as a containment claim."""
    h = _armed("stringy")
    m = h.measure("host state", "")
    with pytest.raises(HarnessError, match="non-bool value"):
        h.expect("the host is blocked", m, want=True, unbaselined="n/a")


def test_expect_refuses_a_truthy_string_that_contradicts_its_own_claim() -> None:
    """The other direction: v1 FAILED "the host is blocked" for a value of "blocked"."""
    h = _armed("stringy2")
    m = h.measure("host state", "blocked")
    with pytest.raises(HarnessError, match="non-bool value"):
        h.expect("the host is blocked", m, want=True, unbaselined="n/a")


# -- v1 defect 2: a failed control destroyed the evidence --------------------


def test_a_failed_control_renders_the_measurements_before_voiding() -> None:
    """K1's first run: the control fired correctly and left nothing on the page to explain it."""
    h = Harness("evidence")
    h.control("rig is live", False)
    h.measure("drop counter", 0, detail="no packet reached the rule")
    h.not_tried("x")
    out = io.StringIO()
    with pytest.raises(HarnessError, match="control\\(s\\) did not hold"):
        h.report(out)
    rendered = out.getvalue()
    assert "VOID" in rendered
    assert "drop counter" in rendered
    assert "no packet reached the rule" in rendered
    assert "no verdict is rendered" in rendered


def test_a_failed_precondition_also_renders_what_was_collected() -> None:
    h = Harness("precondition")
    h.measure("euid", 1000)
    out = io.StringIO()
    h._stream = out
    with pytest.raises(HarnessError, match="precondition failed"):
        h.require("nft is present", False, "not on PATH")
    assert "euid" in out.getvalue()


# -- v1 defect 3: no arm-level void ------------------------------------------


def test_voiding_one_arm_leaves_the_other_deciding() -> None:
    """P1b made a per-arm precondition fatal, which discarded a valid arm."""
    h = Harness("two-arm")
    h.control("rig is live", True)
    h.not_tried("x")

    v4, _ = _flipping(h, "v4 reach")
    v4.baseline(want=True)
    h.expect("v4 is blocked", v4.sample(), want=False, arm="v4")

    v6 = h.probe("v6 reach", lambda: True)
    h.void_arm("v6", "this host has no IPv6 address")
    h.expect("v6 is blocked", h.measure("v6 reach", True, arm="v6"), want=False,
             arm="v6", unbaselined="arm is void")

    out = io.StringIO()
    assert h.report(out) is True
    rendered = out.getvalue()
    assert "[VOID] v6 is blocked" in rendered
    assert "[PASS] v4 is blocked" in rendered
    assert "this host has no IPv6 address" in rendered
    assert v6 is not None


def test_a_failed_control_inside_a_void_arm_does_not_void_the_run() -> None:
    h = Harness("void-arm-control")
    h.control("rig is live", True)
    h.control("v6 address assigned", False, arm="v6")
    h.void_arm("v6", "no IPv6 on this host")
    h.not_tried("x")
    assert h.report(io.StringIO()) is True


# -- invariants inherited from v1, which must not have regressed -------------


def test_report_refuses_without_a_control() -> None:
    h = Harness("no-control")
    h.not_tried("x")
    out = io.StringIO()
    with pytest.raises(HarnessError, match="no control or baseline was declared"):
        h.report(out)


def test_report_refuses_without_not_tried() -> None:
    h = Harness("no-bounds")
    h.control("rig is live", True)
    with pytest.raises(HarnessError, match="not_tried"):
        h.report(io.StringIO())


def test_expect_refuses_an_unrecorded_measurement() -> None:
    from research_harness_v2 import Measurement

    h = _armed("foreign")
    stray = Measurement(label="never recorded", value=True)
    with pytest.raises(HarnessError, match="never recorded"):
        h.expect("something", stray, unbaselined="n/a")


def test_run_raises_on_a_non_zero_exit_by_default() -> None:
    h = _armed("cmd")
    with pytest.raises(HarnessError, match="command failed"):
        h.run(["false"])


def test_run_can_opt_out_of_checking_explicitly() -> None:
    h = _armed("cmd-ok")
    assert h.run(["false"], check=False).returncode == 1


def test_a_failing_expectation_still_renders_and_reports_false() -> None:
    """A failing research run is often the finding, so it must render, not raise.

    This is the shape where the policy did not work: the probe reached the host
    before it loaded and still reaches it after.
    """
    h = _armed("failing")
    probe = h.probe("reach", lambda: True)
    probe.baseline(want=True)
    h.expect("policy blocks the host", probe.sample(), want=False)
    out = io.StringIO()
    assert h.report(out) is False
    assert "SOME EXPECTATIONS FAILED" in out.getvalue()


def test_not_tried_is_rendered() -> None:
    h = _armed("bounds")
    out = io.StringIO()
    h.report(out)
    assert "WHAT WAS NOT TRIED" in out.getvalue()


def test_counter_moved() -> None:
    assert counter_moved(0, 1)
    assert not counter_moved(0, 0)
    assert not counter_moved(5, 5)


def test_version_is_pinned() -> None:
    """Research pins its contract at the import line; this is the other half of that."""
    assert HARNESS_VERSION == 2
