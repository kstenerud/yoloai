# ABOUTME: Unit tests for status-monitor.py's HookDetector, which reads agent
# ABOUTME: idle/active state from the append-only logs/agent-hooks.jsonl log.
"""Tests for HookDetector.

HookDetector consumes the append-only hook event log (logs/agent-hooks.jsonl)
written exclusively by the agent's Stop / PreToolUse / UserPromptSubmit hooks.
These tests cover: idle confirmation after the grace period, active reporting,
the stale-active -> idle fallback, the restart EOF-skip (a fresh monitor must
ignore the previous session's events), incremental consumption, partial-line
tolerance, and truncation/rotation recovery.

A fake monotonic clock is installed so grace/staleness windows can be crossed
deterministically without sleeping.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Iterator

import pytest

from conftest import load_status_monitor

sm = load_status_monitor()


@pytest.fixture
def clock(monkeypatch: pytest.MonkeyPatch) -> Iterator[list[float]]:
    """Install a controllable monotonic clock; yields a one-element holder."""
    now = [1000.0]
    monkeypatch.setattr(sm.time, "monotonic", lambda: now[0])
    yield now


def _append(path: Path, status: str) -> None:
    with open(path, "a") as f:
        f.write(json.dumps({"event": f"hook.{status}", "status": status}) + "\n")


def _make(path: Path) -> Any:
    return sm.HookDetector(str(path))


def test_no_events_is_unknown(tmp_path: Path, clock: list[float]) -> None:
    log = tmp_path / "agent-hooks.jsonl"
    log.touch()
    det = _make(log)
    assert det.check(None).status == "unknown"


def test_idle_confirms_after_grace(tmp_path: Path, clock: list[float]) -> None:
    log = tmp_path / "agent-hooks.jsonl"
    log.touch()
    det = _make(log)
    _append(log, "idle")
    # Within the grace window: not yet confirmed.
    assert det.check(None).status == "unknown"
    clock[0] += sm.HOOK_IDLE_GRACE
    assert det.check(None).status == "idle"


def test_active_reports_active(tmp_path: Path, clock: list[float]) -> None:
    log = tmp_path / "agent-hooks.jsonl"
    log.touch()
    det = _make(log)
    _append(log, "active")
    assert det.check(None).status == "active"


def test_stale_active_becomes_idle(tmp_path: Path, clock: list[float]) -> None:
    log = tmp_path / "agent-hooks.jsonl"
    log.touch()
    det = _make(log)
    _append(log, "active")
    assert det.check(None).status == "active"
    clock[0] += sm.HOOK_IDLE_AGE
    assert det.check(None).status == "idle"


def test_active_clears_idle(tmp_path: Path, clock: list[float]) -> None:
    log = tmp_path / "agent-hooks.jsonl"
    log.touch()
    det = _make(log)
    _append(log, "idle")
    assert det.check(None).status == "unknown"  # observe idle, start grace
    clock[0] += sm.HOOK_IDLE_GRACE
    assert det.check(None).status == "idle"
    # A new active event flips the state back immediately.
    _append(log, "active")
    assert det.check(None).status == "active"


def test_last_event_in_batch_wins(tmp_path: Path, clock: list[float]) -> None:
    log = tmp_path / "agent-hooks.jsonl"
    log.touch()
    det = _make(log)
    # Several events appended between polls; the last one decides.
    _append(log, "active")
    _append(log, "idle")
    _append(log, "active")
    assert det.check(None).status == "active"


def test_restart_skips_prior_session_events(tmp_path: Path, clock: list[float]) -> None:
    log = tmp_path / "agent-hooks.jsonl"
    # Previous session left an idle event in the log.
    _append(log, "idle")
    # A monitor restarted by stop/start must ignore it (seek to EOF on init).
    det = _make(log)
    clock[0] += sm.HOOK_IDLE_GRACE
    assert det.check(None).status == "unknown"
    # Only events appended after construction are considered.
    _append(log, "active")
    assert det.check(None).status == "active"


def test_partial_final_line_is_reread_whole(tmp_path: Path, clock: list[float]) -> None:
    log = tmp_path / "agent-hooks.jsonl"
    log.touch()
    det = _make(log)
    # Simulate a hook mid-append: a complete line followed by an unterminated one.
    with open(log, "a") as f:
        f.write(json.dumps({"event": "hook.active", "status": "active"}) + "\n")
        f.write('{"event":"hook.idle","status":"id')  # no newline yet
    assert det.check(None).status == "active"  # only the complete line counted
    # The hook finishes writing the line.
    with open(log, "a") as f:
        f.write('le"}\n')
    assert det.check(None).status == "unknown"  # observe idle, start grace
    clock[0] += sm.HOOK_IDLE_GRACE
    assert det.check(None).status == "idle"


def test_malformed_line_ignored(tmp_path: Path, clock: list[float]) -> None:
    log = tmp_path / "agent-hooks.jsonl"
    log.touch()
    det = _make(log)
    with open(log, "a") as f:
        f.write("not json at all\n")
        f.write(json.dumps({"event": "hook.idle", "status": "idle"}) + "\n")
    assert det.check(None).status == "unknown"  # observe idle, start grace
    clock[0] += sm.HOOK_IDLE_GRACE
    assert det.check(None).status == "idle"


def test_truncation_recovers(tmp_path: Path, clock: list[float]) -> None:
    log = tmp_path / "agent-hooks.jsonl"
    log.touch()
    det = _make(log)
    _append(log, "active")
    assert det.check(None).status == "active"
    # File rotated/truncated out from under us, then a fresh event appended.
    log.write_text("")
    _append(log, "idle")
    assert det.check(None).status == "unknown"  # observe idle, start grace
    clock[0] += sm.HOOK_IDLE_GRACE
    assert det.check(None).status == "idle"


def test_missing_log_is_unknown(tmp_path: Path, clock: list[float]) -> None:
    log = tmp_path / "agent-hooks.jsonl"  # never created
    det = _make(log)
    assert det.check(None).status == "unknown"
