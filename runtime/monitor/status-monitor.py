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
    """Write status JSON atomically."""
    data = {
        "status": status,
        "exit_code": exit_code,
        "timestamp": int(time.time()),
    }
    tmp = status_file + ".tmp"
    try:
        with open(tmp, "w") as f:
            json.dump(data, f)
            f.write("\n")
        os.replace(tmp, status_file)
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
    """Reads status from status.json written by agent hooks."""
    name = "hook"
    confidence = "high"

    def __init__(self, status_file):
        self.status_file = status_file

    def check(self, _agent_pid):
        try:
            with open(self.status_file) as f:
                data = json.load(f)
            s = data.get("status", "")
            if s in ("idle", "active"):
                return DetectorResult(s, self.confidence)
        except (OSError, json.JSONDecodeError, ValueError):
            pass
        return DetectorResult("unknown")


class WchanDetector:
    """Checks kernel wait channel for the agent process."""
    name = "wchan"
    confidence = "high"

    def check(self, agent_pid):
        wchan = read_wchan(agent_pid)
        if wchan in IDLE_WCHANS:
            return DetectorResult("idle", self.confidence)
        if wchan in EVENT_LOOP_WCHANS:
            if has_active_connections(agent_pid):
                return DetectorResult("active", self.confidence)
            return DetectorResult("idle", self.confidence)
        if wchan in ACTIVE_WCHANS:
            return DetectorResult("active", self.confidence)
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
        # Check last non-empty line for the pattern to reduce false positives
        lines = [l for l in content.splitlines() if l.strip()]
        if lines and self.pattern in lines[-1]:
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
        if content == self.prev_content:
            return DetectorResult("idle", self.confidence)
        self.prev_content = content
        return DetectorResult("unknown")


# --- Detector framework ---

STABILITY_THRESHOLDS = {
    "high": 1,
    "medium": MEDIUM_STABILITY,
    "low": LOW_STABILITY,
}


def build_detectors(config, status_file, tmux_sock=None):
    """Instantiate detectors based on config.json detector list."""
    detector_names = config.get("detectors", [])
    idle = config.get("idle", {})
    log_path = "/yoloai/log.txt"
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


def run_monitor(config_path, status_file, tmux_sock=None):
    """Main monitor loop."""
    with open(config_path) as f:
        config = json.load(f)

    sandbox_name = config.get("sandbox_name", "sandbox")
    detectors = build_detectors(config, status_file, tmux_sock)

    # Per-detector stability counters: {detector_name: (last_status, count)}
    stability = {}

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
            write_status(status_file, "done", ec)
            update_title(sandbox_name)
            break

        # 2. Get agent PID
        agent_pid = get_agent_pid(tmux_sock)

        # 3. Evaluate detectors in order
        final_status = "active"  # safe default
        for det in detectors:
            result = det.check(agent_pid)
            if result.status == "unknown":
                continue

            threshold = STABILITY_THRESHOLDS.get(result.confidence, 1)
            key = det.name
            prev_status, count = stability.get(key, (None, 0))

            if result.status == prev_status:
                count += 1
            else:
                count = 1
            stability[key] = (result.status, count)

            if count >= threshold:
                final_status = result.status
                break

        # 4. Write status
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
