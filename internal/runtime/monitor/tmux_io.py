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

import os
import shutil
import subprocess
import time
from typing import Any, Callable

Runner = Callable[..., "subprocess.CompletedProcess[str]"]

_runner: Runner = subprocess.run

# Well-known locations checked when tmux is not found on PATH (Homebrew on
# Apple Silicon, Homebrew on Intel, system).
_TMUX_FALLBACK_PATHS = ("/opt/homebrew/bin/tmux", "/usr/local/bin/tmux", "/usr/bin/tmux")

# Retry budget for resolving tmux. On Tart macOS VMs the security-scan storm
# triggered by `xcodebuild -runFirstLaunch` transiently hides tmux from BOTH
# shutil.which (PATH lookup) and os.path.isfile (direct stat) for several
# seconds — long enough that a single probe at process start can miss a tmux
# that is genuinely installed. We re-probe with a short backoff before giving
# up. The happy path resolves on the first attempt and never sleeps.
_RESOLVE_ATTEMPTS = 30
_RESOLVE_DELAY_SECONDS = 1.0

# Cached absolute path, set on the first successful probe. The literal "tmux"
# fallback is never cached, so a transient miss cannot poison later calls.
_cached_tmux_bin: str | None = None


def _probe_tmux_bin() -> str | None:
    """Single resolution attempt: return tmux's absolute path, or None if it is
    not currently visible on PATH or at any known fallback location."""
    found = shutil.which("tmux")
    if found:
        return found
    for p in _TMUX_FALLBACK_PATHS:
        if os.path.isfile(p):
            return p
    return None


def tmux_bin() -> str:
    """Resolve the absolute path to the tmux binary, with bounded retry.

    Resolution is deferred to call time (not import time) and retried through
    the transient post-`xcodebuild -runFirstLaunch` window on Tart VMs, where
    both PATH lookup and direct stat for tmux briefly fail even though tmux is
    installed. The first successful probe is cached. If the retry budget is
    exhausted, returns the literal "tmux" so subprocess raises a clear
    FileNotFoundError rather than blocking forever.
    """
    global _cached_tmux_bin
    if _cached_tmux_bin is not None:
        return _cached_tmux_bin
    for attempt in range(_RESOLVE_ATTEMPTS):
        found = _probe_tmux_bin()
        if found:
            _cached_tmux_bin = found
            return found
        if attempt < _RESOLVE_ATTEMPTS - 1:
            time.sleep(_RESOLVE_DELAY_SECONDS)
    return "tmux"


def set_tmux_bin(path: str) -> None:
    """Force the cached tmux path. Tests use this to avoid real resolution
    (and its retry sleeps) on machines where tmux may not be installed."""
    global _cached_tmux_bin
    _cached_tmux_bin = path


def reset_tmux_bin() -> None:
    """Clear the cached tmux path (test teardown)."""
    global _cached_tmux_bin
    _cached_tmux_bin = None


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
    cmd: list[str] = [tmux_bin()]
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
