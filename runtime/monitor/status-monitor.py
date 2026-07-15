#!/usr/bin/env python3
"""yoloAI in-container status monitor.

Runs as a background process inside the sandbox. Polls detectors in priority
order to determine agent idle/active status, writes results to status.json,
and updates the tmux window title.

Usage: status-monitor.py /path/to/config.json /path/to/status.json
"""

from __future__ import annotations

import datetime
import json
import os
import platform
import re
import subprocess
import sys
import time
from pathlib import Path
from typing import Any, Protocol, TextIO

# --- Constants ---

POLL_INTERVAL = 2  # seconds between detector polls
MEDIUM_STABILITY = 2  # consecutive matches for medium confidence
LOW_STABILITY = 3  # consecutive matches for low confidence
HOOK_IDLE_AGE = 15  # seconds of no "active" hook write before inferring idle
HOOK_IDLE_GRACE = 2  # seconds idle must persist before HookDetector reports idle
# HOOK_IDLE_GRACE was 8s when the Notification hook was used, because Notification
# fires after every assistant response including intermediate ones in multi-tool
# sequences. The Stop hook fires only once per turn (not between tool calls), so
# a 2s grace period is sufficient to absorb any filesystem write latency.
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

def tmux_cmd(args: list[str], tmux_sock: str | None = None) -> str:
    """Run a tmux command and return stdout, or empty string on failure."""
    cmd = ["tmux"]
    if tmux_sock:
        cmd.extend(["-S", tmux_sock])
    cmd.extend(args)
    try:
        return subprocess.check_output(cmd, text=True, timeout=5, stderr=subprocess.DEVNULL)
    except (subprocess.SubprocessError, OSError):
        return ""


def write_status(status_file: str, status: str, exit_code: int | None = None) -> None:
    """Write status JSON in-place.

    Writes directly to the status file rather than using atomic rename, because
    status.json is a file-level bind mount in Docker. os.replace() fails with
    EBUSY on bind-mounted files, so we truncate-and-write instead.

    status.json is purely the monitor's output channel for the host; the
    HookDetector reads hook events from the append-only logs/agent-hooks.jsonl
    log instead, so the monitor never reads back its own writes here.
    """
    # This schema_version must equal agentStatusSchemaVersion in
    # internal/orchestrator/status/status.go (fenced by schema_version_test.go).
    data: dict[str, Any] = {
        "schema_version": 1,
        "status": status,
        "exit_code": exit_code,
        "timestamp": int(time.time()),
    }
    try:
        with open(status_file, "w") as f:
            json.dump(data, f)
            f.write("\n")
    except OSError:
        pass


def read_status_value(status_file: str) -> str:
    """Read the current status string from status_file ("" on any error).

    Used so the respawn idle-seed can tell whether something out-of-band (a
    resume-restart's deliverPrompt writing "active") has already set the status,
    and avoid clobbering it back to idle.
    """
    try:
        with open(status_file) as f:
            result: str = json.load(f).get("status", "")
            return result
    except (OSError, ValueError):
        return ""


def set_title(name: str, tmux_sock: str | None = None) -> None:
    """Set tmux window title."""
    tmux_cmd(["rename-window", "-t", "main", name], tmux_sock)


# --- Wchan detector ---

def read_wchan_linux(pid: int | None) -> str:
    """Read /proc/PID/wchan on Linux."""
    try:
        return Path(f"/proc/{pid}/wchan").read_text().strip()
    except OSError:
        return "unknown"


def read_wchan_macos(pid: int | None) -> str:
    """Read wait channel via ps on macOS."""
    try:
        out = subprocess.check_output(
            ["ps", "-o", "wchan=", "-p", str(pid)],
            text=True, timeout=5, stderr=subprocess.DEVNULL,
        ).strip()
        return out if out else "unknown"
    except (subprocess.SubprocessError, OSError):
        return "unknown"


def read_wchan(pid: int | None) -> str:
    """Read wait channel, platform-dispatched."""
    if IS_LINUX:
        return read_wchan_linux(pid)
    if IS_MACOS:
        return read_wchan_macos(pid)
    return "unknown"


def has_active_connections_linux(pid: int | None) -> bool:
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


def has_active_connections_macos(pid: int | None) -> bool:
    """Check for ESTABLISHED TCP connections via lsof on macOS."""
    try:
        out = subprocess.check_output(
            ["lsof", "-i", "TCP", "-p", str(pid), "-sTCP:ESTABLISHED", "-Fn"],
            text=True, timeout=5, stderr=subprocess.DEVNULL,
        )
        return bool(out.strip())
    except (subprocess.SubprocessError, OSError):
        return False


def has_active_connections(pid: int | None) -> bool:
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

    def __init__(self, status: str, confidence: str = "high") -> None:
        self.status = status  # "idle", "active", or "unknown"
        self.confidence = confidence  # "high", "medium", "low"

    def __repr__(self) -> str:
        return f"DetectorResult({self.status!r}, {self.confidence!r})"


class Detector(Protocol):
    """Structural type for the detector classes below (HookDetector,
    WchanDetector, ReadyPatternDetector, ContextSignalDetector,
    OutputStabilityDetector). They share no base class — this Protocol
    captures the duck-typed interface build_detectors()/run_monitor() rely
    on: a `name` attribute and a `check(pid)` method."""
    name: str

    def check(self, agent_pid: int | None) -> DetectorResult: ...


class HookDetector:
    """Reads agent status from the append-only hook event log.

    The agent's Stop / PreToolUse / UserPromptSubmit hooks each append one
    JSON line to logs/agent-hooks.jsonl ({"event": "hook.idle"|"hook.active",
    "status": "idle"|"active", ...}). Nothing else writes that file, so — unlike
    the monitor's own status.json output — no hook event can be clobbered before
    the detector observes it. This removes the feedback loop that previously
    left the detector unable to confirm idle when the only fresh writes were
    the monitor's own echoes.

    State machine (last appended event wins), driven by the monotonic time at
    which each event is *observed* (avoids depending on the hook's wall clock):
    - last event is hook.idle: report "idle" once it has persisted for
      HOOK_IDLE_GRACE seconds. The Stop hook fires once per turn, so the grace
      only needs to smooth a same-cycle active→idle flip.
    - last event is hook.active observed < HOOK_IDLE_AGE ago: report "active"
      so lower-priority detectors (e.g. wchan) can't flip to a spurious idle
      during brief gaps between tool calls.
    - last event is hook.active observed >= HOOK_IDLE_AGE ago: report "idle".
      A stale "active" means no hook has fired recently, implying the agent
      stopped working even if the Stop hook failed to fire.
    - no events observed yet: "unknown".

    The detector seeks to end-of-file at construction so a monitor restarted by
    stop/start ignores the previous session's events instead of replaying them.
    """
    name = "hook"
    confidence = "high"

    def __init__(self, log_path: str) -> None:
        self.log_path = log_path
        self._state: str | None = None  # "idle" | "active" | None: last observed hook event
        self._idle_since = 0.0  # monotonic time the idle event was first observed
        self._active_since = 0.0  # monotonic time the latest active event was observed
        # Skip a prior session's events: start reading at the current EOF.
        try:
            self._offset = os.path.getsize(log_path)
        except OSError:
            self._offset = 0

    def _consume_new_events(self) -> None:
        """Read newly appended whole lines and fold them into the state."""
        try:
            size = os.path.getsize(self.log_path)
        except OSError:
            return
        if size < self._offset:
            self._offset = 0  # file truncated/rotated — restart from the top
        if size <= self._offset:
            return
        start = self._offset
        try:
            with open(self.log_path, "rb") as f:
                f.seek(start)
                chunk = f.read()
        except OSError:
            return
        # Only consume up to the last newline so a half-written final line
        # (a hook appending concurrently) is re-read whole on the next poll.
        last_nl = chunk.rfind(b"\n")
        if last_nl == -1:
            return
        self._offset = start + last_nl + 1
        now = time.monotonic()
        for raw in chunk[: last_nl + 1].splitlines():
            line = raw.strip()
            if not line:
                continue
            try:
                evt = json.loads(line.decode("utf-8"))
            except (UnicodeDecodeError, json.JSONDecodeError, ValueError):
                continue
            status = evt.get("status", "")
            if status == "idle":
                if self._state != "idle":
                    self._idle_since = now
                self._state = "idle"
            elif status == "active":
                self._state = "active"
                self._active_since = now

    def check(self, _agent_pid: int | None) -> DetectorResult:
        self._consume_new_events()
        if self._state is None:
            return DetectorResult("unknown")

        now = time.monotonic()
        if self._state == "idle":
            elapsed = now - self._idle_since
            if elapsed >= HOOK_IDLE_GRACE:
                debug(f"  hook: idle confirmed (grace {elapsed:.1f}s >= {HOOK_IDLE_GRACE}s)")
                return DetectorResult("idle", self.confidence)
            debug(f"  hook: idle grace period ({elapsed:.1f}s/{HOOK_IDLE_GRACE}s)")
            return DetectorResult("unknown")

        active_age = now - self._active_since
        if active_age >= HOOK_IDLE_AGE:
            debug(f"  hook: last active hook {active_age:.0f}s ago (>{HOOK_IDLE_AGE}s) -> idle")
            return DetectorResult("idle", self.confidence)
        debug(f"  hook: active (active_age={active_age:.0f}s)")
        return DetectorResult("active", self.confidence)


class WchanDetector:
    """Checks kernel wait channel for the agent process."""
    name = "wchan"
    confidence = "high"

    def __init__(self) -> None:
        self._prev_result: DetectorResult | None = None  # last non-unknown DetectorResult

    def check(self, agent_pid: int | None) -> DetectorResult:
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

    def __init__(self, pattern: str, tmux_sock: str | None = None) -> None:
        self.pattern = pattern
        self.tmux_sock = tmux_sock

    def check(self, _agent_pid: int | None) -> DetectorResult:
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

    def __init__(self, log_path: str) -> None:
        self.log_path = log_path
        self.last_pos = 0
        self.last_signal: str | None = None
        # Seek to end of file at startup
        try:
            self.last_pos = os.path.getsize(log_path)
        except OSError:
            pass

    def check(self, _agent_pid: int | None) -> DetectorResult:
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

    def __init__(self, tmux_sock: str | None = None) -> None:
        self.tmux_sock = tmux_sock
        self.prev_content: str | None = None

    def check(self, _agent_pid: int | None) -> DetectorResult:
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


def build_detectors(
    config: dict[str, Any], tmux_sock: str | None = None, yoloai_dir: str | None = None
) -> list[Detector]:
    """Instantiate detectors based on runtime-config.json detector list."""
    detector_names = config.get("detectors", [])
    idle = config.get("idle", {})
    logs_dir = os.path.join(yoloai_dir, "logs") if yoloai_dir else "/yoloai/logs"
    log_path = os.path.join(logs_dir, "agent.log")
    hook_log_path = os.path.join(logs_dir, "agent-hooks.jsonl")
    detectors: list[Detector] = []

    for name in detector_names:
        if name == "hook":
            detectors.append(HookDetector(hook_log_path))
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


def _run(cmd: list[str]) -> str:
    """Run a command and return stripped stdout, or "" on any failure."""
    try:
        return subprocess.check_output(
            cmd, text=True, timeout=5, stderr=subprocess.DEVNULL).strip()
    except (subprocess.SubprocessError, OSError):
        return ""


def proc_is_wrapper(pid: int) -> bool:
    """True if PID is the fall-to-shell wrapper (agent-run.sh, D96).

    Under fall-to-shell the wrapper is the pane process and runs the agent as a
    CHILD (so it can regain the pane and write `done` on agent exit). The
    process-based detectors (wchan) must therefore inspect the child, not the
    wrapper — the wrapper sits in `do_wait` waiting for the child, which
    ACTIVE_WCHANS would misread as a permanently-active agent.
    """
    if IS_LINUX:
        try:
            cmdline = Path(f"/proc/{pid}/cmdline").read_text()
        except OSError:
            return False
        return "agent-run.sh" in cmdline
    return "agent-run.sh" in _run(["ps", "-o", "command=", "-p", str(pid)])


def first_child_pid(pid: int) -> int | None:
    """Return the first child PID of PID, or None."""
    if IS_LINUX:
        try:
            children = Path(f"/proc/{pid}/task/{pid}/children").read_text().split()
        except OSError:
            return None
        candidates = children
    else:
        candidates = _run(["pgrep", "-P", str(pid)]).split()
    for c in candidates:
        try:
            return int(c)
        except ValueError:
            continue
    return None


def get_agent_pid(tmux_sock: str | None = None) -> int | None:
    """Get the PID of the agent process running in the tmux pane.

    When the pane process is the fall-to-shell wrapper (agent-run.sh), descend
    one level to the agent it launched as a child, so process-based detectors
    inspect the agent and not the wrapper's `wait()` (D96 Phase 3).
    """
    output = tmux_cmd(["list-panes", "-t", "main", "-F", "#{pane_pid}"], tmux_sock)
    pid_str = output.strip()
    if not pid_str:
        return None
    try:
        pane_pid = int(pid_str)
    except ValueError:
        return None
    if proc_is_wrapper(pane_pid):
        child = first_child_pid(pane_pid)
        if child is not None:
            return child
    return pane_pid


_tmux_fail_count = 0  # consecutive cycles where tmux returned no usable data
_TMUX_FAIL_THRESHOLD = 3  # report death after this many consecutive failures


def check_pane_dead(tmux_sock: str | None = None) -> tuple[bool, int | None]:
    """Check if the tmux pane has exited. Returns (dead, exit_code) or (False, None).

    Handles two failure modes:
    1. tmux unreachable (empty output): retries up to _TMUX_FAIL_THRESHOLD
       cycles, then reports death with exit_code=0.
    2. pane_dead=1 but pane_dead_status empty: on some platforms (Docker
       Desktop macOS), tmux sets pane_dead before reaping the zombie child
       via waitpid, leaving pane_dead_status empty indefinitely. Retries
       up to _TMUX_FAIL_THRESHOLD cycles, then reports death with exit_code=0.
    """
    global _tmux_fail_count
    output = tmux_cmd(
        ["list-panes", "-t", "main", "-F", "#{pane_dead}|#{pane_dead_status}"],
        tmux_sock,
    )
    if not output.strip():
        _tmux_fail_count += 1
        if _tmux_fail_count >= _TMUX_FAIL_THRESHOLD:
            debug(f"tmux unreachable for {_tmux_fail_count} cycles — reporting pane dead")
            return True, 0
        return False, None  # tmux transient error — retry next cycle
    parts = output.strip().split("|", 1)
    if len(parts) < 2:
        _tmux_fail_count += 1
        if _tmux_fail_count >= _TMUX_FAIL_THRESHOLD:
            return True, 0
        return False, None
    if parts[0] == "1":
        try:
            exit_code = int(parts[1])
        except ValueError:
            # pane_dead=1 but status not yet populated (zombie not reaped).
            # Retry a few times; if it persists, assume clean exit.
            _tmux_fail_count += 1
            _log_jsonl("debug", "pane_dead.no_status",
                       "pane dead but status not parseable",
                       raw=output.strip(), fail_count=_tmux_fail_count)
            if _tmux_fail_count >= _TMUX_FAIL_THRESHOLD:
                debug(f"pane dead with empty status for {_tmux_fail_count} cycles — assuming exit 0")
                return True, 0
            return False, None
        _tmux_fail_count = 0
        _log_jsonl("info", "pane_dead.detected",
                   "pane death detected with exit code",
                   raw=output.strip(), exit_code=exit_code)
        return True, exit_code
    _tmux_fail_count = 0  # pane alive — reset counter
    return False, None


_monitor_log: TextIO | None = None  # file handle for logs/monitor.jsonl
_debug_enabled = False  # set by run_monitor based on config


def _log_jsonl(level: str, event: str, msg: str, **fields: Any) -> None:
    """Write a structured JSONL entry to logs/monitor.jsonl."""
    if _monitor_log is None:
        return
    now = datetime.datetime.now(datetime.timezone.utc)
    ts = now.strftime("%Y-%m-%dT%H:%M:%S.") + f"{now.microsecond // 1000:03d}Z"
    entry: dict[str, Any] = {"ts": ts, "level": level, "event": event, "msg": msg}
    entry.update(fields)
    try:
        _monitor_log.write(json.dumps(entry) + "\n")
        _monitor_log.flush()
    except OSError:
        pass


def debug(msg: str) -> None:
    """Write a debug-level entry to monitor.jsonl if debug mode is enabled.

    Accepts a plain string (legacy format) for compatibility with existing callers.
    The message is stored verbatim in the 'msg' field.
    """
    if not _debug_enabled:
        return
    _log_jsonl("debug", "detector.result", msg)


def run_monitor(config_path: str, status_file: str, tmux_sock: str | None = None) -> None:
    """Main monitor loop."""
    global _monitor_log, _debug_enabled

    # RUNTIME_CONFIG_SCHEMA_VERSION must equal sandbox-setup.py's constant and
    # sandbox/create.go's runtimeConfigSchemaVersion. W2 of the architecture
    # remediation plan.
    runtime_config_schema_version = 1

    with open(config_path) as f:
        config = json.load(f)

    got_schema = config.get("schema_version")
    if got_schema is not None and got_schema != runtime_config_schema_version:
        raise RuntimeError(
            f"schema_version mismatch in {config_path}: got {got_schema}, "
            f"expected {runtime_config_schema_version} "
            f"(runtime-config.json was written by an incompatible yoloai version)"
        )

    # Derive yoloai_dir from config path (e.g. /yoloai/runtime-config.json → /yoloai)
    yoloai_dir = os.path.dirname(os.path.abspath(config_path))

    _debug_enabled = config.get("debug", False) or bool(os.environ.get("YOLOAI_MONITOR_DEBUG"))
    monitor_log_path = os.path.join(yoloai_dir, "logs", "monitor.jsonl")
    try:
        _monitor_log = open(monitor_log_path, "a", buffering=1)  # line-buffered
    except OSError:
        pass

    sandbox_name = config.get("sandbox_name", "sandbox")
    # Mode selector (session-layer.md Tier-2). hook-authoritative: the agent's
    # turn hook is the sole active/idle authority (it writes agent-status.json
    # directly); the monitor runs no heuristics for active/idle — only pane-death
    # -> done and a one-shot idle seed on respawn. Absent -> heuristic-only
    # (back-compat for sandboxes created before the selector existed).
    idle_mode = config.get("idle_mode", "heuristic-only")
    hook_authoritative = idle_mode == "hook-authoritative"
    detectors = build_detectors(config, tmux_sock, yoloai_dir)

    detector_names = [d.name for d in detectors]
    _log_jsonl("info", "monitor.start", "monitor started",
               idle_mode=idle_mode,
               detectors=detector_names,
               sandbox=sandbox_name)
    debug(f"platform: linux={IS_LINUX} macos={IS_MACOS}")

    # Per-detector stability counters: {detector_name: (last_status, count)}
    stability: dict[str, tuple[str | None, int]] = {}
    # Global hold: when idle, require GLOBAL_HOLD_CYCLES consecutive non-idle
    # decisions before transitioning to active. Prevents brief sensor gaps
    # (e.g. wchan "0" blip) from causing idle->active->idle flaps.
    hold_status: str | None = None  # last written status
    hold_active_count = 0  # consecutive cycles wanting to leave idle

    prev_title = ""

    def update_title(title: str) -> None:
        nonlocal prev_title
        if title != prev_title:
            set_title(title, tmux_sock)
            prev_title = title

    # The monitor is a DURABLE session component (DF46): it watches the pane for
    # the life of the box, not a single agent run. When the agent exits it records
    # "done" but keeps watching, so an in-place relaunch (respawn-pane) is
    # re-detected and tracked without restarting the monitor. It exits only when
    # the box (and the session-runner that parents it) goes down.
    in_done = False  # latched while the pane is dead, cleared on respawn
    while True:
        # 1. Check pane death
        dead, exit_code = check_pane_dead(tmux_sock)
        if dead:
            if not in_done:
                ec = exit_code if exit_code is not None else 1
                debug(f"pane dead: exit_code={ec}")
                _log_jsonl("info", "status.transition", "status changed",
                           **{"from": hold_status or "unknown", "to": "done", "detector": "pane_dead",
                              "exit_code": ec})
                write_status(status_file, "done", ec)
                update_title(sandbox_name)
                hold_status = "done"
                hold_active_count = 0
                stability = {}
                in_done = True
            time.sleep(POLL_INTERVAL)
            continue

        if hook_authoritative:
            # The agent's hook owns active/idle (it writes agent-status.json on
            # turn-start/turn-stop). The monitor runs NO heuristics here — that
            # is what removes the startup blip. It only seeds "idle" once when a
            # just-respawned agent comes up waiting (the hook flips it to active
            # when the next turn starts). Initial create is seeded by
            # sandbox-setup.py, so no seed is needed on first start.
            if in_done:
                in_done = False
                # Seed "idle" on respawn — but ONLY if nothing has set a fresher
                # status out-of-band. A resume-restart respawns the pane and then
                # synchronously writes "active" via deliverPrompt; seeding idle
                # unconditionally would clobber that back to a stale idle (the
                # very thing the active-before-submit write exists to prevent).
                if read_status_value(status_file) == "done":
                    _log_jsonl("info", "status.transition", "status changed",
                               **{"from": "done", "to": "idle", "detector": "respawn_seed"})
                    write_status(status_file, "idle")
                    update_title(f"> {sandbox_name}")
                    hold_status = "idle"
            time.sleep(POLL_INTERVAL)
            continue

        # Honor a wrapper-written `done` (D96 Phase 3). Under fall-to-shell the
        # wrapper records `done` on agent exit but keeps the pane alive as a
        # shell, so check_pane_dead stays False. We must NOT run the detector
        # stack against that idle shell — it would clobber `done` with
        # `idle`/`active`, masquerading an exited agent as waiting. The on-disk
        # `done` IS the latch: detectors only ever write active/idle, so a `done`
        # with a live pane can only be the wrapper's. yoloai-resume clears it
        # (seeds `idle`) when it relaunches the agent, at which point detection
        # resumes below.
        if read_status_value(status_file) == "done":
            if not in_done:
                in_done = True
                hold_status = "done"
                hold_active_count = 0
                stability = {}
                update_title(sandbox_name)
                debug("wrapper-done latched; detection paused until resume")
            time.sleep(POLL_INTERVAL)
            continue

        # Pane is alive and not in the wrapper-done latch: a new agent has been
        # launched into it (initial launch or respawn). Clear the done latch and
        # fall through to normal active/idle detection.
        in_done = False

        # 2. Get agent PID
        agent_pid = get_agent_pid(tmux_sock)

        # 3. Evaluate detectors in order
        final_status = "active"  # safe default
        decided_by: str | None = None
        detector_results: list[str] = []
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
        if final_status != hold_status:
            _log_jsonl("info", "status.transition", "status changed",
                       **{"from": hold_status or "unknown", "to": final_status,
                          "detector": decided_by or "default"})
        hold_status = final_status
        write_status(status_file, final_status)

        # 5. Update title
        if final_status == "idle":
            update_title(f"> {sandbox_name}")
        else:
            update_title(sandbox_name)

        time.sleep(POLL_INTERVAL)


def write_status_cli(args: list[str]) -> None:
    """Handle `status-monitor.py --write-status STATUS STATUS_FILE [EXIT_CODE]`.

    The fall-to-shell wrapper (agent-run.sh, D96) records the agent's
    authoritative `done` on exit — pane death no longer does it, because the
    wrapper keeps the pane alive as a shell; yoloai-resume seeds `idle` when it
    relaunches the agent (the pane never died, so the monitor's respawn idle-seed
    never fires — yoloai-resume replicates it). Both route through the monitor's
    own write_status so the agent-status.json schema stays single-sourced (fenced
    by schema_version_test.go) instead of duplicated in shell.
    """
    if len(args) < 2:
        print("Usage: status-monitor.py --write-status STATUS STATUS_FILE [EXIT_CODE]", file=sys.stderr)
        sys.exit(2)
    status = args[0]
    status_file = args[1]
    exit_code: int | None = None
    if len(args) > 2:
        try:
            exit_code = int(args[2])
        except ValueError:
            exit_code = 1
    write_status(status_file, status, exit_code)


def main() -> None:
    if len(sys.argv) >= 2 and sys.argv[1] == "--write-status":
        write_status_cli(sys.argv[2:])
        return

    if len(sys.argv) < 3:
        print(f"Usage: {sys.argv[0]} CONFIG_PATH STATUS_FILE [TMUX_SOCK]", file=sys.stderr)
        sys.exit(1)

    config_path = sys.argv[1]
    status_file = sys.argv[2]
    tmux_sock = sys.argv[3] if len(sys.argv) > 3 else None

    run_monitor(config_path, status_file, tmux_sock)


if __name__ == "__main__":
    main()
