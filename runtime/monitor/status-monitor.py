#!/usr/bin/env python3
"""yoloAI in-container status monitor.

Runs as a background process inside the sandbox. Polls detectors in priority
order to determine agent idle/active status, writes results to status.json,
and updates the tmux window title.

Usage: status-monitor.py /path/to/config.json /path/to/status.json
"""

import json
import os
import platform
import re
import subprocess
import sys
import time
from pathlib import Path

# --- Constants ---

POLL_INTERVAL = 2  # seconds between detector polls
MEDIUM_STABILITY = 2  # consecutive matches for medium confidence
LOW_STABILITY = 3  # consecutive matches for low confidence
HOOK_IDLE_AGE = 15  # seconds of no "active" hook write before inferring idle
HOOK_IDLE_GRACE = 8  # seconds idle must persist before HookDetector reports idle
GLOBAL_HOLD_CYCLES = 2  # consecutive non-idle cycles needed to leave idle

# Wait channels indicating terminal input wait (idle)
IDLE_WCHANS = {"n_tty_read", "wait_woken", "ttyin"}

# Wait channels indicating event loop (needs network check)
EVENT_LOOP_WCHANS = {
    "do_epoll_wait", "poll_schedule_timeout",  # Linux
    "select", "kqueue", "pselect",  # macOS
}

# Wait channels indicating active work
ACTIVE_WCHANS = {"do_wait", "wait", "sbwait"}

# ANSI escape sequence pattern for stripping from log output
ANSI_RE = re.compile(r"\x1b\[[0-9;]*[a-zA-Z]|\x1b\].*?\x07|\x1b\[.*?[@-~]")


# --- Platform detection ---

IS_LINUX = os.path.exists("/proc/1/wchan")
IS_MACOS = platform.system() == "Darwin"


# --- Utility functions ---

def tmux_cmd(args, tmux_sock=None):
    """Run a tmux command and return stdout, or empty string on failure."""
    cmd = ["tmux"]
    if tmux_sock:
        cmd.extend(["-S", tmux_sock])
    cmd.extend(args)
    try:
        return subprocess.check_output(cmd, text=True, timeout=5, stderr=subprocess.DEVNULL)
    except (subprocess.SubprocessError, OSError):
        return ""


def write_status(status_file, status, exit_code=None):
    """Write status JSON in-place.

    Writes directly to the status file rather than using atomic rename, because
    status.json is a file-level bind mount in Docker. os.replace() fails with
    EBUSY on bind-mounted files, so we truncate-and-write instead. This is safe
    because we're the only structured writer (hooks use shell redirection) and
    a partial read by the host would just fail JSON parsing and trigger the
    exec fallback.

    Sets source="monitor" so the HookDetector can distinguish monitor writes
    from hook writes (which don't set source).
    """
    data = {
        "status": status,
        "exit_code": exit_code,
        "timestamp": int(time.time()),
        "source": "monitor",
    }
    try:
        with open(status_file, "w") as f:
            json.dump(data, f)
            f.write("\n")
    except OSError:
        pass


def set_title(name, tmux_sock=None):
    """Set tmux window title."""
    tmux_cmd(["rename-window", "-t", "main", name], tmux_sock)


# --- Wchan detector ---

def read_wchan_linux(pid):
    """Read /proc/PID/wchan on Linux."""
    try:
        return Path(f"/proc/{pid}/wchan").read_text().strip()
    except OSError:
        return "unknown"


def read_wchan_macos(pid):
    """Read wait channel via ps on macOS."""
    try:
        out = subprocess.check_output(
            ["ps", "-o", "wchan=", "-p", str(pid)],
            text=True, timeout=5, stderr=subprocess.DEVNULL,
        ).strip()
        return out if out else "unknown"
    except (subprocess.SubprocessError, OSError):
        return "unknown"


def read_wchan(pid):
    """Read wait channel, platform-dispatched."""
    if IS_LINUX:
        return read_wchan_linux(pid)
    if IS_MACOS:
        return read_wchan_macos(pid)
    return "unknown"


def has_active_connections_linux(pid):
    """Check for ESTABLISHED TCP connections via /proc/net/tcp6.

    Uses the network namespace approach: /proc/<pid>/net/tcp6 covers all
    processes in the container's network namespace. Connection state 01 =
    ESTABLISHED.
    """
    try:
        data = Path(f"/proc/{pid}/net/tcp6").read_text()
    except OSError:
        return False
    for line in data.splitlines()[1:]:  # skip header
        fields = line.split()
        if len(fields) >= 4 and fields[3] == "01":
            return True
    return False


def has_active_connections_macos(pid):
    """Check for ESTABLISHED TCP connections via lsof on macOS."""
    try:
        out = subprocess.check_output(
            ["lsof", "-i", "TCP", "-p", str(pid), "-sTCP:ESTABLISHED", "-Fn"],
            text=True, timeout=5, stderr=subprocess.DEVNULL,
        )
        return bool(out.strip())
    except (subprocess.SubprocessError, OSError):
        return False


def has_active_connections(pid):
    """Check for active network connections, platform-dispatched."""
    if IS_LINUX:
        return has_active_connections_linux(pid)
    if IS_MACOS:
        return has_active_connections_macos(pid)
    return False


# --- Detector implementations ---

class DetectorResult:
    """Result from a detector check."""
    __slots__ = ("status", "confidence")

    def __init__(self, status, confidence="high"):
        self.status = status  # "idle", "active", or "unknown"
        self.confidence = confidence  # "high", "medium", "low"

    def __repr__(self):
        return f"DetectorResult({self.status!r}, {self.confidence!r})"


class HookDetector:
    """Reads status from status.json written by agent hooks.

    Returns "idle" when:
    - The file says "idle" (Notification hook fired) AND idle has persisted
      for at least HOOK_IDLE_GRACE seconds. The grace period filters out
      brief idle blips between tool calls in multi-tool sequences — the
      Notification hook fires after every assistant response, not just when
      Claude is truly waiting for user input.
    - The file says "active" but the write is stale (age > HOOK_IDLE_AGE).
      A stale "active" means PreToolUse hasn't fired recently, implying the
      agent stopped working. This provides idle detection even when the
      Notification hook fails to fire (a known upstream issue).

    Returns "active" when:
    - The file says "active" and the last hook write is recent (< HOOK_IDLE_AGE).
      This prevents lower-priority detectors (e.g. wchan) from reporting
      spurious idle during brief gaps between tool calls.

    The grace period is started only by actual hook writes (no "source"
    field), not by the monitor's own echoed writes. An "active" hook write
    (PreToolUse) immediately clears the grace timer.
    """
    name = "hook"
    confidence = "high"

    def __init__(self, status_file):
        self.status_file = status_file
        self._hook_ts = 0  # last timestamp written by a hook (not by monitor)
        self._idle_since = 0  # monotonic time when idle was first seen from hook

    def check(self, _agent_pid):
        try:
            with open(self.status_file) as f:
                data = json.load(f)
            s = data.get("status", "")
            ts = data.get("timestamp", 0)
            source = data.get("source", "")
            now = int(time.time())
            age = now - ts if ts else -1

            if s == "idle":
                # Only start/continue the grace period from actual hook writes.
                # Monitor writes (source="monitor") echo the current state and
                # should not reset or extend the grace timer.
                if not source:
                    self._hook_ts = ts
                    if not self._idle_since:
                        self._idle_since = time.monotonic()

                if self._idle_since:
                    elapsed = time.monotonic() - self._idle_since
                    if elapsed >= HOOK_IDLE_GRACE:
                        debug(f"  hook: idle confirmed (grace {elapsed:.1f}s >= {HOOK_IDLE_GRACE}s)")
                        return DetectorResult("idle", self.confidence)
                    debug(f"  hook: idle grace period ({elapsed:.1f}s/{HOOK_IDLE_GRACE}s)")
                    return DetectorResult("unknown")

                debug(f"  hook: file says idle (age={age}s) but source={source!r}, waiting")
                return DetectorResult("unknown")

            if s == "active":
                # Active signal clears the idle grace period.
                self._idle_since = 0

                # Only update _hook_ts from actual hook writes (no "source"
                # field). The monitor sets source="monitor" on its writes.
                if not source and ts > self._hook_ts:
                    self._hook_ts = ts

                hook_age = now - self._hook_ts if self._hook_ts else -1

                if self._hook_ts and hook_age >= HOOK_IDLE_AGE:
                    debug(f"  hook: last hook write {hook_age}s ago (>{HOOK_IDLE_AGE}s) -> idle")
                    return DetectorResult("idle", self.confidence)

                # Recent hook write says active — report it so lower-priority
                # detectors (e.g. wchan) can't override with a spurious idle
                # during brief gaps between tool calls.
                if self._hook_ts and hook_age >= 0:
                    debug(f"  hook: active (hook_age={hook_age}s)")
                    return DetectorResult("active", self.confidence)

                debug(f"  hook: file says 'active' (hook_age={hook_age}s), waiting")
                return DetectorResult("unknown")

            debug(f"  hook: file says {s!r} (age={age}s), ignoring")
        except (OSError, json.JSONDecodeError, ValueError) as e:
            debug(f"  hook: read error: {e}")
        return DetectorResult("unknown")


class WchanDetector:
    """Checks kernel wait channel for the agent process."""
    name = "wchan"
    confidence = "high"

    def __init__(self):
        self._prev_result = None  # last non-unknown DetectorResult

    def check(self, agent_pid):
        wchan = read_wchan(agent_pid)

        # "0" means the process is on-CPU (not blocked). This is transient —
        # it doesn't mean the agent started or stopped working. Return the
        # previous result to avoid flapping.
        if wchan == "0":
            if self._prev_result:
                debug(f"  wchan: 0 -> reusing previous ({self._prev_result.status})")
                return self._prev_result
            debug(f"  wchan: 0 -> unknown (no previous)")
            return DetectorResult("unknown")

        if wchan in IDLE_WCHANS:
            debug(f"  wchan: {wchan} -> idle")
            self._prev_result = DetectorResult("idle", self.confidence)
            return self._prev_result
        if wchan in EVENT_LOOP_WCHANS:
            has_conn = has_active_connections(agent_pid)
            if has_conn:
                debug(f"  wchan: {wchan} + active connections -> unknown")
                # Ambiguous: could be active API call or just keepalive
                # connections (common with Node.js agents like Claude Code).
                # Return unknown to let lower-priority detectors decide.
                return DetectorResult("unknown")
            debug(f"  wchan: {wchan} + no connections -> idle")
            self._prev_result = DetectorResult("idle", self.confidence)
            return self._prev_result
        if wchan in ACTIVE_WCHANS:
            debug(f"  wchan: {wchan} -> active")
            self._prev_result = DetectorResult("active", self.confidence)
            return self._prev_result
        debug(f"  wchan: {wchan} -> unknown (unrecognized)")
        return DetectorResult("unknown")


class ReadyPatternDetector:
    """Checks tmux pane content for the agent's ready pattern."""
    name = "ready_pattern"
    confidence = "medium"

    def __init__(self, pattern, tmux_sock=None):
        self.pattern = pattern
        self.tmux_sock = tmux_sock

    def check(self, _agent_pid):
        content = tmux_cmd(["capture-pane", "-t", "main", "-p"], self.tmux_sock)
        if not content:
            return DetectorResult("unknown")
        # Check bottom 5 non-empty lines for the pattern. The agent's ready
        # prompt is near the bottom but may not be the very last line — TUI
        # agents like Claude Code show status bars, hints, or other chrome
        # below the input prompt.
        lines = [l for l in content.splitlines() if l.strip()]
        for line in lines[-5:]:
            if self.pattern in line:
                return DetectorResult("idle", self.confidence)
        return DetectorResult("unknown")


class ContextSignalDetector:
    """Monitors pipe-pane log for [YOLOAI:IDLE]/[YOLOAI:WORKING] markers."""
    name = "context_signal"
    confidence = "medium"

    def __init__(self, log_path):
        self.log_path = log_path
        self.last_pos = 0
        self.last_signal = None
        # Seek to end of file at startup
        try:
            self.last_pos = os.path.getsize(log_path)
        except OSError:
            pass

    def check(self, _agent_pid):
        try:
            size = os.path.getsize(self.log_path)
            if size <= self.last_pos:
                # No new data, return last known signal
                if self.last_signal:
                    return DetectorResult(self.last_signal, self.confidence)
                return DetectorResult("unknown")
            with open(self.log_path) as f:
                f.seek(self.last_pos)
                new_data = f.read()
            self.last_pos = size
        except OSError:
            return DetectorResult("unknown")

        # Strip ANSI codes and search for markers
        clean = ANSI_RE.sub("", new_data)
        # Find the last marker in the new data
        idle_pos = clean.rfind("[YOLOAI:IDLE]")
        working_pos = clean.rfind("[YOLOAI:WORKING]")
        if idle_pos > working_pos:
            self.last_signal = "idle"
        elif working_pos > idle_pos:
            self.last_signal = "active"

        if self.last_signal:
            return DetectorResult(self.last_signal, self.confidence)
        return DetectorResult("unknown")


class OutputStabilityDetector:
    """Detects idle by checking if tmux pane content is unchanged."""
    name = "output_stability"
    confidence = "low"

    def __init__(self, tmux_sock=None):
        self.tmux_sock = tmux_sock
        self.prev_content = None

    def check(self, _agent_pid):
        content = tmux_cmd(["capture-pane", "-t", "main", "-p"], self.tmux_sock)
        if not content:
            return DetectorResult("unknown")
        # Normalize: strip trailing whitespace per line and remove trailing
        # blank lines. This prevents cursor position changes and minor tmux
        # capture variations from resetting the stability counter.
        normalized = "\n".join(
            l.rstrip() for l in content.rstrip("\n").splitlines()
        )
        if normalized == self.prev_content:
            return DetectorResult("idle", self.confidence)
        self.prev_content = normalized
        return DetectorResult("unknown")


# --- Detector framework ---

STABILITY_THRESHOLDS = {
    "high": 1,
    "medium": MEDIUM_STABILITY,
    "low": LOW_STABILITY,
}


def build_detectors(config, status_file, tmux_sock=None, yoloai_dir=None):
    """Instantiate detectors based on runtime-config.json detector list."""
    detector_names = config.get("detectors", [])
    idle = config.get("idle", {})
    log_path = os.path.join(yoloai_dir, "log.txt") if yoloai_dir else "/yoloai/log.txt"
    detectors = []

    for name in detector_names:
        if name == "hook":
            detectors.append(HookDetector(status_file))
        elif name == "wchan":
            detectors.append(WchanDetector())
        elif name == "ready_pattern":
            pattern = idle.get("ReadyPattern", "")
            if pattern:
                detectors.append(ReadyPatternDetector(pattern, tmux_sock))
        elif name == "context_signal":
            detectors.append(ContextSignalDetector(log_path))
        elif name == "output_stability":
            detectors.append(OutputStabilityDetector(tmux_sock))

    return detectors


def get_agent_pid(tmux_sock=None):
    """Get the PID of the agent process running in the tmux pane."""
    output = tmux_cmd(["list-panes", "-t", "main", "-F", "#{pane_pid}"], tmux_sock)
    pid_str = output.strip()
    if pid_str:
        try:
            return int(pid_str)
        except ValueError:
            pass
    return None


def check_pane_dead(tmux_sock=None):
    """Check if the tmux pane has exited. Returns (dead, exit_code) or (False, None)."""
    output = tmux_cmd(
        ["list-panes", "-t", "main", "-F", "#{pane_dead}|#{pane_dead_status}"],
        tmux_sock,
    )
    if not output.strip():
        return True, 1  # tmux error = assume dead
    parts = output.strip().split("|", 1)
    if len(parts) < 2:
        return False, None
    if parts[0] == "1":
        try:
            return True, int(parts[1])
        except ValueError:
            return True, 1
    return False, None


DEBUG_LOG = None  # file handle for debug logging, set by run_monitor


def debug(msg):
    """Write a debug message to the monitor log file if debug mode is enabled."""
    if DEBUG_LOG is None:
        return
    try:
        DEBUG_LOG.write(f"[{time.strftime('%H:%M:%S')}] {msg}\n")
        DEBUG_LOG.flush()
    except OSError:
        pass


def run_monitor(config_path, status_file, tmux_sock=None):
    """Main monitor loop."""
    global DEBUG_LOG

    with open(config_path) as f:
        config = json.load(f)

    # Derive yoloai_dir from config path (e.g. /yoloai/runtime-config.json → /yoloai)
    yoloai_dir = os.path.dirname(os.path.abspath(config_path))

    # Enable debug logging if config.debug is set or YOLOAI_MONITOR_DEBUG env var
    if config.get("debug", False) or os.environ.get("YOLOAI_MONITOR_DEBUG"):
        try:
            DEBUG_LOG = open(os.path.join(yoloai_dir, "monitor.log"), "a")
        except OSError:
            pass

    sandbox_name = config.get("sandbox_name", "sandbox")
    detectors = build_detectors(config, status_file, tmux_sock, yoloai_dir)

    debug(f"monitor started: sandbox={sandbox_name} detectors={[d.name for d in detectors]}")
    debug(f"platform: linux={IS_LINUX} macos={IS_MACOS}")

    # Per-detector stability counters: {detector_name: (last_status, count)}
    stability = {}
    # Global hold: when idle, require GLOBAL_HOLD_CYCLES consecutive non-idle
    # decisions before transitioning to active. Prevents brief sensor gaps
    # (e.g. wchan "0" blip) from causing idle->active->idle flaps.
    hold_status = None  # last written status
    hold_active_count = 0  # consecutive cycles wanting to leave idle

    prev_title = ""

    def update_title(title):
        nonlocal prev_title
        if title != prev_title:
            set_title(title, tmux_sock)
            prev_title = title

    while True:
        # 1. Check pane death
        dead, exit_code = check_pane_dead(tmux_sock)
        if dead:
            ec = exit_code if exit_code is not None else 1
            debug(f"pane dead: exit_code={ec}")
            write_status(status_file, "done", ec)
            update_title(sandbox_name)
            break

        # 2. Get agent PID
        agent_pid = get_agent_pid(tmux_sock)

        # 3. Evaluate detectors in order
        final_status = "active"  # safe default
        decided_by = None
        detector_results = []
        for det in detectors:
            result = det.check(agent_pid)
            if result.status == "unknown":
                detector_results.append(f"{det.name}=unknown")
                continue

            threshold = STABILITY_THRESHOLDS.get(result.confidence, 1)
            key = det.name
            prev_status, count = stability.get(key, (None, 0))

            if result.status == prev_status:
                count += 1
            else:
                count = 1
            stability[key] = (result.status, count)

            detector_results.append(
                f"{det.name}={result.status}({result.confidence} {count}/{threshold})"
            )

            if count >= threshold:
                final_status = result.status
                decided_by = det.name
                break

        # 3b. Apply global hold: don't leave idle on a single default cycle
        if hold_status == "idle" and final_status != "idle":
            hold_active_count += 1
            if hold_active_count < GLOBAL_HOLD_CYCLES:
                debug(
                    f"pid={agent_pid} [{' '.join(detector_results)}] -> {final_status}"
                    + (f" (by {decided_by})" if decided_by else " (default)")
                    + f" [HELD idle, {hold_active_count}/{GLOBAL_HOLD_CYCLES}]"
                )
                time.sleep(POLL_INTERVAL)
                continue
        else:
            hold_active_count = 0

        debug(
            f"pid={agent_pid} [{' '.join(detector_results)}] -> {final_status}"
            + (f" (by {decided_by})" if decided_by else " (default)")
        )

        # 4. Write status
        hold_status = final_status
        write_status(status_file, final_status)

        # 5. Update title
        if final_status == "idle":
            update_title(f"> {sandbox_name}")
        else:
            update_title(sandbox_name)

        time.sleep(POLL_INTERVAL)


def main():
    if len(sys.argv) < 3:
        print(f"Usage: {sys.argv[0]} CONFIG_PATH STATUS_FILE [TMUX_SOCK]", file=sys.stderr)
        sys.exit(1)

    config_path = sys.argv[1]
    status_file = sys.argv[2]
    tmux_sock = sys.argv[3] if len(sys.argv) > 3 else None

    run_monitor(config_path, status_file, tmux_sock)


if __name__ == "__main__":
    main()
