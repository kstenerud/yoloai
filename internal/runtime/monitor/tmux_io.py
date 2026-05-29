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
#
# A fixed budget is the *fallback*: it can expire mid-storm because the storm
# lasts as long as firstlaunch runs (60-120s+), not a fixed number of seconds.
# When firstlaunch markers are registered (see set_firstlaunch_markers), we
# instead wait for the window to actually close, using this budget only as a
# grace period for the tail. The hard ceiling guards against a firstlaunch
# child that dies without ever signalling completion.
_RESOLVE_ATTEMPTS = 30
_RESOLVE_DELAY_SECONDS = 1.0
_FIRSTLAUNCH_MAX_WAIT_SECONDS = 240.0

# Cached absolute path, set on the first successful probe. The literal "tmux"
# fallback is never cached, so a transient miss cannot poison later calls.
_cached_tmux_bin: str | None = None

# Marker paths that bound the `xcodebuild -runFirstLaunch` window. The Tart
# setup path registers these so tmux resolution can wait out the scan storm
# instead of burning a fixed budget. Unset (None) on every other backend, in
# which case resolution falls back to the bounded retry below.
_firstlaunch_started_marker: str | None = None
_firstlaunch_done_marker: str | None = None


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


def set_firstlaunch_markers(started: str | None, done: str | None) -> None:
    """Register the marker paths that bracket the firstlaunch scan-storm window.

    `started` is created when `xcodebuild -runFirstLaunch` is launched; `done`
    is created when it finishes. While `started` exists and `done` does not,
    tmux resolution treats a missing tmux as transient and keeps waiting."""
    global _firstlaunch_started_marker, _firstlaunch_done_marker
    _firstlaunch_started_marker = started
    _firstlaunch_done_marker = done


def _firstlaunch_in_progress() -> bool:
    """True only while the firstlaunch window is open: the started marker
    exists and the done marker has not yet appeared. Returns False when no
    markers are registered (every non-Tart path)."""
    started = _firstlaunch_started_marker
    if not started or not os.path.exists(started):
        return False
    done = _firstlaunch_done_marker
    if done and os.path.exists(done):
        return False
    return True


def tmux_bin() -> str:
    """Resolve the absolute path to the tmux binary, waiting out the firstlaunch
    scan storm when one is in progress.

    Resolution is deferred to call time (not import time). On Tart VMs the
    `xcodebuild -runFirstLaunch` security-scan storm briefly hides tmux from
    both PATH lookup and direct stat even though tmux is installed. When
    firstlaunch markers are registered (set_firstlaunch_markers) we re-probe
    for as long as the window stays open, capped by a hard ceiling. Once the
    window closes — or when no markers are registered — we fall back to a
    bounded retry that covers the storm's tail. The first successful probe is
    cached. If everything is exhausted, returns the literal "tmux" so
    subprocess raises a clear FileNotFoundError rather than blocking forever.
    """
    global _cached_tmux_bin
    if _cached_tmux_bin is not None:
        return _cached_tmux_bin
    waited = 0.0
    while _firstlaunch_in_progress() and waited < _FIRSTLAUNCH_MAX_WAIT_SECONDS:
        found = _probe_tmux_bin()
        if found:
            _cached_tmux_bin = found
            return found
        time.sleep(_RESOLVE_DELAY_SECONDS)
        waited += _RESOLVE_DELAY_SECONDS
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
    """Clear the cached tmux path and firstlaunch markers (test teardown)."""
    global _cached_tmux_bin, _firstlaunch_started_marker, _firstlaunch_done_marker
    _cached_tmux_bin = None
    _firstlaunch_started_marker = None
    _firstlaunch_done_marker = None


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
