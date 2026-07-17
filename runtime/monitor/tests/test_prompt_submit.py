# ABOUTME: Tests for deliver_prompt's closed-loop submit — the confirm-and-retry
# ABOUTME: that keeps a swallowed Enter from parking the agent on an unsent prompt.
"""Tests for prompt submission confirmation (DF13).

The failure these guard is quiet: the TUI takes the pasted text into its input
box but never acts on the Enter, so the agent sits on a fully composed prompt it
will never run. Open-loop delivery cannot see that — the text is on screen
either way — and the only downstream signal was the caller's 90s timeout. These
drive the pane through the injectable tmux runner, so the stuck box is built
deliberately rather than waited for.
"""

from __future__ import annotations

import subprocess
from pathlib import Path
from typing import Any, Iterator

import pytest

import tmux_io
from conftest import load_sandbox_setup

sandbox_setup = load_sandbox_setup()

PROMPT = "Run this shell command exactly as written:\necho smoke > output.txt"
CFG: dict[str, Any] = {"submit_sequence": "Enter", "ready_pattern": "❯"}

# The prompt still composed in the live input box: the last ready_pattern line
# carries it. This is the stuck state, verbatim in shape from the DF13 autopsy.
PANE_STUCK = "\n".join([
    "─" * 40,
    "❯ Run this shell command exactly as written:",
    "  echo smoke > output.txt",
    "─" * 40,
])
# Submitted: the text moved up into the transcript and the input box is empty.
# Note the prompt text is STILL on screen — which is exactly why a naive
# "is the text visible" check cannot tell these two apart.
PANE_SENT = "\n".join([
    "  ❯ Run this shell command exactly as written:",
    "    echo smoke > output.txt",
    "─" * 40,
    "❯ ",
    "─" * 40,
])


def _ok(stdout: str = "") -> "subprocess.CompletedProcess[str]":
    return subprocess.CompletedProcess(args=[], returncode=0, stdout=stdout, stderr="")


class PaneFake:
    """Injectable tmux runner: records calls and serves scripted panes."""

    def __init__(self, panes: list[str]) -> None:
        self.panes = panes
        self.calls: list[list[str]] = []

    def __call__(self, cmd: list[str], **kwargs: Any) -> "subprocess.CompletedProcess[str]":
        self.calls.append(cmd)
        if "capture-pane" in cmd:
            # Serve each scripted pane once, then repeat the last.
            pane = self.panes[0] if len(self.panes) == 1 else self.panes.pop(0)
            return _ok(pane)
        return _ok()

    def submits(self) -> int:
        return sum(1 for c in self.calls if "send-keys" in c and "Enter" in c)


@pytest.fixture(autouse=True)
def _fast(monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    monkeypatch.setattr(sandbox_setup.time, "sleep", lambda _s: None)
    tmux_io.set_tmux_bin("tmux")
    yield
    tmux_io.reset_runner()
    tmux_io.reset_tmux_bin()


def _deliver(fake: PaneFake, tmp_path: Path) -> bool:
    (tmp_path / "prompt.txt").write_text(PROMPT)
    tmux_io.set_runner(fake)
    # sandbox-setup.py is importlib-loaded, so it is untyped to mypy: bool() is
    # the assertion that deliver_prompt's contract still holds, not decoration.
    return bool(sandbox_setup.deliver_prompt(CFG, str(tmp_path)))


# --- prompt_pending_in_input ---


def test_pending_is_true_when_prompt_sits_in_the_input_box() -> None:
    tmux_io.set_runner(PaneFake([PANE_STUCK]))
    assert sandbox_setup.prompt_pending_in_input(CFG, PROMPT) is True


def test_pending_is_false_once_the_prompt_moved_into_the_transcript() -> None:
    # The prompt text is still on the pane here; only its position changed.
    tmux_io.set_runner(PaneFake([PANE_SENT]))
    assert sandbox_setup.prompt_pending_in_input(CFG, PROMPT) is False


def test_pending_is_false_without_a_ready_pattern_to_locate_the_box() -> None:
    # No marker → the box can't be found → we must not guess and re-send a key.
    tmux_io.set_runner(PaneFake([PANE_STUCK]))
    assert sandbox_setup.prompt_pending_in_input({"ready_pattern": ""}, PROMPT) is False


def test_pending_is_false_when_the_pane_cannot_be_read() -> None:
    tmux_io.set_runner(lambda cmd, **kw: subprocess.CompletedProcess(args=[], returncode=1, stdout="", stderr="boom"))
    assert sandbox_setup.prompt_pending_in_input(CFG, PROMPT) is False


# --- deliver_prompt closed loop ---


def test_submit_is_sent_once_when_it_takes(tmp_path: Path) -> None:
    fake = PaneFake([PANE_SENT])
    assert _deliver(fake, tmp_path) is True
    assert fake.submits() == 1, "a submit that worked must not be re-sent"


def test_swallowed_submit_is_resent_until_it_takes(tmp_path: Path) -> None:
    # Stuck on the first check, then the retry lands.
    fake = PaneFake([PANE_STUCK, PANE_SENT])
    assert _deliver(fake, tmp_path) is True
    assert fake.submits() == 2, "a swallowed submit must be re-sent exactly once"


def test_retries_are_bounded_when_the_prompt_never_submits(tmp_path: Path) -> None:
    fake = PaneFake([PANE_STUCK])  # never recovers
    assert _deliver(fake, tmp_path) is True
    assert fake.submits() == 1 + sandbox_setup._SUBMIT_VERIFY_ATTEMPTS, \
        "a permanently stuck box must not retry forever"
