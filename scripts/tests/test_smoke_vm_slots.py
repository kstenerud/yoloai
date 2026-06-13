# ABOUTME: Tests for the smoke harness's macOS tart VM-slot accounting — parsing
# ABOUTME: doctor's vm_census and deciding skip-vs-clamp from free host slots.

from __future__ import annotations

from smoke_test import parse_vm_census, plan_tart_slots


def test_parse_vm_census_extracts_limit_in_use_and_names() -> None:
    doc = {
        "vm_census": {
            "limit": 2,
            "in_use": 1,
            "slots": [{"vm_name": "yoloai-embrace", "pid": 1, "owned": True}],
        }
    }
    assert parse_vm_census(doc) == (2, 1, ["yoloai-embrace"])


def test_parse_vm_census_missing_census_returns_none() -> None:
    # Non-macOS / tart absent: doctor omits vm_census (omitempty), or it is null.
    assert parse_vm_census({}) is None
    assert parse_vm_census({"vm_census": None}) is None


def test_parse_vm_census_malformed_counts_return_none() -> None:
    assert parse_vm_census({"vm_census": {"limit": "2", "in_use": 1}}) is None
    assert parse_vm_census({"vm_census": {"limit": 2}}) is None


def test_parse_vm_census_drops_unnamed_slots() -> None:
    # An orphaned VM whose name doctor couldn't recover still counts toward
    # in_use, but contributes no name to the occupant list.
    doc = {
        "vm_census": {
            "limit": 2,
            "in_use": 2,
            "slots": [{"vm_name": "a"}, {"pid": 9}],
        }
    }
    assert parse_vm_census(doc) == (2, 2, ["a"])


def test_plan_tart_slots_zero_free_blocks() -> None:
    assert plan_tart_slots(0, 2) == 0
    # Over-limit (negative free) — e.g. two foreign VMs on a 2-VM host — also blocks.
    assert plan_tart_slots(-1, 2) == 0


def test_plan_tart_slots_clamps_to_free() -> None:
    # One foreign VM (e.g. yoloai-embrace) leaves a single slot: run serially.
    assert plan_tart_slots(1, 2) == 1


def test_plan_tart_slots_respects_requested_when_ample() -> None:
    assert plan_tart_slots(2, 2) == 2
    assert plan_tart_slots(5, 2) == 2
