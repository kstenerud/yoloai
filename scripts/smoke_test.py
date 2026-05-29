#!/usr/bin/env python3
"""End-to-end smoke tests for yoloai against real agents.

Run with: python3 scripts/smoke_test.py [--full]
Or via:   make smoketest / make smoketest-full

Base tier (default): docker + containerd-vm on Linux, docker + tart on macOS.
Full tier (--full):  all backends including podman, gVisor, vm-enhanced.

Tests that don't need a real agent (files exchange, reset, start-after-done,
overlay) have been moved to Go integration tests (sandbox/integration_test.go,
internal/cli/integration_test.go).

Requires ANTHROPIC_API_KEY and configured backends.
See docs/dev/plans/smoke-test-v2.md for the full design.
"""
from __future__ import annotations

import argparse
import atexit
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable, Optional

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

SENTINEL = "done"  # touched in the exchange dir when the agent finishes its prompt
DEFAULT_TIMEOUT = 90    # seconds: container + agent startup for non-VM backends
VM_TIMEOUT = 180        # seconds: VM boot + agent startup (Firecracker/Tart)
QEMU_TIMEOUT = 300      # seconds: QEMU-based Kata VM — slower boot than Firecracker
CMD_TIMEOUT = 60        # seconds: individual yoloai commands

# Upfront base-image build budgets (prerequisite phase). Container/seatbelt
# builds are local and fast. Tart's first run pulls a ~30 GB macOS VM image,
# which legitimately needs a much larger budget so it completes and the backend
# actually gets tested. A timeout aborts the run (exit 1) rather than skipping
# the backend — a release gate must not pass while silently dropping coverage.
BASE_BUILD_TIMEOUT = 600       # seconds: local image build (docker/podman/seatbelt/containerd)
TART_BASE_BUILD_TIMEOUT = 3600  # seconds: one-time ~30 GB macOS VM image pull

STALL_GRACE_SECS = 30   # ignore stall detection for this many seconds after polling starts
STALL_IDLE_COUNT = 3    # consecutive idle polls before declaring a stall (3×3s = 9s sustained idle)
# Terminal sandbox statuses that mean the agent will never write the sentinel.
STALL_TERMINAL = {"done", "failed", "stopped", "removed", "broken", "unavailable"}


# ---------------------------------------------------------------------------
# Data structures
# ---------------------------------------------------------------------------

@dataclass
class BackendSpec:
    """One entry in the backend test matrix."""

    os: str                 # "linux" or "mac"
    isolation: str          # "container", "container-enhanced", "vm", "vm-enhanced"
    backend: Optional[str]  # "docker", "podman", or None (use yoloai default)
    label: str              # short label used in sandbox names and display
    check_backend: str      # daemon name for `yoloai system check --backend`
    is_vm: bool = False     # True → use VM_TIMEOUT for sentinel polling
    check_isolation: str = ""  # isolation to validate in prereq check (empty = skip)
    sentinel_timeout_override: int = 0  # non-zero overrides the default sentinel timeout
    retries: int = 0        # number of times to retry the test on failure
    stall_grace_secs: int = 0  # non-zero overrides global STALL_GRACE_SECS for this backend

    @property
    def is_seatbelt(self) -> bool:
        """Seatbelt uses the host filesystem; exchange dir is a host path."""
        return self.os == "mac" and self.isolation == "container" and self.backend is None

    def exchange_dir(self, sandbox_name: str) -> str:
        """Return the exchange dir path as seen from inside the sandbox."""
        if self.is_seatbelt:
            return str(Path.home() / ".yoloai" / "sandboxes" / sandbox_name / "files")
        if self.is_vm and self.os == "mac":  # Tart VMs
            # Tart setup creates /Users/admin/.yoloai → /Volumes/My Shared Files/yoloai
            # (virtiofs path has spaces; the symlink is space-free).
            return "/Users/admin/.yoloai/files"
        return "/yoloai/files"

    def sentinel_timeout(self) -> int:
        if self.sentinel_timeout_override:
            return self.sentinel_timeout_override
        return VM_TIMEOUT if self.is_vm else DEFAULT_TIMEOUT

    def new_timeout(self) -> int:
        """Subprocess timeout for the blocking `new`/`restart` calls.

        On VM backends `new` blocks through the full VM clone + boot + guest
        setup before returning (tart routinely ~118s — see run 20260529-041940,
        where the flat 120s ceiling tripped on a slightly slower clone and only
        the retry saved it). VMs need the VM budget; container backends keep the
        fast ceiling."""
        if self.is_vm:
            return self.sentinel_timeout_override or VM_TIMEOUT
        return 120

    def sentinel_stall_grace(self) -> int:
        return self.stall_grace_secs if self.stall_grace_secs else STALL_GRACE_SECS

    def new_args(self) -> list[str]:
        """Return --os / --isolation / --backend flags for `yoloai new`."""
        args = ["--os", self.os, "--isolation", self.isolation]
        if self.backend:
            args += ["--backend", self.backend]
        return args


@dataclass
class PrereqResult:
    spec: BackendSpec
    available: bool
    note: str = ""


@dataclass
class TestResult:
    name: str
    passed: bool = False
    skipped: bool = False
    reason: str = ""
    elapsed_s: float = 0.0
    # Populated by the failure-autopsy pass (Tier 1). autopsy_path points at the
    # per-failure FAILURE.md; fingerprints holds the matched diagnostic labels so
    # print_summary and the run manifest can surface root cause without re-scanning.
    autopsy_path: Optional[str] = None
    fingerprints: list[str] = field(default_factory=list)


class SkipTest(Exception):
    pass


def _xml_escape(s: str) -> str:
    """Escape a string for safe embedding in XML."""
    return (
        s.replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
        .replace("'", "&apos;")
    )


class JUnitWriter:
    """Incrementally writes JUnit XML test results, crash-resilient via atexit."""

    def __init__(self, path: str) -> None:
        self._path = path
        self._f = open(path, "w")  # noqa: SIM115
        self._f.write('<?xml version="1.0" encoding="UTF-8"?>\n')
        self._f.write('<testsuites>\n')
        self._f.write('  <testsuite name="smoke">\n')
        self._closed = False
        atexit.register(self.close)

    def write_testcase(self, result: TestResult) -> None:
        name = _xml_escape(result.name)
        self._f.write(
            f'    <testcase name="{name}" time="{result.elapsed_s:.2f}">\n'
        )
        if result.skipped:
            msg = _xml_escape(result.reason)
            self._f.write(f'      <skipped message="{msg}" />\n')
        elif not result.passed:
            msg = _xml_escape(result.reason)
            self._f.write(f'      <failure message="{msg}">{msg}</failure>\n')
        self._f.write("    </testcase>\n")
        self._f.flush()

    def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        self._f.write("  </testsuite>\n")
        self._f.write("</testsuites>\n")
        self._f.close()


@dataclass
class RunContext:
    yoloai_bin: str
    tmpdir: Path
    log_dir: Path
    run_id: str
    fixture_dir: Path
    full: bool
    debug: bool = False
    test_filter: Optional[list[str]] = None
    backend_filter: Optional[list[str]] = None
    sandboxes: list[str] = field(default_factory=list)
    results: list[TestResult] = field(default_factory=list)
    junit: Optional[JUnitWriter] = None


# ---------------------------------------------------------------------------
# Backend matrices
# ---------------------------------------------------------------------------

# Base tier: fast, reliable backends for PR gates and nightly smoke.
BASE_LINUX_BACKENDS: list[BackendSpec] = [
    BackendSpec("linux", "container",          "docker", "docker",
                check_backend="docker", retries=1),
    BackendSpec("linux", "vm",                 None,     "containerd-vm",
                check_backend="containerd", is_vm=True, check_isolation="vm",
                sentinel_timeout_override=QEMU_TIMEOUT, stall_grace_secs=120,
                retries=1),
]

BASE_MACOS_BACKENDS: list[BackendSpec] = [
    BackendSpec("linux", "container", "docker", "docker",
                check_backend="docker", retries=1),
    BackendSpec("mac",   "vm",        None,     "tart",
                check_backend="tart",   is_vm=True, retries=1, stall_grace_secs=120),
]

# Full tier: all backends for pre-release validation.
FULL_LINUX_BACKENDS: list[BackendSpec] = [
    BackendSpec("linux", "container",          "docker", "docker",
                check_backend="docker", retries=1),
    BackendSpec("linux", "container",          "podman", "podman",
                check_backend="podman"),
    BackendSpec("linux", "container-enhanced", "docker", "docker-cenhanced",
                check_backend="docker"),
    BackendSpec("linux", "vm",                 None,     "containerd-vm",
                check_backend="containerd", is_vm=True, check_isolation="vm",
                sentinel_timeout_override=QEMU_TIMEOUT, stall_grace_secs=120,
                retries=1),
    BackendSpec("linux", "vm-enhanced",        None,     "containerd-vmenhanced",
                check_backend="containerd", is_vm=True, check_isolation="vm-enhanced",
                sentinel_timeout_override=QEMU_TIMEOUT, stall_grace_secs=120,
                retries=1),
]

FULL_MACOS_BACKENDS: list[BackendSpec] = [
    BackendSpec("linux", "container", "docker", "docker",
                check_backend="docker"),
    BackendSpec("linux", "container", "podman", "podman",
                check_backend="podman"),
    # Note: linux+vm isolation requires containerd, which is Linux-only.
    # On macOS, use mac+vm (Tart) instead.
    BackendSpec("mac",   "container", None,     "seatbelt",
                check_backend="seatbelt"),
    BackendSpec("mac",   "vm",        None,     "tart",
                check_backend="tart",   is_vm=True, retries=1, stall_grace_secs=120),
]

# Required for non-matrix tests. Must be available on both platforms.
DEFAULT_BACKEND = BackendSpec(
    "linux", "container", "docker", "docker", check_backend="docker"
)

# Tests restricted to --full tier.
FULL_ONLY_TESTS = {"clone"}

def is_full_test(name: str) -> bool:
    """Return True if this test requires --full."""
    return name.split("/")[0] in FULL_ONLY_TESTS


# ---------------------------------------------------------------------------
# Test helper
# ---------------------------------------------------------------------------

class Test:
    """Encapsulates one test run: log file, sandbox tracking, and assertion helpers."""

    def __init__(self, ctx: RunContext, name: str, attempt: int = 1) -> None:
        self.ctx = ctx
        self.name = name
        self.attempt = attempt
        # Sanitise the name for use as a filename.
        safe = name.replace("/", "-").replace(" ", "_")
        self.log_file = ctx.log_dir / f"{safe}.log"
        self.log_file.parent.mkdir(parents=True, exist_ok=True)
        # Sandboxes created by this attempt (subset of ctx.sandboxes). Used by
        # run_test to preserve diagnostic state when the attempt fails.
        self.local_sandboxes: list[str] = []

    @property
    def debug_new_flags(self) -> list[str]:
        """Return --debug flag for `yoloai new` when debug mode is active."""
        return ["--debug"] if self.ctx.debug else []

    def run(self, *args: str, timeout: int = CMD_TIMEOUT) -> subprocess.CompletedProcess[str]:
        """Run a yoloai subcommand, logging the invocation and output."""
        cmd = [self.ctx.yoloai_bin]
        if self.ctx.debug:
            cmd.extend(["--bugreport", "unsafe"])
        cmd.extend(args)
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
        with self.log_file.open("a") as f:
            f.write(f"$ {' '.join(cmd)}\n")
            f.write(f"exit: {result.returncode}\n")
            if result.stdout:
                f.write(f"stdout:\n{result.stdout}\n")
            if result.stderr:
                f.write(f"stderr:\n{result.stderr}\n")
            f.write("\n")
        return result

    def sandbox(self, label: str) -> str:
        """Allocate a sandbox name and register it for cleanup."""
        name = f"{self.ctx.run_id}-{label}"
        self.ctx.sandboxes.append(name)
        self.local_sandboxes.append(name)
        return name

    def project(self, label: str) -> Path:
        """Return a fresh copy of the project fixture for this test."""
        dest = self.ctx.tmpdir / f"project-{label}"
        if dest.exists():
            shutil.rmtree(dest)
        shutil.copytree(self.ctx.fixture_dir, dest)
        return dest

    def assert_ok(self, result: subprocess.CompletedProcess[str], step: str) -> None:
        if result.returncode != 0:
            raise AssertionError(
                f"{step}: exit {result.returncode}\nstderr: {result.stderr.strip()}"
            )

    def assert_in(self, needle: str, haystack: str, step: str) -> None:
        if needle not in haystack:
            raise AssertionError(
                f"{step}: expected {needle!r} in output\ngot: {haystack[:400]}"
            )

    def _sandbox_status(self, sandbox_name: str) -> str:
        """Return the sandbox status string, or 'unknown' on any error."""
        try:
            r = subprocess.run(
                [self.ctx.yoloai_bin, "sandbox", sandbox_name, "info", "--json"],
                capture_output=True, text=True, timeout=15,
            )
            data = json.loads(r.stdout)
            return str(data.get("status", "unknown"))
        except Exception:
            return "unknown"

    def wait_for_done(self, sandbox_name: str, timeout: int = 30) -> None:
        """Poll until the sandbox status is 'done' or 'failed'.

        Used after wait_for_sentinel when the test needs the agent to have
        fully exited (StatusDone) before calling start — e.g. to exercise the
        relaunchAgent* code path rather than the container-recreation path.
        """
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            status = self._sandbox_status(sandbox_name)
            if status in ("done", "failed"):
                return
            time.sleep(1)
        raise AssertionError(
            f"sandbox {sandbox_name!r} did not reach done/failed within {timeout}s "
            f"(last status: {self._sandbox_status(sandbox_name)!r})"
        )

    def wait_for_sentinel(
        self,
        sandbox_name: str,
        sentinel: str = SENTINEL,
        timeout: int = DEFAULT_TIMEOUT,
        stall_grace_secs: int = STALL_GRACE_SECS,
    ) -> None:
        """Poll `yoloai files ls` until `sentinel` appears as an exact line.

        Fails early if the sandbox reaches a terminal state (done/failed/stopped/…)
        or sustains an idle state for STALL_IDLE_COUNT × 3s without the sentinel.
        Stall detection is skipped for the first stall_grace_secs to avoid false
        positives during slow VM startup. Use spec.sentinel_stall_grace() to get
        the per-backend value.
        """
        deadline = time.monotonic() + timeout
        start = time.monotonic()
        consecutive_idle = 0

        while time.monotonic() < deadline:
            r = self.run("files", sandbox_name, "ls", timeout=15)
            if r.returncode == 0:
                lines = [line.strip() for line in r.stdout.splitlines()]
                if sentinel in lines:
                    return

            elapsed = time.monotonic() - start
            if elapsed >= stall_grace_secs:
                status = self._sandbox_status(sandbox_name)
                if status in STALL_TERMINAL:
                    raise AssertionError(
                        f"agent reached terminal state {status!r} "
                        f"without sentinel {sentinel!r}{self._sentinel_diag(sandbox_name)}"
                    )
                if status == "idle":
                    consecutive_idle += 1
                    if consecutive_idle >= STALL_IDLE_COUNT:
                        raise AssertionError(
                            f"agent idle for {consecutive_idle * 3}s+ "
                            f"without sentinel {sentinel!r}"
                            f"{self._sentinel_diag(sandbox_name)}"
                        )
                else:
                    consecutive_idle = 0

            time.sleep(3)

        raise AssertionError(
            f"sentinel {sentinel!r} not seen in {timeout}s "
            f"(log: {self.log_file}){self._sentinel_diag(sandbox_name)}"
        )

    def _sentinel_diag(self, sandbox_name: str) -> str:
        """Build a one-line diagnostic for sentinel-wait failures.

        Reports what (if anything) is in the exchange dir, plus host disk
        state, plus an in-sandbox network probe (DF5), so "agent idle 9s+"
        tells you which failure mode you hit:
        - exchange dir empty → agent never wrote the sentinel (it stalled,
          erred on the command, or never processed the prompt); the preserved
          terminal-snapshot.txt shows which
        - host disk near full → almost certainly ENOSPC; prune containerd
        - network unreachable → DF8's Kata netns warm-up race; not an agent stall
        """
        parts: list[str] = []
        ls = self.run("files", sandbox_name, "ls", timeout=15)
        if ls.returncode == 0:
            present = sorted(line.strip() for line in ls.stdout.splitlines() if line.strip())
            parts.append(f"exchange dir: {present if present else 'empty'}")
        else:
            parts.append("exchange dir: <ls failed>")
        try:
            usage = shutil.disk_usage("/")
            pct = usage.used * 100 // usage.total
            parts.append(f"host /: {pct}% used, {usage.free // (1024 ** 3)}G free")
        except OSError:
            pass
        if probe := _probe_network(sandbox_name):
            parts.append(f"network: {probe}")
        return "\n      " + "; ".join(parts)


# ---------------------------------------------------------------------------
# Test runner
# ---------------------------------------------------------------------------

# Files and directories copied out of ~/.yoloai/sandboxes/<name>/ when a test
# fails. Kept intentionally narrow: enough to answer "did the prompt arrive,
# did the agent launch, what status did it land in, what files did it touch"
# without dragging in credentials (agent-state/) or build caches (cache/).
# work/ is included so the user can inspect the agent's actual diff.
_PRESERVE_FILES = (
    "environment.json",  # was "meta.json" pre-Q-W rename
    "sandbox-state.json",
    "agent-status.json",
    "prompt.txt",
    "resume-prompt.txt",
    "runtime-config.json",
    "lifecycle-on-create-done",
    "setup.log",
    "xcodebuild-firstlaunch.log",
    # DF9 root-cause-investigation diagnostic: in-VM + host-side
    # network state captured when waitForNetworkReady's probe budget
    # exhausts (containerd backend only, ~30s into Start). Tells us
    # exactly which network stage failed without re-running.
    "network-diag.txt",
)
_PRESERVE_DIRS = (
    "logs",
    "files",
    "work",
)


def _sudo_uid_gid() -> tuple[Optional[int], Optional[int]]:
    """Return (uid, gid) of the sudo invoker when running as root via sudo.
    Mirrors internal/fileutil.SudoUID/SudoGID. Returns (None, None) otherwise.
    """
    if os.geteuid() != 0:
        return None, None
    try:
        return int(os.environ["SUDO_UID"]), int(os.environ["SUDO_GID"])
    except (KeyError, ValueError):
        return None, None


def _fix_preserved_perms(target: Path) -> None:
    """Chown to the sudo invoker (if any) and widen perms to 755/644.

    Sandbox source files under ~/.yoloai/sandboxes/ are often mode 600/700 and
    root-owned when the smoke test ran under sudo. shutil.copy2/copytree
    preserve those perms, which then trips `golangci-lint ./...` during the
    next `make check` (the user's shell can't traverse root:700 dirs). Open
    up the preserved copies — they are diagnostic artifacts, not credentials.
    """
    uid, gid = _sudo_uid_gid()

    def adjust(path: str, is_dir: bool) -> None:
        try:
            os.chmod(path, 0o755 if is_dir else 0o644)
        except OSError:
            pass
        if uid is not None and gid is not None:
            try:
                os.chown(path, uid, gid)
            except OSError:
                pass

    adjust(str(target), is_dir=True)
    for root, dirs, files in os.walk(target):
        for d in dirs:
            adjust(os.path.join(root, d), is_dir=True)
        for f in files:
            adjust(os.path.join(root, f), is_dir=False)


# ---- network probe diagnostic (DF5) ------------------------------------------
#
# Sandboxes on Kata-backed backends (containerd-vm / containerd-vmenhanced)
# show an "agent idle 9s+" failure family that DF8 traced to TCP
# ConnectionRefused — the agent receives the prompt, calls the API, and
# the connection is refused (probably Kata netns warm-up race). The smoke
# test had no way to distinguish "agent stuck doing nothing" from "agent
# is actively retrying a refused connection" until DF3's terminal snapshot
# landed.
#
# This probes network reachability from INSIDE the sandbox at the moment
# the smoke test decides to fail. The result is appended to the failure
# diagnostic line — every "agent idle 9s+" / "agent terminal" / "sentinel
# timeout" failure now carries an explicit network classification:
#
#   exchange dir: empty; host /: 76% used, 18G free; network: reachable (HTTP 401)
#   exchange dir: empty; host /: 76% used, 18G free; network: unreachable (curl exit 7)
#
# Running at failure-diagnosis time (not pre-prompt) means passing tests
# pay no latency, and we get network state at a known reference moment
# alongside the terminal-snapshot capture.

# Multi-stage shell probe run inside the sandbox. Each stage maps to one
# CNI step that might be racy — dns → route → tcp → https. Every stage is
# wrapped in `timeout` so a wedged step (glibc resolver hanging on missing
# DNS, kernel waiting for SYN-ACK) can't blow past the outer subprocess
# budget. Worst-case total ≈ 5+1+5+9 = 20s; outer subprocess timeout is
# 30s to give headroom for ctr/exec setup latency. Emits one key=value
# line per stage so future DF8 data points carry structural CNI info.
_NETWORK_PROBE_SCRIPT = """
set +e

# 1. DNS resolution. `timeout 5` bounds glibc's resolver wait, which on a
# broken/missing nameserver can hang 20-30s before giving up.
timeout 5 getent hosts api.anthropic.com >/dev/null 2>&1
echo "dns=$([ $? -eq 0 ] && echo ok || echo fail)"

# 2. Default route present (instant; catches CNI not setting up a route).
ip route show default 2>/dev/null | grep -q default
echo "route=$([ $? -eq 0 ] && echo ok || echo fail)"

# 3. Raw TCP to a fixed IP, bypassing DNS. /dev/tcp is bash-only; if bash
# is absent the redirect fails fast (exit 127), still bounded by timeout.
timeout 5 bash -c '</dev/tcp/1.1.1.1/443' 2>/dev/null
echo "tcp_1111_443=$([ $? -eq 0 ] && echo ok || echo fail)"

# 4. End-to-end HTTPS to the actual API. curl's own timeouts cap it but
# wrap in `timeout` as belt-and-suspenders for stalled DNS in --resolve.
timeout 9 curl -sS --connect-timeout 5 --max-time 8 -o /dev/null \
  -w "%{http_code}" https://api.anthropic.com/ >/tmp/.np_code 2>/tmp/.np_err
np_exit=$?
echo "https_exit=$np_exit"
echo "https_code=$(cat /tmp/.np_code 2>/dev/null)"
"""


def _network_probe_cmd(backend: str, container_name: str) -> Optional[list[str]]:
    """Subprocess command to run the multi-stage network probe inside the
    sandbox. Returns None for unsupported backends.
    """
    probe = ["sh", "-c", _NETWORK_PROBE_SCRIPT]
    if backend == "docker":
        return ["docker", "exec", "-i", "--user", "yoloai", container_name, *probe]
    if backend == "podman":
        return ["podman", "exec", "-i", "--user", "yoloai", container_name, *probe]
    if backend == "containerd":
        exec_id = f"netprobe{int(time.time() * 1000)}"
        return [
            "sudo", "-n", "ctr", "-n", "yoloai", "task", "exec",
            "--exec-id", exec_id, "--user", "yoloai", container_name, *probe,
        ]
    return None


def _parse_network_probe(stdout: bytes) -> dict[str, str]:
    """Parse the key=value lines emitted by _NETWORK_PROBE_SCRIPT."""
    out: dict[str, str] = {}
    for line in stdout.decode(errors="replace").splitlines():
        line = line.strip()
        if "=" not in line:
            continue
        key, _, value = line.partition("=")
        out[key.strip()] = value.strip()
    return out


def _summarize_network_probe(fields: dict[str, str]) -> str:
    """Boil the staged probe down to one human-readable diagnostic line.
    The format is intentionally compact so it slots into _sentinel_diag's
    semicolon-separated parts list without overflowing.
    """
    if not fields:
        return "unreachable (no probe output)"
    dns = fields.get("dns", "?")
    route = fields.get("route", "?")
    tcp = fields.get("tcp_1111_443", "?")
    https_exit = fields.get("https_exit", "?")
    https_code = fields.get("https_code", "")

    # Verdict: HTTPS exit 0 with any code means reachable; otherwise call
    # out the earliest failing stage so the reader knows where CNI broke.
    if https_exit == "0":
        return f"reachable [dns={dns} route={route} tcp={tcp} https={https_code}]"
    earliest = "https"
    for stage, ok in (("dns", dns), ("route", route), ("tcp", tcp)):
        if ok != "ok":
            earliest = stage
            break
    return f"unreachable [{earliest} failed | dns={dns} route={route} tcp={tcp} https=exit {https_exit}]"


def _probe_network(sandbox_name: str) -> Optional[str]:
    """Returns a one-line diagnostic about network reachability from inside
    the sandbox via a staged probe (DNS → route → TCP → HTTPS), or None if
    the sandbox / backend isn't probe-able. Examples:
        "reachable [dns=ok route=ok tcp=ok https=401]"
        "unreachable [dns failed | dns=fail route=ok tcp=ok https=exit 6]"
        "unreachable [route failed | dns=ok route=fail tcp=fail https=exit 7]"
        "unreachable [tcp failed | dns=ok route=ok tcp=fail https=exit 28]"
        "unreachable [https failed | dns=ok route=ok tcp=ok https=exit 35]"
    """
    env_path = Path.home() / ".yoloai" / "sandboxes" / sandbox_name / "environment.json"
    if not env_path.is_file():
        return None
    try:
        backend = str(json.loads(env_path.read_text()).get("backend", ""))
    except (OSError, json.JSONDecodeError):
        return None
    if not backend:
        return None
    container = f"yoloai-{sandbox_name}"
    cmd = _network_probe_cmd(backend, container)
    if cmd is None:
        return None
    try:
        r = subprocess.run(cmd, capture_output=True, timeout=30)
    except subprocess.TimeoutExpired as e:
        # The script's per-stage timeouts cap each step, so this should
        # only fire on ctr/docker exec setup latency. Include any partial
        # output so we don't lose the stages that did complete.
        partial = ""
        if e.stdout:
            fields = _parse_network_probe(e.stdout)
            if fields:
                partial = f" partial={_summarize_network_probe(fields)}"
        return f"unreachable (subprocess timeout{partial})"
    except OSError as e:
        return f"probe error ({e})"
    return _summarize_network_probe(_parse_network_probe(r.stdout))


# ---- monitor.jsonl detector-tail diagnostic (DF4) ----------------------------
#
# logs/monitor.jsonl carries one entry per detector cycle — the wchan
# detector's "do_epoll_wait + no connections -> idle" line was the decisive
# signal for diagnosing the DF8 failure family. The whole stream is already
# preserved under logs/, but finding the relevant entries means grepping
# through hundreds of lines.
#
# This extracts the last N `event: detector.result` entries and writes them
# as a top-level monitor-tail.txt next to environment.json and the
# terminal-snapshot files. The preserved attempt dir's first 4-5 files now
# tell the failure story without the user opening logs/.

_MONITOR_TAIL_LINES = 30


def _write_monitor_tail(sandbox_name: str, dest_dir: Path) -> bool:
    """Extract the last N detector.result entries from logs/monitor.jsonl
    and write them as a plain-text monitor-tail.txt in *dest_dir*.
    Best-effort — returns False if monitor.jsonl is missing/empty/malformed,
    True if anything was written.
    """
    src = Path.home() / ".yoloai" / "sandboxes" / sandbox_name / "logs" / "monitor.jsonl"
    if not src.is_file():
        return False
    try:
        raw = src.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return False

    detector_lines: list[str] = []
    for line in raw.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            entry = json.loads(line)
        except json.JSONDecodeError:
            continue
        if entry.get("event") != "detector.result":
            continue
        ts = str(entry.get("ts", "")).removesuffix("Z")
        msg = str(entry.get("msg", "")).strip()
        detector_lines.append(f"{ts}  {msg}")

    if not detector_lines:
        return False

    tail = detector_lines[-_MONITOR_TAIL_LINES:]
    header = (
        f"# Last {len(tail)} of {len(detector_lines)} detector.result entries from logs/monitor.jsonl\n"
        f"# Full stream preserved in logs/monitor.jsonl.\n\n"
    )
    try:
        (dest_dir / "monitor-tail.txt").write_text(header + "\n".join(tail) + "\n")
        return True
    except OSError:
        return False


# ---- tmux capture-pane diagnostic snapshot (DF3) -----------------------------
#
# When a test fails (especially the "agent idle 9s+" family on containerd-vm /
# containerd-vmenhanced), the preserved agent.log is a raw ANSI byte stream that
# can't be read without piping through a terminal emulator. We need the
# *rendered* text — what the user would see on screen — to know whether the
# agent printed a clarifying question (DF2), an API error, or nothing at all.
#
# DF3 phase 2 (2026-05-27): the capture lives in yoloai now —
# `yoloai sandbox <name> terminal-snapshot [--ansi]` invokes
# sandbox.Manager.CaptureTerminal via the runtime's non-interactive Exec
# surface, so the per-backend dispatch (docker exec / podman exec / sudo ctr
# task exec) is gone from this script. The CLI command is the single source
# of truth and is also wired into the bug-report writer at
# internal/cli/sandboxcmd/bugreport.go::writeBugReportTerminalSnapshot, so
# `yoloai sandbox <name> bugreport unsafe` carries the same snapshot for
# users who hit the failure outside the smoke test.

def _capture_terminal_snapshot(
    yoloai_bin: str, sandbox_name: str, dest_dir: Path, log_file: Optional[Path] = None
) -> bool:
    """Capture rendered tmux output for *sandbox_name* into *dest_dir*.
    Writes terminal-snapshot.txt (plain) and terminal-snapshot.ansi (with
    escape sequences preserved). Returns True if at least one was written.
    Best-effort — failures are logged and swallowed.

    Delegates to `yoloai sandbox <name> terminal-snapshot` (and the same with
    --ansi) so all the backend dispatch (docker/podman/containerd/tart/
    seatbelt) lives in one yoloai-level primitive instead of duplicated
    Python branches. tart and seatbelt — which the prior per-backend
    dispatch couldn't reach — now capture too.
    """
    wrote = False
    for flag_args, filename in (
        ([], "terminal-snapshot.txt"),
        (["--ansi"], "terminal-snapshot.ansi"),
    ):
        cmd = [yoloai_bin, "sandbox", sandbox_name, "terminal-snapshot", *flag_args]
        try:
            r = subprocess.run(cmd, capture_output=True, timeout=10)
        except (subprocess.TimeoutExpired, OSError) as e:
            if log_file is not None:
                with log_file.open("a") as f:
                    f.write(f"\nterminal-snapshot capture failed (flags={flag_args}): {e}\n")
            continue
        if r.returncode != 0:
            if log_file is not None:
                with log_file.open("a") as f:
                    f.write(
                        f"\nterminal-snapshot capture exit {r.returncode} "
                        f"(flags={flag_args}): {r.stderr.decode(errors='replace').strip()}\n"
                    )
            continue
        try:
            (dest_dir / filename).write_bytes(r.stdout)
            wrote = True
        except OSError:
            pass
    return wrote


def _summarize_network_probe_events(sandbox_name: str) -> Optional[str]:
    """Extract sandbox.network.ready / sandbox.network.probe_timeout events
    from the sandbox's cli.jsonl and return a compact summary, or None if
    no probe events were emitted. DF8 fix observability — passing runs
    show "probe: 1 attempt 47ms" when the probe ran fast, or
    "probe: 8 attempts 3500ms" when the probe caught the Kata netns
    warm-up race. Silent absence means the probe didn't fire at all (e.g.
    non-containerd backends).
    """
    cli_jsonl = Path.home() / ".yoloai" / "sandboxes" / sandbox_name / "logs" / "cli.jsonl"
    if not cli_jsonl.is_file():
        return None
    try:
        contents = cli_jsonl.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return None
    parts: list[str] = []
    for line in contents.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            entry = json.loads(line)
        except json.JSONDecodeError:
            continue
        event = str(entry.get("event", ""))
        if event not in ("sandbox.network.ready", "sandbox.network.probe_timeout"):
            continue
        label = "ready" if event.endswith("ready") else "TIMEOUT"
        attempts = entry.get("attempts", "?")
        elapsed = entry.get("elapsed_ms", "?")
        parts.append(f"{label} ({attempts} attempt(s), {elapsed}ms)")
    if not parts:
        return None
    return "; ".join(parts)


def _preserve_sandbox(yoloai_bin: str, sandbox_name: str, dest_parent: Path) -> Optional[Path]:
    """Copy diagnostic state from ~/.yoloai/sandboxes/<sandbox_name>/ to
    dest_parent/<sandbox_name>/. Returns the target dir, or None if the source
    doesn't exist (e.g. the test failed before the sandbox was created).
    """
    src = Path.home() / ".yoloai" / "sandboxes" / sandbox_name
    if not src.is_dir():
        return None
    target = dest_parent / sandbox_name
    target.mkdir(parents=True, exist_ok=True)
    for f in _PRESERVE_FILES:
        src_f = src / f
        if src_f.is_file():
            try:
                shutil.copy2(src_f, target / f)
            except OSError:
                pass
    for d in _PRESERVE_DIRS:
        src_d = src / d
        if src_d.is_dir():
            try:
                shutil.copytree(src_d, target / d, dirs_exist_ok=True)
            except OSError:
                pass
    # Best-effort: rendered tmux output for the agent's session. Container must
    # still be running for this to work, which is true during _preserve_sandbox
    # (the retry/cleanup destroy happens later). DF3 diagnostic.
    _capture_terminal_snapshot(yoloai_bin, sandbox_name, target)
    # Best-effort: top-level summary of the last N detector decisions from
    # monitor.jsonl. The full stream is also preserved under logs/. DF4.
    _write_monitor_tail(sandbox_name, target)
    return target


def _preserve_failed_attempt(
    ctx: RunContext, test_name: str, sandbox_names: list[str], attempt: int
) -> Optional[Path]:
    """Mirror each sandbox in *sandbox_names* under <log_dir>/sandboxes/<test>/attemptN/.
    Returns the attempt directory if anything was preserved, else None.
    """
    if not sandbox_names:
        return None
    # test_name typically contains "/" (e.g. "stop_start/docker"); pathlib
    # keeps that as a real subdirectory, which groups attempts naturally.
    base = ctx.log_dir / "sandboxes" / test_name / f"attempt{attempt}"
    preserved_any = False
    for name in sandbox_names:
        out = _preserve_sandbox(ctx.yoloai_bin, name, base)
        if out is not None:
            preserved_any = True
    if preserved_any:
        _fix_preserved_perms(base)
        return base
    return None


# ---------------------------------------------------------------------------
# Failure autopsy (Tier 1)
#
# When a test fails we already preserve setup.log + logs/*.jsonl under the
# attempt dir, but nothing reads them — the summary just says "sentinel not
# seen". This pass scans those artifacts for known fatal fingerprints (seeded
# from the backend-idiosyncrasies.md symptom index, so a match cites the exact
# entry to read), builds a key-event timeline from the structured logs, and
# writes a per-failure FAILURE.md. print_summary then surfaces the top match
# inline so root cause is visible without log-spelunking.
# ---------------------------------------------------------------------------

# Where the cited sections live, relative to repo root. Anchors are the GitHub
# slugs of the headings in that file.
_IDIO_DOC = "docs/dev/backend-idiosyncrasies.md"


@dataclass
class Fingerprint:
    label: str
    pattern: str  # regex, searched case-insensitively against artifact text
    anchor: str = ""  # backend-idiosyncrasies.md heading slug, or "" if none
    hint: str = ""  # one-line note on what the match means / what to do


# Ordered most-specific first: the first match wins as the headline cause, so a
# precise "tmux during firstlaunch" fingerprint must precede the generic
# "Python traceback" / "command timed out" catch-alls.
FINGERPRINTS: list[Fingerprint] = [
    Fingerprint(
        "tmux unresolvable during firstlaunch window (Tart)",
        r"FileNotFoundError:.*'tmux'",
        "tart-transient-fspath-failure-makes-tmux-unresolvable-during-the-firstlaunch-window",
        "tmux_bin() retry budget exhausted; the firstlaunch security-scan storm outlasted it",
    ),
    Fingerprint(
        "get_working_dir race / FileNotFoundError on workdir (Tart)",
        r"FileNotFoundError.*get_working_dir",
        "tart-vm-workdir-setup-races-python-startup",
    ),
    Fingerprint(
        "Seatbelt SBPL deny / SIGTRAP (exit 133)",
        r"trace/bpt trap|\bexit (code )?133\b|\bdeny\(1\)",
        "agent-dies-silently-sigtrap--sbpl-subpath-rules-must-use-vnode-resolved-paths",
        "an SBPL subpath rule is missing a vnode-resolved variant",
    ),
    Fingerprint(
        "secrets-consumed marker timeout (Tart/Kata)",
        r"secrets-consumed marker not observed",
        "kata-secrets-temp-dir-removed-before-the-guest-reads-it",
    ),
    Fingerprint(
        "git index.lock race (Docker/Podman)",
        r"index\.lock: file exists",
        "dockerpodman-agent-git-and-apply-git-race-on-indexlock",
    ),
    Fingerprint(
        "agent idle / API unreachable (DF8)",
        r"agent idle for \d+s|request timed out|api unreachable",
        "request-timed-out-in-claude-code--api-unreachable-not-dns-failure",
    ),
    Fingerprint(
        "agent's sentinel command failed; agent stalled (tool error in pane)",
        r"error: exit code \d+",
        "agent-stalls-when-the-sentinel-command-errors",
        "the agent ran a command that exited non-zero, then stopped (often asking "
        "for clarification) instead of completing — the sentinel was never "
        "written. Usually a small-model tool-call garble on a long multi-path "
        "command, not an infra fault: prompt delivery and fs writability are fine",
    ),
    Fingerprint(
        "disk full (ENOSPC)",
        r"enospc|no space left on device",
    ),
    Fingerprint(
        "Python traceback in guest setup",
        r"traceback \(most recent call last\):",
        hint="the guest sandbox-setup.py crashed; see the final exception line below",
    ),
    Fingerprint(
        "FileNotFoundError (generic)",
        r"filenotfounderror: \[errno 2\]",
    ),
    Fingerprint(
        "harness timeout (sentinel not seen / command timed out)",
        r"sentinel '.*' not seen|command timed out",
        hint="nothing fatal found in artifacts — the guest stalled rather than crashed",
    ),
]


@dataclass
class FingerprintHit:
    fp: Fingerprint
    line: str  # the first artifact line that matched
    source: str  # filename the match came from


def _autopsy_artifact_files(attempt_dir: Path) -> list[Path]:
    """Files worth scanning for fingerprints, in priority order: the guest
    traceback (setup.log) first, then the structured logs."""
    out: list[Path] = []
    for sandbox_dir in sorted(attempt_dir.glob("*")):
        if not sandbox_dir.is_dir():
            continue
        setup_log = sandbox_dir / "setup.log"
        if setup_log.is_file():
            out.append(setup_log)
        logs = sandbox_dir / "logs"
        if logs.is_dir():
            out.extend(sorted(logs.glob("*.jsonl")))
        diag = sandbox_dir / "network-diag.txt"
        if diag.is_file():
            out.append(diag)
        # The captured agent pane is the only place an agent-side failure shows
        # up — a command the agent ran that errored, or a clarifying question it
        # stalled on. The structured logs see "idle", not why.
        snapshot = sandbox_dir / "terminal-snapshot.txt"
        if snapshot.is_file():
            out.append(snapshot)
    return out


def scan_fingerprints(attempt_dir: Path) -> list[FingerprintHit]:
    """Scan preserved artifacts for known fatal fingerprints.

    Returns hits in FINGERPRINTS order (most-specific first). At most one hit
    per fingerprint — the first matching line found across the artifact files.
    """
    compiled = [(fp, re.compile(fp.pattern, re.IGNORECASE)) for fp in FINGERPRINTS]
    files = _autopsy_artifact_files(attempt_dir)
    hits: list[FingerprintHit] = []
    seen: set[str] = set()
    for fp, rx in compiled:
        if fp.label in seen:
            continue
        for f in files:
            try:
                text = f.read_text(errors="replace")
            except OSError:
                continue
            for line in text.splitlines():
                if rx.search(line):
                    hits.append(FingerprintHit(fp, line.strip()[:200], f.name))
                    seen.add(fp.label)
                    break
            if fp.label in seen:
                break
    return hits


_TS_RX = re.compile(r"(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?)")


def _parse_ts(s: str) -> Optional[float]:
    """Parse an ISO-8601 'ts' field (trailing Z tolerated) to epoch seconds."""
    m = _TS_RX.search(s)
    if not m:
        return None
    raw = m.group(1)
    for fmt in ("%Y-%m-%dT%H:%M:%S.%f", "%Y-%m-%dT%H:%M:%S"):
        try:
            return time.mktime(time.strptime(raw, fmt))
        except ValueError:
            continue
    return None


def _collect_log_events(logs_dirs: list[Path]) -> list[tuple[float, str, str]]:
    """Parse every *.jsonl under each logs dir into (epoch, event, msg),
    sorted chronologically. Lines without a parseable ts are dropped."""
    events: list[tuple[float, str, str]] = []
    for logs in logs_dirs:
        if not logs.is_dir():
            continue
        for jf in logs.glob("*.jsonl"):
            try:
                text = jf.read_text(errors="replace")
            except OSError:
                continue
            for line in text.splitlines():
                line = line.strip()
                if not line:
                    continue
                try:
                    d = json.loads(line)
                except ValueError:
                    continue
                ts = d.get("ts") or d.get("time") or ""
                epoch = _parse_ts(ts) if isinstance(ts, str) else None
                if epoch is None:
                    continue
                ev = str(d.get("event") or d.get("level") or "?")
                msg = str(d.get("msg") or d.get("message") or "")
                events.append((epoch, ev, msg))
    events.sort(key=lambda e: e[0])
    return events


def _attempt_logs_dirs(attempt_dir: Path) -> list[Path]:
    """The logs/ dir under each preserved sandbox inside an attempt dir."""
    return [sd / "logs" for sd in sorted(attempt_dir.glob("*")) if sd.is_dir()]


def build_timeline(attempt_dir: Path, max_lines: int = 40) -> list[str]:
    """Build a chronological key-event timeline from the structured jsonl logs.

    Each line is `+<delta>s <event>  <msg>`, delta measured from the first
    event. Gaps over 5s are flagged with `<<< GAP Ns` because a long stall
    between events (e.g. firstlaunch hiding tmux) is the usual smoking gun.
    The tail near the crash is the most useful, so when there are more than
    max_lines events we keep the first few and the last many.
    """
    events = _collect_log_events(_attempt_logs_dirs(attempt_dir))
    if not events:
        return []
    # Collapse runs of the same event name (e.g. host sandbox.info polling)
    # into one line with a count and span, so the noise can't bury the gaps.
    collapsed: list[tuple[float, str, str, int, float]] = []  # epoch, ev, msg, count, last_epoch
    for epoch, ev, msg in events:
        if collapsed and collapsed[-1][1] == ev:
            first_epoch, e0, m0, count, _ = collapsed[-1]
            collapsed[-1] = (first_epoch, e0, m0, count + 1, epoch)
        else:
            collapsed.append((epoch, ev, msg, 1, epoch))
    t0 = collapsed[0][0]
    rendered: list[str] = []
    prev = t0
    for epoch, ev, msg, count, last_epoch in collapsed:
        gap = epoch - prev
        flag = f"   <<< GAP {gap:.0f}s" if gap > 5 else ""
        label = ev if count == 1 else f"{ev} (x{count} over {last_epoch - epoch:.0f}s)"
        rendered.append(f"  +{epoch - t0:6.1f}s  {label:<40}  {msg[:60]}{flag}")
        prev = last_epoch
    if len(rendered) > max_lines:
        head, tail = 6, max_lines - 6
        rendered = (
            rendered[:head]
            + [f"  ... {len(rendered) - max_lines} earlier events elided ..."]
            + rendered[-tail:]
        )
    return rendered


# ---------------------------------------------------------------------------
# Baseline retention (Tier 3)
#
# On every pass we snapshot a tiny "last-good" record per (test, backend):
# the ordered list of structured event names the passing run emitted, plus its
# environment.json and version. On a later failure, the autopsy diffs the
# failing run's events against last-good and reports which steps the good run
# reached that this one never did — i.e. exactly where it stalled. This turns
# an intermittent failure into "the good run got to agent.ready; this one died
# at tmux.start", without hunting down a prior passing run by hand.
# ---------------------------------------------------------------------------

def _testcache_root() -> Path:
    """Repo-local cache for smoke-test state: run dirs, baselines, and the index.

    Lives at <repo-root>/.testcache (gitignored) rather than ~/.yoloai so that
    multiple checkouts of the repo don't clash on shared state, and so cruft can
    be cleared at different levels (drop runs/ but keep baselines+index, or nuke
    the whole dir). Override with YOLOAI_SMOKE_CACHE for a custom location."""
    env = os.environ.get("YOLOAI_SMOKE_CACHE")
    if env:
        return Path(env).expanduser()
    return Path(__file__).resolve().parent.parent / ".testcache"


_TESTCACHE_ROOT = _testcache_root()
_BASELINE_ROOT = _TESTCACHE_ROOT / "baselines"


def _baseline_path(test_name: str) -> Path:
    """test_name is like 'full_workflow/tart'; keep the slash as a subdir."""
    return _BASELINE_ROOT / f"{test_name}.json"


def _read_environment(sandbox_dir: Path) -> dict[str, object]:
    env = sandbox_dir / "environment.json"
    try:
        data = json.loads(env.read_text())
        return data if isinstance(data, dict) else {}
    except (OSError, ValueError):
        return {}


def save_baseline(ctx: RunContext, test_name: str, sandbox_names: list[str]) -> None:
    """Snapshot last-good event names + environment for a passing test.

    Reads from the still-live ~/.yoloai/sandboxes/<name>/ dirs (preservation
    only happens on failure, and cleanup runs at exit). Best-effort: any error
    just skips the snapshot — a missing baseline only means no diff later."""
    if not sandbox_names:
        return
    sandboxes_root = Path.home() / ".yoloai" / "sandboxes"
    logs_dirs = [sandboxes_root / n / "logs" for n in sandbox_names]
    events = _collect_log_events(logs_dirs)
    if not events:
        return
    # Ordered, de-duplicated event names (first occurrence wins).
    seen: set[str] = set()
    event_names: list[str] = []
    for _epoch, ev, _msg in events:
        if ev not in seen:
            seen.add(ev)
            event_names.append(ev)
    environment = _read_environment(sandboxes_root / sandbox_names[0])
    record = {
        "test": test_name,
        "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "version": binary_version_info(ctx.yoloai_bin),
        "event_names": event_names,
        "last_event": event_names[-1] if event_names else None,
        "environment": environment,
    }
    path = _baseline_path(test_name)
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(record, indent=2) + "\n")
    except OSError:
        pass


def baseline_diff_lines(test_name: str, attempt_dir: Path) -> list[str]:
    """Markdown lines comparing a failing attempt to its last-good baseline.

    Empty if no baseline exists. Otherwise reports the baseline's identity and
    the steps it reached that the failing run did not — the stall point."""
    path = _baseline_path(test_name)
    try:
        base = json.loads(path.read_text())
    except (OSError, ValueError):
        return []
    base_events: list[str] = list(base.get("event_names") or [])
    fail_events = {ev for _e, ev, _m in _collect_log_events(_attempt_logs_dirs(attempt_dir))}
    missing = [ev for ev in base_events if ev not in fail_events]
    ver = base.get("version") or {}
    commit = ver.get("commit", "?") if isinstance(ver, dict) else "?"
    lines = [
        "## Baseline comparison",
        "",
        f"- last-good: {base.get('timestamp', '?')} (commit {commit})",
        f"- last-good reached: `{base.get('last_event') or '?'}`",
    ]
    if missing:
        lines.append(
            f"- steps the last-good run reached that this run never did "
            f"({len(missing)}):"
        )
        for ev in missing[:20]:
            lines.append(f"    - `{ev}`")
        if len(missing) > 20:
            lines.append(f"    - ... and {len(missing) - 20} more")
    else:
        lines.append("- this run reached every step the last-good run did "
                     "(failure is later than the captured event surface)")
    lines.append("")
    return lines


def write_failure_autopsy(
    ctx: RunContext, result: TestResult, attempt_dir: Path
) -> Optional[Path]:
    """Scan + timeline + write FAILURE.md into *attempt_dir*. Records the
    autopsy path and matched fingerprint labels on *result*. Returns the
    FAILURE.md path, or None if attempt_dir is unusable."""
    if not attempt_dir.is_dir():
        return None
    hits = scan_fingerprints(attempt_dir)
    timeline = build_timeline(attempt_dir)
    result.fingerprints = [h.fp.label for h in hits]

    lines: list[str] = []
    lines.append(f"# Failure autopsy: {result.name}")
    lines.append("")
    lines.append(f"- run: {ctx.log_dir.name}")
    lines.append(f"- version: {binary_version(ctx.yoloai_bin)}")
    lines.append(f"- elapsed: {result.elapsed_s:.1f}s")
    lines.append(f"- reason: {result.reason}")
    lines.append("")
    lines.append("## Matched fingerprints")
    lines.append("")
    if hits:
        for h in hits:
            lines.append(f"### {h.fp.label}")
            if h.fp.hint:
                lines.append(f"- {h.fp.hint}")
            if h.fp.anchor:
                lines.append(f"- see: {_IDIO_DOC}#{h.fp.anchor}")
            lines.append(f"- matched ({h.source}): `{h.line}`")
            lines.append("")
    else:
        lines.append("None. No known fatal fingerprint matched — likely a new")
        lines.append("failure mode. Inspect setup.log and logs/ below, and if the")
        lines.append(f"root cause is new add a fingerprint + a {_IDIO_DOC} entry.")
        lines.append("")
    lines.append("## Key-event timeline")
    lines.append("")
    if timeline:
        lines.append("```")
        lines.extend(timeline)
        lines.append("```")
    else:
        lines.append("(no structured events found in logs/*.jsonl — the guest may")
        lines.append("have crashed before its structured logger flushed)")
    lines.append("")
    lines.extend(baseline_diff_lines(result.name, attempt_dir))

    out = attempt_dir / "FAILURE.md"
    try:
        out.write_text("\n".join(lines) + "\n")
    except OSError:
        return None
    result.autopsy_path = str(out)
    return out


def _destroy_retry_sandboxes(ctx: RunContext, count_before: int) -> None:
    """Destroy sandboxes added since *count_before* so a retry starts clean."""
    stale = ctx.sandboxes[count_before:]
    for name in stale:
        subprocess.run(
            [ctx.yoloai_bin, "destroy", "--yes", name],
            capture_output=True,
            timeout=30,
        )
    del ctx.sandboxes[count_before:]


def _prerun_prune(ctx: RunContext) -> None:
    """Run `yoloai system prune --yes` once before tests start.

    Addresses DF9's cross-run leak class: a prior smoke invocation
    that exited mid-run (Ctrl-C, killed by a parent process, OOM, …)
    can leave backend state behind — Tart VMs, containerd containers,
    docker containers — that the current run will trip over when it
    allocates sandbox names or hits a per-host limit (the macOS 2-VM
    cap is the canonical example).

    `yoloai system prune` enumerates yoloai-prefixed state across all
    backends and removes anything with no matching sandbox dir. It
    inherits the wedged-shim / wedged-Tart-VM escalation from commits
    3c433b0 and 0b6d2f9, so it can't hang on the same orphan that
    caused the leak in the first place. Bounded to 60s overall;
    timeouts or non-zero exits are surfaced but don't abort the run
    (the per-test prereq checks will catch any actual blockage that
    survived the prune).

    Best-effort: the prune output is parsed for the "Removed …"
    lines and summarized as "pre-run prune: cleaned N items"; on no
    leakage, prints "pre-run prune: clean".
    """
    try:
        result = subprocess.run(
            [ctx.yoloai_bin, "system", "prune", "--yes"],
            capture_output=True, timeout=60, text=True,
        )
    except subprocess.TimeoutExpired:
        print("pre-run prune: TIMEOUT (>60s); continuing — per-test prereqs will catch real blockers")
        print()
        return
    except FileNotFoundError:
        # ctx.yoloai_bin doesn't exist; the caller's smoke-binary
        # check has already failed louder than we will. Stay silent.
        return

    if result.returncode != 0:
        print(f"pre-run prune: exit {result.returncode} (continuing)")
        if result.stderr.strip():
            for line in result.stderr.strip().splitlines()[:5]:
                print(f"  {line}")
        print()
        return

    removed = [line for line in result.stdout.splitlines() if line.startswith("Removed ")]
    if not removed:
        print("pre-run prune: clean")
    else:
        print(f"pre-run prune: cleaned {len(removed)} item(s) from prior runs")
        # Show up to 5 sample lines; long lists collapse to a count.
        for line in removed[:5]:
            print(f"  {line}")
        if len(removed) > 5:
            print(f"  ... and {len(removed) - 5} more")
    print()


def run_test(
    ctx: RunContext,
    name: str,
    fn: Callable[[Test], None],
    attempt: int = 1,
) -> TestResult:
    t = Test(ctx, name, attempt=attempt)
    print(f"  {name} ...", end="", flush=True)
    start = time.monotonic()
    try:
        fn(t)
        result = TestResult(name=name, passed=True, elapsed_s=time.monotonic() - start)
        # DF8 fix observability: surface network-probe events from any
        # sandbox this test created. Silent when the probe didn't fire
        # (e.g. non-containerd backends) or wasn't recorded — see
        # _summarize_network_probe_events.
        probe_notes: list[str] = []
        for sb in t.local_sandboxes:
            if summary := _summarize_network_probe_events(sb):
                probe_notes.append(f"{sb}: {summary}")
        if probe_notes:
            print(f" PASS  [probe: {' | '.join(probe_notes)}]")
        else:
            print(" PASS")
    except SkipTest as e:
        result = TestResult(name=name, skipped=True, reason=str(e), elapsed_s=time.monotonic() - start)
        print(f"\n  *** SKIP [{name}]: {e}")
    except AssertionError as e:
        result = TestResult(name=name, passed=False, reason=str(e), elapsed_s=time.monotonic() - start)
        print(f"\n  *** FAIL [{name}]")
        for line in str(e).splitlines():
            print(f"      {line}")
        print(f"      log: {t.log_file}")
    except subprocess.TimeoutExpired as e:
        result = TestResult(name=name, passed=False, reason=f"command timed out: {e}", elapsed_s=time.monotonic() - start)
        print(f"\n  *** FAIL [{name}]: command timed out")
        print(f"      log: {t.log_file}")
    except Exception as e:
        result = TestResult(name=name, passed=False, reason=f"{type(e).__name__}: {e}", elapsed_s=time.monotonic() - start)
        print(f"\n  *** ERROR [{name}]: {type(e).__name__}: {e}")
        print(f"      log: {t.log_file}")
    # Preserve sandbox state on failure so the user can diagnose later.
    # cleanup() destroys all sandboxes at exit, so this must happen before
    # we return — and retries destroy the prior attempt's sandboxes, so the
    # copy happens per attempt rather than only at end-of-run.
    if not result.passed and not result.skipped and t.local_sandboxes:
        preserved = _preserve_failed_attempt(ctx, name, t.local_sandboxes, attempt)
        if preserved is not None:
            print(f"      preserved: {preserved}")
            autopsy = write_failure_autopsy(ctx, result, preserved)
            if autopsy is not None:
                if result.fingerprints:
                    print(f"      autopsy: {result.fingerprints[0]}")
                print(f"      details: {autopsy}")
    elif result.passed and t.local_sandboxes:
        # Snapshot last-good event surface for future failure diffs (Tier 3).
        # Must read the live sandbox dirs before cleanup destroys them.
        save_baseline(ctx, name, t.local_sandboxes)
    ctx.results.append(result)
    if ctx.junit:
        ctx.junit.write_testcase(result)
    return result


def skip_test(ctx: RunContext, name: str, reason: str) -> TestResult:
    result = TestResult(name=name, skipped=True, reason=reason)
    print(f"  *** SKIP [{name}]: {reason}")
    ctx.results.append(result)
    if ctx.junit:
        ctx.junit.write_testcase(result)
    return result


# ---------------------------------------------------------------------------
# Project fixture
# ---------------------------------------------------------------------------

def create_fixture(tmpdir: Path) -> Path:
    """Create a minimal project used as the workdir for all sandbox tests."""
    fixture = tmpdir / "fixture"
    fixture.mkdir()
    (fixture / "README.md").write_text("# Smoke Test Project\n")
    (fixture / "hello.py").write_text('print("hello")\n')
    return fixture


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

def _prompt(exdir: str, work: str, sentinel: str = SENTINEL) -> str:
    """Wrap the agent's work in a single completion sentinel.

    The agent does its work, then `touch <exdir>/<sentinel>` signals it
    finished; the host polls `yoloai files ls` for that file. One chained
    command, with the exchange path appearing exactly once — the shortest,
    least-garblable form for a small model to reproduce verbatim.

    Prompt iteration history (preserve to avoid regressing):

    v1 — bare shell snippet. Haiku sometimes replied with a clarifying
         question ("Could you clarify what you'd like me to do with it?")
         instead of executing it, captured in a preserved stop_start
         failure transcript.

    v2 — "Run this shell command exactly as written; do not modify it or
         ask for clarification: <cmd>". Fixed v1's "what is this code?"
         interpretation, but DF2 (discovered-findings.md) raised the
         hypothesis that the negation half ("do not ask for clarification")
         independently primes smaller / faster models to ask exactly that —
         classic instruction-following failure under negation. Couldn't be
         empirically verified without a fresh flake transcript.

    v3 — keep the explicit "Run this shell command" wrapper that
         resolved v1's failure mode, drop the negation, add a positive
         tool reference. The "using your shell/bash tool" hint signals
         that a tool call IS expected, reducing the chance the model
         produces a tool-less narrative reply (which would classify as
         idle in monitor.py).

    v4 — bind the exchange dir to a shell variable so the long path appears
         once instead of three times. Both seatbelt full_workflow and
         stop_start flaked (run 20260529-034737): the haiku agent dropped the
         `mv` *target* — the third occurrence of seatbelt's ~90-char host
         exchange path — and stalled asking the user to clarify, so `done`
         was never written. docker/podman passed because their exdir is the
         short `/yoloai/files`; the failure tracked path length/repetition.

    v5 (this) — drop the two-stage in-progress→done rename entirely. The
         rename existed so the success signal survived a mid-prompt ENOSPC
         (rename(2) allocates no blocks), but check_prerequisites now aborts
         the whole run at ≥90% disk full, so that case can't be reached. A
         single `touch <exdir>/<sentinel>` after the work needs the long path
         only once and no shell-var indirection — even simpler than v4. The
         lost "agent started but didn't finish" vs "never started" split
         (_idle_phase, removed with this change) is now covered by the
         preserved terminal-snapshot.txt + its `Error: Exit code N`
         fingerprint, which show directly whether the agent ran anything.
         `{work}` still runs in the cwd (test_full_workflow asserts output.txt
         lands in the work dir after apply).
    """
    cmd = f"{work} && touch {exdir}/{sentinel}"
    return f"Run this shell command exactly as written, using your shell/bash tool:\n{cmd}"


def test_full_workflow(t: Test, spec: BackendSpec) -> None:
    """new → wait → diff → apply (assert content) → log → info."""
    project = t.project(f"workflow-{spec.label}")
    name = t.sandbox(f"workflow-{spec.label}")
    exdir = spec.exchange_dir(name)
    prompt = _prompt(exdir, "echo smoke > output.txt")

    r = t.run(
        "new", name, str(project),
        "--model", "haiku",
        "--prompt", prompt,
        "--yes",
        *spec.new_args(),
        *t.debug_new_flags,
        timeout=spec.new_timeout(),
    )
    t.assert_ok(r, "new")

    t.wait_for_sentinel(name, timeout=spec.sentinel_timeout(), stall_grace_secs=spec.sentinel_stall_grace())

    r = t.run("diff", name)
    t.assert_ok(r, "diff")
    t.assert_in("output.txt", r.stdout, "diff output")

    r = t.run("apply", name, "--yes", "--include-uncommitted")
    t.assert_ok(r, "apply")

    output_file = project / "output.txt"
    if not output_file.exists():
        raise AssertionError("output.txt not found in project dir after apply")
    if "smoke" not in output_file.read_text():
        raise AssertionError(
            f"output.txt does not contain 'smoke': {output_file.read_text()!r}"
        )

    r = t.run("log", name)
    t.assert_ok(r, "log")
    if not r.stdout.strip():
        raise AssertionError("log is empty after agent run")

    r = t.run("sandbox", name, "info")
    t.assert_ok(r, "sandbox info")
    t.assert_in(name, r.stdout, "sandbox info (name)")
    t.assert_in("claude", r.stdout, "sandbox info (agent)")


def test_stop_start(t: Test, spec: BackendSpec) -> None:
    """new → wait → restart with new prompt → wait → diff → apply → verify.

    Uses `yoloai restart --prompt` (= stop + start internally) to verify
    credential re-injection after a container restart. The second prompt
    writes to the work copy so we can verify diff/apply end-to-end.
    """
    project = t.project(f"stop-start-{spec.label}")
    name = t.sandbox(f"stop-start-{spec.label}")
    exdir = spec.exchange_dir(name)
    prompt = _prompt(exdir, "echo smoke > output.txt")

    r = t.run(
        "new", name, str(project),
        "--model", "haiku",
        "--prompt", prompt,
        "--yes",
        *spec.new_args(),
        *t.debug_new_flags,
        timeout=spec.new_timeout(),
    )
    t.assert_ok(r, "new")
    t.wait_for_sentinel(name, timeout=spec.sentinel_timeout(), stall_grace_secs=spec.sentinel_stall_grace())

    # restart = stop + start internally.  A new prompt with a different sentinel
    # proves the agent ran successfully with injected credentials after restart.
    # The prompt writes to the work copy so diff/apply can verify.
    sentinel2 = "done2"
    prompt2 = _prompt(exdir, "echo restarted > output2.txt", sentinel=sentinel2)
    r = t.run("restart", name, "--prompt", prompt2, timeout=spec.new_timeout())
    t.assert_ok(r, "restart")

    # Restart adds stop+start overhead on top of model inference, so allow extra time.
    t.wait_for_sentinel(name, sentinel=sentinel2, timeout=spec.sentinel_timeout() + 60, stall_grace_secs=spec.sentinel_stall_grace())

    # Verify diff shows the restarted agent's output
    r = t.run("diff", name)
    t.assert_ok(r, "diff after restart")
    t.assert_in("output2.txt", r.stdout, "diff after restart")

    # Apply and verify the file lands in the project directory
    r = t.run("apply", name, "--yes", "--include-uncommitted")
    t.assert_ok(r, "apply after restart")

    output2 = project / "output2.txt"
    if not output2.exists():
        raise AssertionError("output2.txt not found in project dir after apply")
    if "restarted" not in output2.read_text():
        raise AssertionError(
            f"output2.txt does not contain 'restarted': {output2.read_text()!r}"
        )


def test_clone(t: Test, spec: BackendSpec) -> None:
    """new (A) → wait → clone to B → diff B shows agent changes.

    Asserts that clone copies the full work copy state including agent
    modifications, not just the baseline.
    """
    project = t.project("clone")
    name_a = t.sandbox("clone-a")
    exdir = spec.exchange_dir(name_a)
    prompt = _prompt(exdir, "echo smoke > clone-output.txt")

    r = t.run(
        "new", name_a, str(project),
        "--model", "haiku",
        "--prompt", prompt,
        "--yes",
        *spec.new_args(),
        *t.debug_new_flags,
        timeout=spec.new_timeout(),
    )
    t.assert_ok(r, "new sandbox A")
    t.wait_for_sentinel(name_a, timeout=spec.sentinel_timeout(), stall_grace_secs=spec.sentinel_stall_grace())

    name_b = f"{t.ctx.run_id}-clone-b"
    r = t.run("clone", name_a, name_b, timeout=CMD_TIMEOUT)
    t.assert_ok(r, "clone")
    # Register B after clone succeeds, before assertions, so it is always destroyed.
    t.ctx.sandboxes.append(name_b)

    r = t.run("diff", name_b)
    t.assert_ok(r, "diff on clone")
    t.assert_in("clone-output.txt", r.stdout, "cloned diff output")


def test_isolation_check(t: Test, spec: BackendSpec) -> None:
    """Verify network-isolated sandbox blocks outbound traffic.

    Creates a sandbox with --network-isolated, waits for it to become active,
    then execs curl to an external address (should be blocked) and to localhost
    (should not timeout — proves networking stack is functional, not just broken).

    Only runs on container backends where iptables rules are applied by entrypoint.
    """
    if spec.isolation != "container" or spec.is_seatbelt:
        raise SkipTest(
            f"isolation_check only runs on plain container backends (got {spec.label}); "
            "seatbelt and container-enhanced (gVisor) don't support iptables-based network isolation"
        )

    project = t.project(f"isolation-{spec.label}")
    name = t.sandbox(f"isolation-{spec.label}")

    r = t.run(
        "new", name, str(project),
        "--no-start", "--yes",
        "--network-isolated",
        *spec.new_args(),
        *t.debug_new_flags,
        timeout=60,
    )
    t.assert_ok(r, "new --network-isolated")

    r = t.run("start", name, timeout=CMD_TIMEOUT)
    t.assert_ok(r, "start")

    # Wait for the container to be active
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        status = t._sandbox_status(name)
        if status == "active" or status == "idle":
            break
        time.sleep(1)

    # Outbound to external address should be blocked by iptables rules
    r = t.run("exec", name, "--", "curl", "-s", "--max-time", "5", "http://1.1.1.1", timeout=30)
    if r.returncode == 0:
        raise AssertionError(
            "curl to 1.1.1.1 succeeded but should be blocked by network isolation"
        )

    # Localhost should not get exit code 28 (timeout) — proves the networking
    # stack is functional, not just broken
    r = t.run("exec", name, "--", "curl", "-s", "--max-time", "3", "http://127.0.0.1:1", timeout=30)
    if r.returncode == 28:
        raise AssertionError(
            "curl to 127.0.0.1 timed out (exit 28) — networking stack may be broken, not isolated"
        )


# ---------------------------------------------------------------------------
# Prerequisites check
# ---------------------------------------------------------------------------

def _run_system_check(ctx: RunContext, daemon: str, isolation: str) -> tuple[bool, str]:
    """Run `yoloai system check` for one (daemon, isolation) pair."""
    cmd = [ctx.yoloai_bin, "system", "check", "--json", "--backend", daemon, "--agent", "claude"]
    if isolation:
        cmd += ["--isolation", isolation]
    try:
        r = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        data: dict[str, object] = json.loads(r.stdout)
        ok = bool(data.get("ok", False))
        note = ""
        checks = data.get("checks", [])
        if isinstance(checks, list):
            for check in checks:
                if isinstance(check, dict) and not check.get("ok"):
                    note = str(check.get("message", "check failed"))
                    break
        return ok, note
    except subprocess.TimeoutExpired:
        return False, "system check timed out"
    except (json.JSONDecodeError, KeyError) as e:
        return False, f"could not parse system check output: {e}"
    except FileNotFoundError:
        return False, "yoloai binary not found"


def check_prerequisites(
    ctx: RunContext,
    backends: list[BackendSpec],
) -> dict[str, PrereqResult]:
    """Run `yoloai system check` for each unique (daemon, isolation) pair; return per-spec results.

    For every reachable daemon, runs `yoloai system build` upfront so that the
    image is guaranteed to be current before any test starts.  Build output is
    forwarded to stdout so the user can see progress and know it isn't stuck.
    """
    print("Checking prerequisites...\n")

    # Disk pre-flight: containerd snapshots accumulate, and ENOSPC inside a
    # Kata VM surfaces indirectly — typically as ConnectionRefused /
    # FailToOpenSocket from the agent process (network plumbing depends on
    # disk-backed state), then "agent idle 9s+" from this harness. Burning
    # several minutes of model retries to discover that is wasteful, so abort
    # at >=90% with a prune hint. Soft-note at 80-89% (still likely to pass).
    try:
        usage = shutil.disk_usage("/")
        pct = usage.used * 100 // usage.total
        free_gb = usage.free // (1024 ** 3)
        if pct >= 90:
            print(
                f"ERROR: host / is {pct}% full ({free_gb}G free).\n"
                "Containerd-based tests fail with ENOSPC-adjacent network errors "
                "at this fill level. Free space before running:\n"
                "  sudo yoloai system prune --cache --yes",
                file=sys.stderr,
            )
            sys.exit(1)
        if pct >= 80:
            print(f"  Note: host / is {pct}% full ({free_gb}G free); watch for ENOSPC on containerd tests.\n")
    except OSError:
        pass

    # Deduplicate by (check_backend, check_isolation) so vm and vm-enhanced are
    # checked separately (each needs its own isolation validation).
    unique_keys: set[tuple[str, str]] = {
        (spec.check_backend, spec.check_isolation) for spec in backends
    }
    check_results: dict[tuple[str, str], tuple[bool, str]] = {}

    for daemon, isolation in sorted(unique_keys):
        check_results[(daemon, isolation)] = _run_system_check(ctx, daemon, isolation)

    # Build images upfront for every known daemon.  This ensures `yoloai new`
    # never triggers an inline build that would blow the per-command timeout.
    # If the daemon isn't running, `system build` exits quickly with an error
    # and we skip the recheck.  If the image is already up to date the build
    # returns quickly too.
    all_daemons: set[str] = {daemon for (daemon, _) in check_results}

    for daemon in sorted(all_daemons):
        print(f"  Building yoloai-base image for {daemon} backend (output below)...")
        build_timeout = TART_BASE_BUILD_TIMEOUT if daemon == "tart" else BASE_BUILD_TIMEOUT
        try:
            r = subprocess.run(
                [ctx.yoloai_bin, "system", "build", "--backend", daemon],
                timeout=build_timeout,
            )
        except subprocess.TimeoutExpired:
            # Fail loud rather than skip: a release gate must not pass while
            # silently dropping a backend. The larger tart budget normally lets
            # the one-time ~30 GB pull finish; if it still times out, pre-pull
            # it once outside the run so the build is a fast no-op here.
            print(
                f"\nERROR: base image build for {daemon} timed out after {build_timeout}s.\n"
                f"Pre-pull it once outside the smoke run, then retry:\n"
                f"  {ctx.yoloai_bin} system build --backend {daemon}",
                file=sys.stderr,
            )
            sys.exit(1)
        print()
        if r.returncode != 0:
            continue
        # Recheck all (daemon, *) pairs now that the image is current.
        for (d, isolation) in list(check_results.keys()):
            if d == daemon:
                check_results[(d, isolation)] = _run_system_check(ctx, d, isolation)

    results: dict[str, PrereqResult] = {}
    for spec in backends:
        key = (spec.check_backend, spec.check_isolation)
        ok, note = check_results.get(key, (False, "not checked"))
        results[spec.label] = PrereqResult(spec=spec, available=ok, note=note)

    col_w = max(len(label) for label in results) + 2
    print(f"  {'BACKEND':<{col_w}} {'STATUS':<6}  NOTE")
    print(f"  {'-' * col_w} {'-' * 6}  {'-' * 40}")
    for label, pr in results.items():
        status = "ok" if pr.available else "FAIL"
        print(f"  {label:<{col_w}} {status:<6}  {pr.note}")
    print()

    return results


# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------

def cleanup(ctx: RunContext) -> None:
    """Destroy all tracked sandboxes and remove the scratch tmpdir.

    Per-sandbox `yoloai destroy` calls can hang when a Kata shim is wedged
    (containerd task RUNNING but the VM is dead; see backend-idiosyncrasies.md
    "Kata shim wedge"). The library now escalates to direct-PID kill, but
    that ladder still takes up to ~15s per stuck sandbox. We bound each
    destroy to 60s; if it times out we record the name and fall back to a
    single `yoloai system prune --yes` pass at the end, which iterates
    backend state directly and uses the same escalation. Surface both the
    per-sandbox timeouts and the prune fallback to stderr so failed
    cleanup is never silent — the previous behavior swallowed
    TimeoutExpired and left containerd state leaking after each run.

    Logs are written to ctx.log_dir (./yoloai-smoketest-<timestamp>/) and
    are never deleted here — they persist until the user cleans them up
    manually.
    """
    if ctx.sandboxes:
        print(f"\nCleaning up {len(ctx.sandboxes)} sandbox(es)...")
        timed_out: list[str] = []
        for name in ctx.sandboxes:
            try:
                subprocess.run(
                    [ctx.yoloai_bin, "destroy", "--yes", name],
                    capture_output=True, timeout=60,
                )
            except subprocess.TimeoutExpired:
                timed_out.append(name)
                print(f"  TIMEOUT  destroy {name} (>60s); will retry via system prune")

        if timed_out:
            print(
                f"\n{len(timed_out)} destroy(s) timed out — running 'yoloai system prune' "
                "as a backend-level fallback."
            )
            try:
                result = subprocess.run(
                    [ctx.yoloai_bin, "system", "prune", "--yes"],
                    capture_output=True, timeout=180, text=True,
                )
                if result.returncode != 0:
                    print(f"  prune exit {result.returncode}: {result.stderr.strip()}")
                else:
                    # The prune output names what was reclaimed; surface it
                    # so the user can see what would otherwise have leaked.
                    out = result.stdout.strip()
                    if out:
                        for line in out.splitlines():
                            print(f"  prune: {line}")
            except subprocess.TimeoutExpired:
                print(
                    "  PRUNE TIMEOUT (>180s) — orphan backend state likely remains. "
                    "Run 'yoloai system doctor' to inspect."
                )

    shutil.rmtree(ctx.tmpdir, ignore_errors=True)


# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

def print_summary(results: list[TestResult]) -> None:
    passed = [r for r in results if r.passed]
    failed = [r for r in results if not r.passed and not r.skipped]
    skipped = [r for r in results if r.skipped]

    print("\n" + "=" * 60)
    print(
        f"Results: {len(passed)} passed, {len(failed)} failed, "
        f"{len(skipped)} skipped"
    )
    print("=" * 60)

    if failed:
        print("\nFailed tests:")
        for r in failed:
            print(f"  FAIL  {r.name}")
            for line in r.reason.splitlines():
                print(f"        {line}")
            if r.fingerprints:
                print(f"        cause: {r.fingerprints[0]}")
            if r.autopsy_path:
                print(f"        autopsy: {r.autopsy_path}")

    if skipped:
        print("\nSkipped tests:")
        for r in skipped:
            print(f"  SKIP  {r.name}: {r.reason}")


# Persistent cross-run index. One JSON object per line, appended after every
# run, so "when did seatbelt start failing / on which sha?" is a grep instead
# of mining 2000+ bugreports. Lives in the repo-local .testcache (see
# _testcache_root), outside any single run dir.
_SMOKE_INDEX = _TESTCACHE_ROOT / "smoke-index.jsonl"


def _result_status(r: TestResult) -> str:
    if r.skipped:
        return "skip"
    return "pass" if r.passed else "fail"


def write_run_manifest(
    ctx: RunContext, results: list[TestResult], *, host: str, tier: str
) -> Optional[Path]:
    """Write <log_dir>/manifest.json (machine-readable sibling of summary.txt)
    and append a one-line row to the repo-local .testcache/smoke-index.jsonl.
    Best-effort: returns the manifest path, or None if it couldn't be written."""
    ver = binary_version_info(ctx.yoloai_bin)
    failed = [r for r in results if not r.passed and not r.skipped]
    totals = {
        "passed": sum(1 for r in results if r.passed),
        "failed": len(failed),
        "skipped": sum(1 for r in results if r.skipped),
    }
    stamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    manifest = {
        "run_id": ctx.run_id,
        "run_dir": str(ctx.log_dir),
        "timestamp": stamp,
        "host": host,
        "tier": tier,
        "version": ver,
        "totals": totals,
        "tests": [
            {
                "name": r.name,
                "status": _result_status(r),
                "elapsed_s": round(r.elapsed_s, 1),
                "reason": r.reason,
                "fingerprints": r.fingerprints,
                "autopsy": r.autopsy_path,
            }
            for r in results
        ],
    }
    manifest_path = ctx.log_dir / "manifest.json"
    try:
        manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")
    except OSError:
        manifest_path = None  # type: ignore[assignment]

    # Index row: compact, one per run, with just enough to triage and locate
    # the full manifest. Failures carry their headline fingerprint inline.
    row = {
        "run_id": ctx.run_id,
        "timestamp": stamp,
        "commit": ver["commit"],
        "version": ver["version"],
        "host": host,
        "tier": tier,
        "totals": totals,
        "run_dir": str(ctx.log_dir),
        "failures": [
            {
                "name": r.name,
                "fingerprint": r.fingerprints[0] if r.fingerprints else None,
            }
            for r in failed
        ],
    }
    try:
        _SMOKE_INDEX.parent.mkdir(parents=True, exist_ok=True)
        with open(_SMOKE_INDEX, "a") as f:
            f.write(json.dumps(row) + "\n")
    except OSError:
        pass

    return manifest_path


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="End-to-end smoke tests for yoloai against real agents.",
    )
    parser.add_argument(
        "--full",
        action="store_true",
        help=(
            "Run the full backend matrix (all backends). "
            "Without --full, only base-tier backends are tested. "
            "Missing backends are skipped with a warning."
        ),
    )
    parser.add_argument(
        "--test",
        action="append",
        help=(
            "Run only specific test(s). Can be specified multiple times. "
            "Examples: --test stop_start --test full_workflow/seatbelt"
        ),
    )
    parser.add_argument(
        "--backend",
        action="append",
        help=(
            "Run full_workflow test only for specific backend(s). "
            "Can be specified multiple times. Examples: --backend seatbelt --backend tart"
        ),
    )
    parser.add_argument(
        "--debug",
        action="store_true",
        help="Add --debug to 'yoloai new' and --bugreport unsafe to all commands",
    )
    parser.add_argument(
        "--junit",
        metavar="PATH",
        help="Write JUnit XML test results to PATH (crash-resilient via atexit)",
    )
    parser.add_argument(
        "--allow-stale",
        action="store_true",
        help=(
            "Skip the check that ./yoloai is newer than the source tree. "
            "Use only when intentionally testing a hand-built binary; "
            "`make smoketest` rebuilds so this is rarely needed."
        ),
    )
    parser.add_argument(
        "--out-dir",
        metavar="DIR",
        help=(
            "Parent directory for the per-run yoloai-smoketest-<ts>/ log dir. "
            "Defaults to <repo>/.testcache/runs/ (gitignored). Override to put "
            "run artifacts elsewhere; see also YOLOAI_SMOKE_CACHE."
        ),
    )
    return parser.parse_args()


def find_yoloai() -> Optional[str]:
    # Smoke test must use the locally built binary from the repo
    if Path("./yoloai").is_file():
        return "./yoloai"
    return None


# Embedded-resource suffixes that go:embed bakes into the binary — kept in sync
# with the Makefile's EMBEDFILES glob so the freshness check uses the same
# definition of "source" that `make build` does.
_EMBED_SUFFIXES = (".sh", ".py", ".conf", ".md")


def _newest_source_mtime() -> tuple[float, str]:
    """Return (mtime, path) of the newest tracked source file under cwd.

    Mirrors the Makefile: every *.go outside vendor/, plus the embedded
    resources under internal/ (Dockerfile, *.sh, *.py, *.conf, *.md;
    excluding tests/), plus go.mod / go.sum. Used to detect a stale ./yoloai
    before a smoke run. Uses os.walk (like `find`) so an unreadable dir — e.g.
    a root-owned smoke-log dir from a prior sudo run — is skipped, not fatal.
    """
    newest = 0.0
    newest_path = ""

    def consider(path: str) -> None:
        nonlocal newest, newest_path
        try:
            m = os.stat(path).st_mtime
        except OSError:
            return
        if m > newest:
            newest, newest_path = m, path

    for dirpath, dirnames, filenames in os.walk("."):
        # Prune dirs make's globs never look at (and that may be unreadable).
        dirnames[:] = [d for d in dirnames if d not in ("vendor", ".git", "__pycache__")]
        parts = set(Path(dirpath).parts)
        in_internal = "internal" in parts
        in_tests = "tests" in parts
        for fn in filenames:
            full = os.path.join(dirpath, fn)
            if fn.endswith(".go"):
                # GOFILES: every *.go outside vendor/ (tests included).
                consider(full)
            elif in_internal and not in_tests and (fn == "Dockerfile" or fn.endswith(_EMBED_SUFFIXES)):
                # EMBEDFILES: embedded resources under internal/, excluding tests/.
                consider(full)

    for extra in ("go.mod", "go.sum"):
        consider(extra)

    return newest, newest_path


def check_binary_fresh(yoloai_bin: str) -> Optional[str]:
    """Return an error message if yoloai_bin is older than any tracked source.

    The smoke test always runs the repo-local ./yoloai; a stale binary
    silently tests old code — Go *or* embedded Python (entrypoint.py,
    sandbox-setup.py) — which is the most confusing failure mode there is.
    The `make smoketest` wrapper rebuilds via its `build` dependency, but a
    direct `python3 scripts/smoke_test.py` invocation does not, so guard it
    here. Returns None when the binary is current.
    """
    try:
        bin_mtime = Path(yoloai_bin).stat().st_mtime
    except OSError:
        return None  # missing binary is reported by the caller's find_yoloai check
    newest, newest_path = _newest_source_mtime()
    if newest <= bin_mtime:
        return None
    return (
        f"ERROR: {yoloai_bin} is older than {newest_path} — the binary is stale.\n"
        "You would be smoke-testing old code (Go or embedded Python). Rebuild first:\n"
        "  make build                 (or use `make smoketest`, which rebuilds for you)\n"
        "Override with --allow-stale only when intentionally testing a hand-built binary."
    )


def binary_version_info(yoloai_bin: str) -> dict[str, str]:
    """Return the binary's embedded build identity as a {version, commit, date}
    dict, read from `<bin> version --json` so the recorded sha reflects what was
    actually compiled and run (the working tree may have moved since the build).
    Missing fields default to '?'; an unqueryable binary yields all '?'."""
    info: dict[str, str] = {"version": "?", "commit": "?", "date": "?"}
    try:
        r = subprocess.run(
            [yoloai_bin, "version", "--json"],
            capture_output=True, text=True, timeout=10,
        )
        parsed = json.loads(r.stdout)
        for k in info:
            info[k] = str(parsed.get(k, "?"))
    except (OSError, ValueError, subprocess.SubprocessError):
        pass
    return info


def binary_version(yoloai_bin: str) -> str:
    """Return the build identity as 'version (commit, date)' for header display."""
    info = binary_version_info(yoloai_bin)
    if info["commit"] == "?" and info["version"] == "?":
        return "unknown"
    return f"{info['version']} ({info['commit']}, {info['date']})"


class _Tee:
    """Forward writes to two streams (real stdout + summary file).

    Used so per-test PASS/FAIL/probe annotations and the final summary
    block — previously printed only to the terminal — land in a file
    that gets preserved with the run. The per-test yoloai logs already
    live in <log_dir>/<test>-<backend>.log; this captures the
    smoke-test's own decisions about those tests.
    """
    def __init__(self, *streams: object) -> None:
        self._streams = streams

    def write(self, data: str) -> int:
        for s in self._streams:
            try:
                s.write(data)  # type: ignore[attr-defined]
                s.flush()  # type: ignore[attr-defined]
            except ValueError:
                # Stream was closed (e.g. by another atexit handler during
                # interpreter shutdown). Skip it — exit code 120 from
                # Python's "stdout broken at shutdown" path otherwise.
                pass
        return len(data)

    def flush(self) -> None:
        for s in self._streams:
            try:
                s.flush()  # type: ignore[attr-defined]
            except ValueError:
                pass

    def isatty(self) -> bool:
        # Preserve TTY-ness from the underlying terminal stream so
        # callers that branch on isatty() (e.g. color output) behave
        # the same as without the tee.
        try:
            return bool(self._streams[0].isatty())  # type: ignore[attr-defined]
        except Exception:
            return False


def _install_stdout_tee(summary_path: Path) -> None:
    """Redirect sys.stdout to a Tee that writes to both terminal and file."""
    summary_path.parent.mkdir(parents=True, exist_ok=True)
    f = open(summary_path, "w", encoding="utf-8", buffering=1)  # line-buffered
    atexit.register(f.close)
    sys.stdout = _Tee(sys.stdout, f)


def main() -> int:
    args = parse_args()

    yoloai_bin = find_yoloai()
    if not yoloai_bin:
        print(
            "ERROR: yoloai not found. "
            "Run from the repo root after `make build`, or install to ~/bin/yoloai.",
            file=sys.stderr,
        )
        return 1

    # Refuse to run against a stale binary: `make smoketest` rebuilds, but a
    # direct script invocation does not, and silently testing old code (Go or
    # embedded Python) wastes a whole run. --allow-stale bypasses intentionally.
    if not args.allow_stale:
        stale_msg = check_binary_fresh(yoloai_bin)
        if stale_msg:
            print(stale_msg, file=sys.stderr)
            return 1

    # Detect the common mistake of `sudo python3 smoke_test.py` without -E,
    # which strips ANTHROPIC_API_KEY and other credentials from the environment.
    if os.getuid() == 0 and not os.environ.get("ANTHROPIC_API_KEY") and not os.environ.get("CLAUDE_CODE_OAUTH_TOKEN"):
        print(
            "ERROR: running as root but no Claude credentials found in environment.\n"
            "sudo strips environment variables by default. Use sudo -E to preserve them:\n"
            "  sudo -E python3 scripts/smoke_test.py\n"
            "  sudo -E make smoketest",
            file=sys.stderr,
        )
        return 1

    # On Linux, full-tier smoke tests need root for VM/namespace backends.
    # Catch the common mistake of forgetting sudo early, before we hit a
    # confusing PermissionError on a root-owned current directory.
    if sys.platform == "linux" and args.full and os.getuid() != 0:
        print(
            "ERROR: full smoke tests require root on Linux (for VM/namespace backends).\n"
            "Run with:\n"
            "  make smoketest-full          (auto-escalates to root)\n"
            "  sudo -E python3 scripts/smoke_test.py --full",
            file=sys.stderr,
        )
        return 1

    run_id = f"smoke-{int(time.time())}"
    tmpdir = Path(tempfile.mkdtemp(prefix="yoloai-smoke-"))
    _t = time.time()
    _ms = int(_t * 1000) % 1000
    out_parent = Path(args.out_dir).expanduser() if args.out_dir else _TESTCACHE_ROOT / "runs"
    log_dir = out_parent / time.strftime(f"yoloai-smoketest-%Y%m%d-%H%M%S.{_ms:03d}", time.gmtime(_t))
    try:
        log_dir.mkdir(parents=True, exist_ok=True)
    except PermissionError:
        print(
            f"ERROR: cannot create {log_dir}\n"
            f"Check that the current directory is writable.",
            file=sys.stderr,
        )
        return 1
    fixture_dir = create_fixture(tmpdir)

    ctx = RunContext(
        yoloai_bin=yoloai_bin,
        tmpdir=tmpdir,
        log_dir=log_dir,
        run_id=run_id,
        fixture_dir=fixture_dir,
        full=args.full,
        debug=args.debug,
        test_filter=args.test,
        backend_filter=args.backend,
    )
    if args.junit:
        ctx.junit = JUnitWriter(args.junit)

    # Tee stdout to <log_dir>/summary.txt so the high-level run
    # transcript (per-test PASS/FAIL lines, probe annotations,
    # final summary block) is captured alongside the per-test logs.
    # Stderr is left untouched. Closed at process exit via atexit.
    # Install the tee BEFORE registering cleanup: atexit is LIFO, so
    # cleanup (registered later) runs first, and its prints land in
    # the summary file before f.close() runs.
    _install_stdout_tee(log_dir / "summary.txt")

    atexit.register(cleanup, ctx)

    is_linux = sys.platform.startswith("linux")
    # When filters are active, use the full matrix so --test / --backend can
    # reach backends (e.g. seatbelt) that live outside the base tier.
    if ctx.full or ctx.test_filter or ctx.backend_filter:
        matrix = FULL_LINUX_BACKENDS if is_linux else FULL_MACOS_BACKENDS
    else:
        matrix = BASE_LINUX_BACKENDS if is_linux else BASE_MACOS_BACKENDS

    # Build the list of specs to prereq-check.  When explicit filters narrow
    # the run, restrict prereq checking (and image builds) to only the backends
    # that will actually be exercised.  DEFAULT_BACKEND is always included
    # because it supplies the credentials check.
    matrix_labels = {s.label for s in matrix}
    def _spec_needed(spec: "BackendSpec") -> bool:
        if not (ctx.test_filter or ctx.backend_filter):
            return True
        if ctx.backend_filter and spec.label in ctx.backend_filter:
            return True
        if ctx.test_filter:
            for test_prefix in ("full_workflow", "stop_start", "isolation_check"):
                if f"{test_prefix}/{spec.label}" in ctx.test_filter:
                    return True
        return False

    all_specs = [DEFAULT_BACKEND] + [
        s for s in matrix
        if s.label != DEFAULT_BACKEND.label and _spec_needed(s)
    ]

    tier = "full" if ctx.full else "base"
    print(f"yoloai smoke test  run={log_dir.name}")
    print(f"host={'linux' if is_linux else 'macos'}  tier={tier}")
    print(f"binary={yoloai_bin}")
    print(f"version={binary_version(yoloai_bin)}")
    print(f"logs={log_dir}\n")

    preq = check_prerequisites(ctx, all_specs)

    # --- Abort: required backend unavailable ---
    default_preq = preq.get(DEFAULT_BACKEND.label)
    if default_preq is None or not default_preq.available:
        note = default_preq.note if default_preq else "not checked"
        print(
            f"ERROR: required backend docker/linux/container is unavailable: {note}\n"
            "Cannot run any tests without the default backend.",
            file=sys.stderr,
        )
        return 1

    # --- Abort: credentials missing (caught by system check on default backend) ---
    if default_preq.note and "no credentials" in default_preq.note.lower():
        print(f"ERROR: {default_preq.note}", file=sys.stderr)
        return 1

    # --- Abort or warn: optional backends unavailable ---
    unavailable_labels = [
        label for label, pr in preq.items()
        if not pr.available and label != DEFAULT_BACKEND.label
        and label in matrix_labels
    ]
    if unavailable_labels:
        notes = [preq[label].note for label in unavailable_labels]
        needs_root = any(
            "network namespace" in note or "CAP_SYS_ADMIN" in note
            for note in notes
        )
        setup_tip = ""
        if needs_root:
            setup_tip = (
                "\nSetup tip: VM isolation requires root-level privileges.\n"
                "Run the smoke test as root to include vm/vmenhanced backends:\n"
                "  sudo make smoketest-full"
            )
        # Always warn+skip for unavailable backends (base tier is designed to
        # be runnable with partial backends; full tier warns but doesn't abort).
        print("WARNING: some backends unavailable (will skip their tests):")
        for label in unavailable_labels:
            print(f"  {label}: {preq[label].note}")
        if setup_tip:
            print(setup_tip)
        print()

    # Pre-run prune: clean up any backend state left over from prior smoke
    # invocations before allocating new sandbox names. DF9 specifically
    # caught this with Tart on macOS — a VM `1779775833-workflow-tart`
    # from a previous run was still alive and counted against Apple's
    # 2-VM concurrent limit, blocking new runs. The containerd backend
    # has its own variant (orphan container records with no matching
    # sandbox dir). Now that the underlying `system prune` handles
    # wedged Kata shims (commit 3c433b0) and wedged Tart VMs
    # (commit 0b6d2f9), running it pre-flight is safe; we can't hang
    # on the same wedge that caused the leak in the first place.
    _prerun_prune(ctx)

    # -------------------------------------------------------------------------
    # Helper to check if a test should run based on --test filter
    def should_run_test(test_name: str) -> bool:
        if ctx.test_filter is None:
            return True
        return test_name in ctx.test_filter

    def run_matrix_test(
        test_label: str,
        test_fn: Callable[[Test, BackendSpec], None],
    ) -> None:
        """Run a test across the backend matrix with prereq checks and retries."""
        for spec in matrix:
            test_name = f"{test_label}/{spec.label}"

            if not should_run_test(test_name):
                continue
            if ctx.backend_filter and spec.label not in ctx.backend_filter:
                continue

            pr = preq.get(spec.label)
            if pr is None or not pr.available:
                reason = pr.note if pr else "not in prereq results"
                skip_test(ctx, test_name, reason)
                continue

            sandbox_count_before = len(ctx.sandboxes)
            result = run_test(ctx, test_name, lambda t: test_fn(t, spec), attempt=1)
            if not result.passed and not result.skipped and spec.retries > 0:
                for retry_idx in range(spec.retries):
                    attempt = retry_idx + 2  # attempt 1 was the initial run
                    print(f"      Retrying {test_name} (attempt {retry_idx + 1}/{spec.retries})...")
                    ctx.results.pop()
                    # Destroy sandboxes created during the failed attempt so
                    # the retry can create fresh ones with the same names.
                    # run_test has already preserved their state under
                    # <log_dir>/sandboxes/<test>/attempt<N>/ before we get here.
                    _destroy_retry_sandboxes(ctx, sandbox_count_before)
                    result = run_test(ctx, test_name, lambda t: test_fn(t, spec), attempt=attempt)
                    if result.passed:
                        break
            # On VM backends, a failed test can leave a running VM that consumes
            # one of the macOS 2-VM slots and blocks subsequent VM tests. Destroy
            # sandboxes immediately after all retries are exhausted (state is
            # already preserved under log_dir/sandboxes/ at this point).
            if not result.passed and spec.is_vm:
                _destroy_retry_sandboxes(ctx, sandbox_count_before)

    # -------------------------------------------------------------------------
    # Non-matrix tests (full tier only)
    # -------------------------------------------------------------------------

    if should_run_test("clone"):
        if ctx.full:
            run_test(ctx, "clone", lambda t: test_clone(t, DEFAULT_BACKEND))
        else:
            skip_test(ctx, "clone", "full tier only (use --full)")

    # -------------------------------------------------------------------------
    # Matrix tests: full_workflow, stop_start, isolation_check
    # -------------------------------------------------------------------------

    print("\nBackend matrix (full_workflow):")
    run_matrix_test("full_workflow", test_full_workflow)

    print("\nBackend matrix (stop_start):")
    run_matrix_test("stop_start", test_stop_start)

    print("\nBackend matrix (isolation_check):")
    run_matrix_test("isolation_check", test_isolation_check)

    print_summary(ctx.results)

    manifest = write_run_manifest(
        ctx, ctx.results, host="linux" if is_linux else "macos", tier=tier
    )
    if manifest is not None:
        print(f"\nmanifest: {manifest}")
    print(f"index: {_SMOKE_INDEX}")

    failed = [r for r in ctx.results if not r.passed and not r.skipped]
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
