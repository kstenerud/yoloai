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


# --- firstlaunch-window gating -------------------------------------------


def test_in_progress_predicate_tracks_markers(tmp_path: Path) -> None:
    """_firstlaunch_in_progress is True only between the started and done
    markers appearing."""
    started = tmp_path / "started"
    done = tmp_path / "done"
    tmux_io.set_firstlaunch_markers(str(started), str(done))
    assert tmux_io._firstlaunch_in_progress() is False  # nothing created yet
    started.write_text("")
    assert tmux_io._firstlaunch_in_progress() is True
    done.write_text("")
    assert tmux_io._firstlaunch_in_progress() is False  # window closed


def test_in_progress_false_without_markers() -> None:
    """No markers registered (every non-Tart path) ⇒ never in a window."""
    assert tmux_io._firstlaunch_in_progress() is False


def test_waits_out_window_past_fixed_budget(monkeypatch: pytest.MonkeyPatch) -> None:
    """While firstlaunch is in progress, resolution must NOT give up at the
    fixed retry budget — the storm outlasts it. tmux appears only after more
    probes than the budget would allow."""
    appear_at = tmux_io._RESOLVE_ATTEMPTS + 20
    state = {"probes": 0}

    def probe() -> str | None:
        state["probes"] += 1
        return "/opt/homebrew/bin/tmux" if state["probes"] >= appear_at else None

    monkeypatch.setattr(tmux_io, "_probe_tmux_bin", probe)
    monkeypatch.setattr(tmux_io, "_firstlaunch_in_progress", lambda: True)
    assert tmux_io.tmux_bin() == "/opt/homebrew/bin/tmux"
    assert state["probes"] >= appear_at


def test_resolves_in_grace_after_window_closes(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Once firstlaunch finishes, the bounded grace covers the storm's tail."""
    state = {"probes": 0}

    monkeypatch.setattr(
        tmux_io, "_firstlaunch_in_progress", lambda: state["probes"] < 3
    )

    def probe() -> str | None:
        state["probes"] += 1
        return "/opt/homebrew/bin/tmux" if state["probes"] >= 5 else None

    monkeypatch.setattr(tmux_io, "_probe_tmux_bin", probe)
    assert tmux_io.tmux_bin() == "/opt/homebrew/bin/tmux"


def test_stuck_window_hits_ceiling_then_literal(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """A firstlaunch child that dies without signalling completion must not
    hang forever: the hard ceiling bounds the wait, then we give up."""
    calls = {"n": 0}

    def never() -> None:
        calls["n"] += 1
        return None

    monkeypatch.setattr(tmux_io, "_probe_tmux_bin", never)
    monkeypatch.setattr(tmux_io, "_firstlaunch_in_progress", lambda: True)
    assert tmux_io.tmux_bin() == "tmux"
    # window-wait ceiling probes + the bounded grace afterwards
    expected = int(tmux_io._FIRSTLAUNCH_MAX_WAIT_SECONDS / tmux_io._RESOLVE_DELAY_SECONDS)
    assert calls["n"] >= expected
