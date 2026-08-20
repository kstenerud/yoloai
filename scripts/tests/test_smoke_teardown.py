#!/usr/bin/env python3
# ABOUTME: Tests for smoke-run teardown: cleanup runs once and only once, and a
# ABOUTME: SIGTERM tears this run's sandboxes down instead of leaving them alive,
# ABOUTME: which is what left four sandboxes running for an hour after a killed run.

from __future__ import annotations

import signal
import subprocess
import sys
import textwrap
from pathlib import Path

_REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(_REPO / "scripts"))

import smoke_test  # noqa: E402


def _ctx_with_sandboxes(tmp_path: Path, names: list[str]) -> smoke_test.RunContext:
    """A context whose yoloai_bin is a script recording every destroy it is asked for."""
    recorder = tmp_path / "destroys.txt"
    fake = tmp_path / "yoloai"
    fake.write_text(f'#!/bin/sh\necho "$@" >> {recorder}\nexit 0\n')
    fake.chmod(0o755)
    scratch = tmp_path / "scratch"          # cleanup() rmtree's ctx.tmpdir, so the
    scratch.mkdir(exist_ok=True)            # recorder must not live inside it
    ctx = smoke_test.RunContext(
        yoloai_bin=str(fake),
        tmpdir=scratch,
        log_dir=tmp_path,
        run_id="ysmk-test",
        fixture_dir=tmp_path,
    )
    ctx.sandboxes = list(names)
    return ctx


def test_cleanup_destroys_every_sandbox_it_was_given(tmp_path: Path) -> None:
    ctx = _ctx_with_sandboxes(tmp_path, ["ysmk-test-a", "ysmk-test-b"])
    smoke_test.cleanup(ctx)
    recorded = (tmp_path / "destroys.txt").read_text()
    assert "ysmk-test-a" in recorded
    assert "ysmk-test-b" in recorded


def test_cleanup_runs_once_even_though_two_paths_call_it(tmp_path: Path) -> None:
    """cleanup is reachable from atexit AND from the signal handler. Without a
    guard, a signalled run destroys everything twice and prints two cleanup
    blocks -- the second over sandboxes that no longer exist."""
    ctx = _ctx_with_sandboxes(tmp_path, ["ysmk-test-a"])
    smoke_test.cleanup(ctx)
    first = (tmp_path / "destroys.txt").read_text()
    smoke_test.cleanup(ctx)
    assert (tmp_path / "destroys.txt").read_text() == first, "second call must be a no-op"


def test_sigterm_tears_down_instead_of_leaking(tmp_path: Path) -> None:
    """The defect this exists for, exercised through a real signal.

    atexit does not run on SIGTERM -- the process is terminated without
    unwinding -- so before the handler every sandbox of a killed run stayed
    alive. A unit test cannot show that: the behaviour only exists once a signal
    is actually delivered to a real process, so this spawns one.
    """
    recorder = tmp_path / "destroys.txt"
    fake = tmp_path / "yoloai"
    fake.write_text(f'#!/bin/sh\necho "$@" >> {recorder}\nexit 0\n')
    fake.chmod(0o755)
    (tmp_path / "scratch").mkdir(exist_ok=True)

    script = textwrap.dedent(f"""
        import sys, time
        sys.path.insert(0, {str(_REPO / "scripts")!r})
        import smoke_test
        from pathlib import Path
        ctx = smoke_test.RunContext(
            yoloai_bin={str(fake)!r},
            tmpdir=Path({str(tmp_path)!r}) / "scratch",
            log_dir=Path({str(tmp_path)!r}),
            run_id="ysmk-test",
            fixture_dir=Path({str(tmp_path)!r}),
        )
        ctx.sandboxes = ["ysmk-test-doomed"]
        smoke_test._install_teardown_signals(ctx)
        print("ready", flush=True)
        time.sleep(60)
    """)
    proc = subprocess.Popen(
        [sys.executable, "-c", script], stdout=subprocess.PIPE, text=True
    )
    try:
        assert proc.stdout is not None
        assert proc.stdout.readline().strip() == "ready"
        proc.send_signal(signal.SIGTERM)
        proc.wait(timeout=30)
    finally:
        if proc.poll() is None:
            proc.kill()

    assert recorder.exists(), "SIGTERM left the sandbox alive — nothing was destroyed"
    assert "ysmk-test-doomed" in recorder.read_text()
    assert proc.returncode == -signal.SIGTERM, (
        "the process must still exit as signalled, not report a tidy status it did not have"
    )
