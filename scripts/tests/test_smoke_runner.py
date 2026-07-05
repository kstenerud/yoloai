# ABOUTME: Regression test for Test.run tolerating invalid UTF-8 in captured
# ABOUTME: command output — tmux capture-pane can slice a multibyte char.
"""In --debug mode yoloai embeds a tmux capture-pane snapshot of the agent TUI
in its output. capture-pane renders a fixed-width grid and can split a multibyte
char (Claude Code's box-drawing ─ ● ❯) at the pane edge, leaving a lone 0xe2. A
strict decode of that output raised UnicodeDecodeError and crashed the harness
mid-test (a flaky "invalid continuation byte"); Test.run now decodes leniently.
"""

from __future__ import annotations

from pathlib import Path

import smoke_test


def _ctx(tmp_path: Path, yoloai_bin: str) -> smoke_test.RunContext:
    return smoke_test.RunContext(
        yoloai_bin=yoloai_bin,
        tmpdir=tmp_path,
        log_dir=tmp_path,
        run_id="test",
        fixture_dir=tmp_path,
    )


def test_run_tolerates_invalid_utf8_in_output(tmp_path: Path) -> None:
    # A fake yoloai emitting a lone 0xe2 (a multibyte char sliced by capture-pane)
    # between valid text — exactly the byte sequence that crashed the harness.
    fake = tmp_path / "fake-yoloai"
    fake.write_bytes(b'#!/bin/sh\nprintf "before\\342after"\n')
    fake.chmod(0o755)

    t = smoke_test.Test(_ctx(tmp_path, str(fake)), "decode-check")
    # Must NOT raise UnicodeDecodeError; the bad byte is replaced.
    result = t.run("noop")

    assert result.returncode == 0
    assert "before" in result.stdout
    assert "after" in result.stdout
