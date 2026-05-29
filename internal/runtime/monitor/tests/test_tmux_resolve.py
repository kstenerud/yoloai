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
