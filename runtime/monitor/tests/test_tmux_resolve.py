# ABOUTME: Tests for tmux_io.tmux_bin() — lazy resolution with bounded retry
# ABOUTME: through the transient post-firstlaunch window on Tart macOS VMs.
"""Resolution-retry tests for tmux_io.tmux_bin().

The Tart VM failure these guard against: `xcodebuild -runFirstLaunch`
triggers a macOS security-scan storm that transiently hides tmux from both
PATH lookup and direct stat for several seconds, even though tmux is
installed. A single probe at process start can miss it. tmux_bin() must
re-probe with backoff and cache the first success, and must never cache the
literal "tmux" fallback (so one transient miss can't poison later calls).
"""

from __future__ import annotations

import time
from pathlib import Path
from typing import Iterator

import pytest
import tmux_io


@pytest.fixture(autouse=True)
def _clean_cache_and_no_sleep(monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    """Reset the resolution cache around each test and stub out the backoff
    sleep so the retry budget runs instantly. tmux_io references this same
    `time` module object, so patching it here also silences its sleeps."""
    tmux_io.reset_tmux_bin()
    monkeypatch.setattr(time, "sleep", lambda _s: None)
    try:
        yield
    finally:
        tmux_io.reset_tmux_bin()


def test_resolves_on_first_probe(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(tmux_io, "_probe_tmux_bin", lambda: "/opt/homebrew/bin/tmux")
    assert tmux_io.tmux_bin() == "/opt/homebrew/bin/tmux"


def test_retries_through_transient_window(monkeypatch: pytest.MonkeyPatch) -> None:
    """tmux is invisible for the first few probes, then appears."""
    calls = {"n": 0}

    def flaky_probe() -> str | None:
        calls["n"] += 1
        return "/opt/homebrew/bin/tmux" if calls["n"] >= 5 else None

    monkeypatch.setattr(tmux_io, "_probe_tmux_bin", flaky_probe)
    assert tmux_io.tmux_bin() == "/opt/homebrew/bin/tmux"
    assert calls["n"] == 5


def test_falls_back_to_literal_after_budget(monkeypatch: pytest.MonkeyPatch) -> None:
    calls = {"n": 0}

    def never_found() -> None:
        calls["n"] += 1
        return None

    monkeypatch.setattr(tmux_io, "_probe_tmux_bin", never_found)
    assert tmux_io.tmux_bin() == "tmux"
    assert calls["n"] == tmux_io._RESOLVE_ATTEMPTS


def test_success_is_cached(monkeypatch: pytest.MonkeyPatch) -> None:
    """Once resolved, later calls don't re-probe."""
    calls = {"n": 0}

    def probe_once() -> str:
        calls["n"] += 1
        return "/usr/local/bin/tmux"

    monkeypatch.setattr(tmux_io, "_probe_tmux_bin", probe_once)
    assert tmux_io.tmux_bin() == "/usr/local/bin/tmux"
    assert tmux_io.tmux_bin() == "/usr/local/bin/tmux"
    assert calls["n"] == 1


def test_literal_fallback_is_not_cached(monkeypatch: pytest.MonkeyPatch) -> None:
    """A transient miss must not poison a later, recovered resolution."""
    state = {"available": False}

    def conditional_probe() -> str | None:
        return "/opt/homebrew/bin/tmux" if state["available"] else None

    monkeypatch.setattr(tmux_io, "_probe_tmux_bin", conditional_probe)
    assert tmux_io.tmux_bin() == "tmux"  # budget exhausted, not cached
    state["available"] = True
    assert tmux_io.tmux_bin() == "/opt/homebrew/bin/tmux"


# --- firstlaunch-context gating ------------------------------------------


def test_context_predicate_tracks_marker(tmp_path: Path) -> None:
    """_in_firstlaunch_context is True only while a registered marker file
    exists on disk."""
    marker = tmp_path / "started"
    tmux_io.set_firstlaunch_marker(str(marker))
    assert tmux_io._in_firstlaunch_context() is False  # not created yet
    marker.write_text("")
    assert tmux_io._in_firstlaunch_context() is True
    marker.unlink()
    assert tmux_io._in_firstlaunch_context() is False  # gone again


def test_context_false_without_marker() -> None:
    """No marker registered (every non-Tart path) ⇒ never in context."""
    assert tmux_io._in_firstlaunch_context() is False


def test_firstlaunch_probes_past_fixed_budget(monkeypatch: pytest.MonkeyPatch) -> None:
    """In firstlaunch context, resolution must keep probing well past the fixed
    retry budget — the scan storm outlasts both the budget and the xcodebuild
    process. This is the regression guard: the old code stopped at a short
    grace once xcodebuild finished and crashed when tmux was still hidden."""
    appear_at = tmux_io._RESOLVE_ATTEMPTS + 40
    state = {"probes": 0}

    def probe() -> str | None:
        state["probes"] += 1
        return "/opt/homebrew/bin/tmux" if state["probes"] >= appear_at else None

    monkeypatch.setattr(tmux_io, "_probe_tmux_bin", probe)
    monkeypatch.setattr(tmux_io, "_in_firstlaunch_context", lambda: True)
    assert tmux_io.tmux_bin() == "/opt/homebrew/bin/tmux"
    assert state["probes"] >= appear_at


def test_firstlaunch_hits_ceiling_then_literal(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """A firstlaunch that never makes tmux resolvable must not hang forever:
    the hard ceiling bounds the wait, then we fall back to the literal."""
    calls = {"n": 0}

    def never() -> None:
        calls["n"] += 1
        return None

    monkeypatch.setattr(tmux_io, "_probe_tmux_bin", never)
    monkeypatch.setattr(tmux_io, "_in_firstlaunch_context", lambda: True)
    assert tmux_io.tmux_bin() == "tmux"
    expected = int(tmux_io._FIRSTLAUNCH_MAX_WAIT_SECONDS / tmux_io._RESOLVE_DELAY_SECONDS)
    assert calls["n"] >= expected
