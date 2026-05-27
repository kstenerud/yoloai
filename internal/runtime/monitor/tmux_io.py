# ABOUTME: Injectable tmux/subprocess wrappers for sandbox-side scripts.
# ABOUTME: Tests swap the runner via set_runner to capture call ordering.
"""I/O seams for tmux and subprocess calls.

Sandbox-side scripts (sandbox-setup.py, status-monitor.py) run tmux and
other subprocesses on the agent's behalf. The lifecycle-banner-vs-agent-
launch race fixed in commit 5a060b9 lived in code that couldn't be
exercised without a fake for these calls. W4 of the architecture
remediation plan moves the wrappers here so tests can install a fake
runner via `set_runner(fn)` and assert ordering on the recorded calls.

The module-level `_runner` defaults to `subprocess.run`. Tests call
`set_runner(fake)` in `setUp` and `reset_runner()` in `tearDown` — the
fake is responsible for returning a `subprocess.CompletedProcess`-shaped
object (the only attributes callers read are `returncode`, `stdout`,
`stderr`).
"""

from __future__ import annotations

import subprocess
from typing import Any, Callable

Runner = Callable[..., "subprocess.CompletedProcess[str]"]

_runner: Runner = subprocess.run


def set_runner(fn: Runner) -> None:
    """Swap the underlying runner. Tests use this to intercept tmux/subprocess calls."""
    global _runner
    _runner = fn


def reset_runner() -> None:
    """Restore subprocess.run as the runner (for test teardown)."""
    global _runner
    _runner = subprocess.run


def run(cmd: list[str], **kwargs: Any) -> "subprocess.CompletedProcess[str]":
    """Run a non-tmux subprocess through the injectable runner."""
    return _runner(cmd, **kwargs)


def tmux(*args: str, socket: str | None = None) -> "subprocess.CompletedProcess[str]":
    """Run a tmux command, optionally with a per-sandbox socket."""
    cmd: list[str] = ["tmux"]
    if socket:
        cmd.extend(["-S", socket])
    cmd.extend(args)
    return _runner(cmd, capture_output=True, text=True)


def tmux_output(*args: str, socket: str | None = None) -> str:
    """Run a tmux command and return stdout, or empty string on failure."""
    result = tmux(*args, socket=socket)
    if result.returncode == 0:
        return result.stdout
    return ""


def set_title(title: str, socket: str | None = None) -> None:
    """Set tmux window title."""
    tmux("rename-window", "-t", "main", title, socket=socket)
