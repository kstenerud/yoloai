# ABOUTME: Tests for the smoke-harness failure-autopsy machinery in
# ABOUTME: smoke_test.py — fingerprint scan, event timeline, baseline diff.
"""Unit tests for the Tier 1-3 smoke-harness diagnostics.

These cover the pure-ish artifact-reading functions: fingerprint scanning
(seeded from the backend-idiosyncrasies symptom index), the key-event
timeline (gap flagging + run-length collapse), the last-good baseline
round-trip, and the FAILURE.md writer. All file I/O is driven through
tmp_path so no real sandbox or binary is required.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

import smoke_test


def _write_jsonl(path: Path, events: list[tuple[str, str, str]]) -> None:
    """Write (ts, event, msg) tuples as one JSON object per line."""
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w") as f:
        for ts, ev, msg in events:
            f.write(json.dumps({"ts": ts, "event": ev, "msg": msg}) + "\n")


def _make_attempt(tmp_path: Path, *, setup_log: str = "",
                  events: list[tuple[str, str, str]] | None = None) -> Path:
    """Build an attempt dir with one sandbox subdir holding setup.log + logs/."""
    attempt = tmp_path / "attempt1"
    sandbox = attempt / "sb"
    (sandbox / "logs").mkdir(parents=True)
    if setup_log:
        (sandbox / "setup.log").write_text(setup_log)
    if events:
        _write_jsonl(sandbox / "logs" / "monitor.jsonl", events)
    return attempt


# --- fingerprint scanning ------------------------------------------------


def test_tmux_firstlaunch_fingerprint_is_headline(tmp_path: Path) -> None:
    """The tmux/firstlaunch race must win over the generic traceback catch-all
    and cite its idiosyncrasy anchor."""
    log = (
        "Traceback (most recent call last):\n"
        "  File \"sandbox-setup.py\", line 698, in setup_tmux_session\n"
        "FileNotFoundError: [Errno 2] No such file or directory: 'tmux'\n"
    )
    hits = smoke_test.scan_fingerprints(_make_attempt(tmp_path, setup_log=log))
    assert hits, "expected at least one fingerprint hit"
    assert hits[0].fp.label.startswith("tmux unresolvable")
    assert "firstlaunch-window" in hits[0].fp.anchor
    # The generic traceback fingerprint should also match, but after the
    # specific one.
    assert any(h.fp.label == "Python traceback in guest setup" for h in hits)


def test_seatbelt_sigtrap_fingerprint(tmp_path: Path) -> None:
    log = "dyld: launch error\nTrace/BPT trap: 5\n"
    hits = smoke_test.scan_fingerprints(_make_attempt(tmp_path, setup_log=log))
    labels = [h.fp.label for h in hits]
    assert any("Seatbelt" in lbl for lbl in labels)


def test_no_fingerprint_when_artifacts_clean(tmp_path: Path) -> None:
    hits = smoke_test.scan_fingerprints(_make_attempt(tmp_path, setup_log="all good\n"))
    assert hits == []


# --- timeline ------------------------------------------------------------


def test_timeline_flags_large_gap(tmp_path: Path) -> None:
    attempt = _make_attempt(tmp_path, events=[
        ("2026-05-29T03:19:53", "firstlaunch.started", "x"),
        ("2026-05-29T03:20:25", "tmux.start", "y"),  # +32s
    ])
    timeline = smoke_test.build_timeline(attempt)
    joined = "\n".join(timeline)
    assert "firstlaunch.started" in joined
    assert "GAP 32s" in joined


def test_timeline_collapses_repeated_events(tmp_path: Path) -> None:
    events = [("2026-05-29T03:19:50", "start", "go")]
    for i in range(5):
        events.append((f"2026-05-29T03:20:{10 + i:02d}", "sandbox.info", "poll"))
    timeline = smoke_test.build_timeline(_make_attempt(tmp_path, events=events))
    joined = "\n".join(timeline)
    assert "sandbox.info (x5" in joined


def test_timeline_empty_when_no_events(tmp_path: Path) -> None:
    assert smoke_test.build_timeline(_make_attempt(tmp_path, setup_log="x")) == []


# --- baseline round-trip -------------------------------------------------


def _make_ctx(tmp_path: Path) -> smoke_test.RunContext:
    return smoke_test.RunContext(
        yoloai_bin="/nonexistent/yoloai",  # binary_version_info degrades to "?"
        tmpdir=tmp_path,
        log_dir=tmp_path / "run",
        run_id="smoke-test",
        fixture_dir=tmp_path,
        full=True,
    )


def test_baseline_save_and_diff_reports_stall(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """save_baseline captures a passing run's event surface; baseline_diff_lines
    then reports which of those steps a later failing run never reached."""
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.setattr(smoke_test, "_BASELINE_ROOT", tmp_path / "baselines")

    # A live, passing sandbox that got all the way to agent.ready.
    sb_logs = tmp_path / ".yoloai" / "sandboxes" / "sb-good" / "logs"
    _write_jsonl(sb_logs / "monitor.jsonl", [
        ("2026-05-29T03:19:50", "tmux.start", "x"),
        ("2026-05-29T03:19:52", "sandbox.tmux_new_session", "x"),
        ("2026-05-29T03:19:55", "agent.ready", "x"),
    ])
    (tmp_path / ".yoloai" / "sandboxes" / "sb-good" / "environment.json").write_text("{}")

    ctx = _make_ctx(tmp_path)
    smoke_test.save_baseline(ctx, "full_workflow/tart", ["sb-good"])

    baseline_file = tmp_path / "baselines" / "full_workflow" / "tart.json"
    assert baseline_file.is_file()
    record = json.loads(baseline_file.read_text())
    assert record["last_event"] == "agent.ready"

    # A failing attempt that died at tmux.start.
    failing = _make_attempt(tmp_path, events=[
        ("2026-05-29T03:19:50", "tmux.start", "x"),
    ])
    diff = "\n".join(smoke_test.baseline_diff_lines("full_workflow/tart", failing))
    assert "Baseline comparison" in diff
    assert "agent.ready" in diff
    assert "sandbox.tmux_new_session" in diff


def test_baseline_diff_empty_without_baseline(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(smoke_test, "_BASELINE_ROOT", tmp_path / "baselines")
    failing = _make_attempt(tmp_path, events=[("2026-05-29T03:19:50", "x", "y")])
    assert smoke_test.baseline_diff_lines("full_workflow/tart", failing) == []


# --- autopsy writer ------------------------------------------------------


def test_write_failure_autopsy_populates_result_and_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(smoke_test, "_BASELINE_ROOT", tmp_path / "baselines")
    log = "FileNotFoundError: [Errno 2] No such file or directory: 'tmux'\n"
    attempt = _make_attempt(tmp_path, setup_log=log, events=[
        ("2026-05-29T03:19:53", "firstlaunch.started", "x"),
        ("2026-05-29T03:20:25", "tmux.start", "y"),
    ])
    ctx = _make_ctx(tmp_path)
    result = smoke_test.TestResult(
        name="full_workflow/tart", passed=False, reason="sentinel not seen"
    )
    out = smoke_test.write_failure_autopsy(ctx, result, attempt)
    assert out is not None and out.is_file()
    assert result.autopsy_path == str(out)
    assert result.fingerprints and result.fingerprints[0].startswith("tmux unresolvable")
    body = out.read_text()
    assert "# Failure autopsy: full_workflow/tart" in body
    assert "GAP 32s" in body
