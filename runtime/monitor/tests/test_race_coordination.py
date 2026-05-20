# ABOUTME: Race-coordination tests for sandbox-setup.py threading. Use the
# ABOUTME: sequenced-log pattern: capture call order via a fake tmux_io runner.
"""Race-coordination tests for sandbox-setup.py.

These exercise the parts of sandbox-setup.py that coordinate the main
agent-launch thread with the background lifecycle thread (the code that
the commit 5a060b9 race fix lives in). Tests use the sequenced-log
pattern documented in `docs/dev/plans/architecture-remediation.md` (W4):
each fake-tmux call appends `(seq, thread_label, cmd)` to a shared list,
and ordering assertions run after both threads have completed via
`thread.join()`. No `time.sleep` is used as a synchronization primitive.

`tmux_io.set_runner` installs the fake; `reset_runner` restores
`subprocess.run` in tearDown so other tests still see production
behavior.
"""

from __future__ import annotations

import itertools
import subprocess
import threading
import time
from pathlib import Path
from typing import Any, Iterator

import pytest

import tmux_io
from conftest import load_sandbox_setup


# Module-level once: importlib-load sandbox-setup.py. Loading is cheap and
# the module has no global side-effects until main() runs.
sandbox_setup = load_sandbox_setup()


# --- helpers -----------------------------------------------------------


CallEntry = tuple[int, str, tuple[str, ...]]


class CallRecorder:
    """Thread-safe sequenced log of fake-runner invocations.

    Each entry is `(sequence_number, thread_name, cmd_tuple)`. Sequence is
    monotonic across all threads, so ordering assertions can compare
    integers directly without inferring order from list position.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._seq = itertools.count(1)
        self.entries: list[CallEntry] = []

    def runner(self, cmd: list[str], **_kwargs: Any) -> "subprocess.CompletedProcess[str]":
        with self._lock:
            self.entries.append(
                (next(self._seq), threading.current_thread().name, tuple(cmd))
            )
        return subprocess.CompletedProcess(args=cmd, returncode=0, stdout="", stderr="")

    def snapshot(self) -> list[CallEntry]:
        with self._lock:
            return list(self.entries)


@pytest.fixture
def recorder() -> Iterator[CallRecorder]:
    """Install a CallRecorder as the tmux_io runner; reset on teardown."""
    rec = CallRecorder()
    tmux_io.set_runner(rec.runner)
    try:
        yield rec
    finally:
        tmux_io.reset_runner()


def _noop_log(_msg: str) -> None:
    return None


# --- tests -------------------------------------------------------------


def test_lifecycle_banner_orders_after_agent_launch(
    tmp_path: Path, recorder: CallRecorder
) -> None:
    """All launch_agent tmux/subprocess calls precede all lifecycle banner calls.

    This is the W4 demonstration test for the 5a060b9 race fix. Removing
    the `pane_ready.wait()` call in `run_lifecycle_background` makes the
    banner fire concurrently with launch_agent — verified manually by
    deleting that line and observing this assertion fail.

    Sequencing:
      1. Start the lifecycle thread. With the fix in place, it finishes
         `run_lifecycle_commands` (no-op cfg) and blocks on `pane_ready`.
      2. Sleep briefly so the lifecycle thread reaches its block — this
         is observation latency, not a synchronization primitive. Under
         the broken code, this also gives the banner time to fire.
      3. Run launch_agent on its own named thread, join it.
      4. Set pane_ready, join lifecycle.
      5. Assert every "launch_agent" sequence number precedes every
         "lifecycle" sequence number.
    """
    pane_ready = threading.Event()
    cfg: dict[str, Any] = {
        "agent_command": "",
        "agent": "test-agent",
        "model": "test-model",
        "submit_sequence": "Enter",
    }

    lifecycle_thread = threading.Thread(
        name="lifecycle",
        target=sandbox_setup.run_lifecycle_background,
        args=(cfg, str(tmp_path), None, _noop_log, pane_ready),
        daemon=True,
    )
    launch_thread = threading.Thread(
        name="launch_agent",
        target=sandbox_setup.launch_agent,
        kwargs={"cfg": cfg, "socket": None},
    )

    lifecycle_thread.start()
    # Give the lifecycle thread time to reach pane_ready.wait() (or, under
    # the broken code, time to fire its banner tmux calls before we start
    # launch_agent). 100 ms is comfortably more than the no-op
    # run_lifecycle_commands path needs.
    time.sleep(0.1)
    launch_thread.start()
    launch_thread.join(timeout=5)
    assert not launch_thread.is_alive(), "launch_agent thread did not finish"

    pane_ready.set()
    lifecycle_thread.join(timeout=5)
    assert not lifecycle_thread.is_alive(), "lifecycle thread did not finish"

    entries = recorder.snapshot()
    launch_seqs = [seq for seq, name, _ in entries if name == "launch_agent"]
    banner_seqs = [seq for seq, name, _ in entries if name == "lifecycle"]
    assert launch_seqs, f"expected launch_agent calls in log; got {entries}"
    assert banner_seqs, f"expected lifecycle banner calls in log; got {entries}"
    assert max(launch_seqs) < min(banner_seqs), (
        "lifecycle banner call interleaved with launch_agent: "
        f"max(launch_agent seq)={max(launch_seqs)} "
        f"min(lifecycle seq)={min(banner_seqs)} entries={entries}"
    )


def test_lifecycle_commands_run_before_banner_within_thread(
    tmp_path: Path, recorder: CallRecorder
) -> None:
    """Within the lifecycle thread, on_start commands run before the banner.

    Reversing the intra-thread order would notify the agent of completion
    before any setup actually ran. This is a second race-coordination
    test using the sequenced-log pattern, covering an ordering invariant
    inside `run_lifecycle_background` rather than across threads.

    pane_ready is set up-front so the thread doesn't block; the test only
    cares about the ordering of run_lifecycle_commands vs banner writes.
    """
    pane_ready = threading.Event()
    pane_ready.set()
    cfg: dict[str, Any] = {
        "lifecycle": {
            "on_start": [{"type": "string", "cmd": "echo lifecycle-marker"}],
        },
        "submit_sequence": "Enter",
    }

    lifecycle_thread = threading.Thread(
        name="lifecycle",
        target=sandbox_setup.run_lifecycle_background,
        args=(cfg, str(tmp_path), None, _noop_log, pane_ready),
        daemon=True,
    )
    lifecycle_thread.start()
    lifecycle_thread.join(timeout=5)
    assert not lifecycle_thread.is_alive(), "lifecycle thread did not finish"

    entries = recorder.snapshot()
    # The lifecycle on_start command is invoked through tmux_io.run as
    # ["sh", "-c", "echo lifecycle-marker"]. Banner tmux calls have
    # "tmux" as cmd[0].
    cmd_seqs = [
        seq for seq, _, cmd in entries
        if len(cmd) >= 3 and cmd[0] == "sh" and "echo lifecycle-marker" in cmd[2]
    ]
    banner_seqs = [seq for seq, _, cmd in entries if cmd and cmd[0] == "tmux"]
    assert cmd_seqs, f"expected lifecycle on_start command in log; got {entries}"
    assert banner_seqs, f"expected banner tmux calls in log; got {entries}"
    assert max(cmd_seqs) < min(banner_seqs), (
        "lifecycle banner ran before its on_start command: "
        f"max(cmd seq)={max(cmd_seqs)} min(banner seq)={min(banner_seqs)} "
        f"entries={entries}"
    )
