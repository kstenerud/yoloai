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

SENTINEL = "done"
DEFAULT_TIMEOUT = 90    # seconds: container + agent startup for non-VM backends
VM_TIMEOUT = 180        # seconds: VM boot + agent startup (Firecracker/Tart)
QEMU_TIMEOUT = 300      # seconds: QEMU-based Kata VM — slower boot than Firecracker
CMD_TIMEOUT = 60        # seconds: individual yoloai commands

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
            return "/Volumes/My Shared Files/yoloai/files"
        return "/yoloai/files"

    def sentinel_timeout(self) -> int:
        if self.sentinel_timeout_override:
            return self.sentinel_timeout_override
        return VM_TIMEOUT if self.is_vm else DEFAULT_TIMEOUT

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
                check_backend="docker"),
    BackendSpec("linux", "vm",                 None,     "containerd-vm",
                check_backend="containerd", is_vm=True, check_isolation="vm",
                sentinel_timeout_override=QEMU_TIMEOUT, stall_grace_secs=90),
]

BASE_MACOS_BACKENDS: list[BackendSpec] = [
    BackendSpec("linux", "container", "docker", "docker",
                check_backend="docker"),
    BackendSpec("mac",   "vm",        None,     "tart",
                check_backend="tart",   is_vm=True, retries=1),
]

# Full tier: all backends for pre-release validation.
FULL_LINUX_BACKENDS: list[BackendSpec] = [
    BackendSpec("linux", "container",          "docker", "docker",
                check_backend="docker"),
    BackendSpec("linux", "container",          "podman", "podman",
                check_backend="podman"),
    BackendSpec("linux", "container-enhanced", None,     "docker-cenhanced",
                check_backend="docker"),
    BackendSpec("linux", "vm",                 None,     "containerd-vm",
                check_backend="containerd", is_vm=True, check_isolation="vm",
                sentinel_timeout_override=QEMU_TIMEOUT, stall_grace_secs=90),
    BackendSpec("linux", "vm-enhanced",        None,     "containerd-vmenhanced",
                check_backend="containerd", is_vm=True, check_isolation="vm-enhanced",
                sentinel_timeout_override=QEMU_TIMEOUT, stall_grace_secs=90),
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
                check_backend="tart",   is_vm=True, retries=1),
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

    def __init__(self, ctx: RunContext, name: str) -> None:
        self.ctx = ctx
        self.name = name
        # Sanitise the name for use as a filename.
        safe = name.replace("/", "-").replace(" ", "_")
        self.log_file = ctx.log_dir / f"{safe}.log"
        self.log_file.parent.mkdir(parents=True, exist_ok=True)

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
                        f"without sentinel {sentinel!r}"
                    )
                if status == "idle":
                    consecutive_idle += 1
                    if consecutive_idle >= STALL_IDLE_COUNT:
                        raise AssertionError(
                            f"agent idle for {consecutive_idle * 3}s+ "
                            f"without sentinel {sentinel!r}"
                        )
                else:
                    consecutive_idle = 0

            time.sleep(3)

        raise AssertionError(
            f"sentinel {sentinel!r} not seen in {timeout}s "
            f"(log: {self.log_file})"
        )


# ---------------------------------------------------------------------------
# Test runner
# ---------------------------------------------------------------------------

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


def run_test(
    ctx: RunContext,
    name: str,
    fn: Callable[[Test], None],
) -> TestResult:
    t = Test(ctx, name)
    print(f"  {name} ...", end="", flush=True)
    start = time.monotonic()
    try:
        fn(t)
        result = TestResult(name=name, passed=True, elapsed_s=time.monotonic() - start)
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

def test_full_workflow(t: Test, spec: BackendSpec) -> None:
    """new → wait → diff → apply (assert content) → log → info."""
    project = t.project(f"workflow-{spec.label}")
    name = t.sandbox(f"workflow-{spec.label}")
    exdir = spec.exchange_dir(name)
    prompt = f"echo smoke > output.txt && touch {exdir}/{SENTINEL}"

    r = t.run(
        "new", name, str(project),
        "--model", "haiku",
        "--prompt", prompt,
        "--yes",
        *spec.new_args(),
        *t.debug_new_flags,
        timeout=120,
    )
    t.assert_ok(r, "new")

    t.wait_for_sentinel(name, timeout=spec.sentinel_timeout(), stall_grace_secs=spec.sentinel_stall_grace())

    r = t.run("diff", name)
    t.assert_ok(r, "diff")
    t.assert_in("output.txt", r.stdout, "diff output")

    r = t.run("apply", name, "--yes")
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
    prompt = f"echo smoke > output.txt && touch {exdir}/{SENTINEL}"

    r = t.run(
        "new", name, str(project),
        "--model", "haiku",
        "--prompt", prompt,
        "--yes",
        *spec.new_args(),
        *t.debug_new_flags,
        timeout=120,
    )
    t.assert_ok(r, "new")
    t.wait_for_sentinel(name, timeout=spec.sentinel_timeout(), stall_grace_secs=spec.sentinel_stall_grace())

    # restart = stop + start internally.  A new prompt with a different sentinel
    # proves the agent ran successfully with injected credentials after restart.
    # The prompt writes to the work copy so diff/apply can verify.
    sentinel2 = "done2"
    prompt2 = f"echo restarted > output2.txt && touch {exdir}/{sentinel2}"
    r = t.run("restart", name, "--prompt", prompt2, timeout=120)
    t.assert_ok(r, "restart")

    # Restart adds stop+start overhead on top of model inference, so allow extra time.
    t.wait_for_sentinel(name, sentinel=sentinel2, timeout=spec.sentinel_timeout() + 60, stall_grace_secs=spec.sentinel_stall_grace())

    # Verify diff shows the restarted agent's output
    r = t.run("diff", name)
    t.assert_ok(r, "diff after restart")
    t.assert_in("output2.txt", r.stdout, "diff after restart")

    # Apply and verify the file lands in the project directory
    r = t.run("apply", name, "--yes")
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
    prompt = f"echo smoke > clone-output.txt && touch {exdir}/{SENTINEL}"

    r = t.run(
        "new", name_a, str(project),
        "--model", "haiku",
        "--prompt", prompt,
        "--yes",
        *spec.new_args(),
        *t.debug_new_flags,
        timeout=120,
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
        data: dict = json.loads(r.stdout)
        ok: bool = data.get("ok", False)
        note = ""
        for check in data.get("checks", []):
            if not check.get("ok"):
                note = check.get("message", "check failed")
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
        r = subprocess.run(
            [ctx.yoloai_bin, "system", "build", "--backend", daemon],
            timeout=600,
        )
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

    Logs are written to ctx.log_dir (~/.yoloai/smoke-logs/<run_id>/) and are
    never deleted here — they persist until the user cleans them up manually.
    """
    if ctx.sandboxes:
        print(f"\nCleaning up {len(ctx.sandboxes)} sandbox(es)...")
        for name in ctx.sandboxes:
            subprocess.run(
                [ctx.yoloai_bin, "destroy", "--yes", name],
                capture_output=True, timeout=30,
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

    if skipped:
        print("\nSkipped tests:")
        for r in skipped:
            print(f"  SKIP  {r.name}: {r.reason}")


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
    return parser.parse_args()


def find_yoloai() -> Optional[str]:
    # Smoke test must use the locally built binary from the repo
    if Path("./yoloai").is_file():
        return "./yoloai"
    return None


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

    run_id = f"smoke-{int(time.time())}"
    tmpdir = Path(tempfile.mkdtemp(prefix="yoloai-smoke-"))
    log_dir = Path.home() / ".yoloai" / "smoke-logs" / run_id
    log_dir.mkdir(parents=True, exist_ok=True)
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
    atexit.register(cleanup, ctx)

    is_linux = sys.platform.startswith("linux")
    if ctx.full:
        matrix = FULL_LINUX_BACKENDS if is_linux else FULL_MACOS_BACKENDS
    else:
        matrix = BASE_LINUX_BACKENDS if is_linux else BASE_MACOS_BACKENDS

    # Build the full list of specs to check: default backend + matrix (deduped).
    matrix_labels = {s.label for s in matrix}
    all_specs = [DEFAULT_BACKEND] + [
        s for s in matrix if s.label != DEFAULT_BACKEND.label
    ]

    tier = "full" if ctx.full else "base"
    print(f"yoloai smoke test  run={run_id}")
    print(f"host={'linux' if is_linux else 'macos'}  tier={tier}")
    print(f"binary={yoloai_bin}")
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
            result = run_test(ctx, test_name, lambda t, s=spec: test_fn(t, s))
            if not result.passed and not result.skipped and spec.retries > 0:
                for attempt in range(spec.retries):
                    print(f"      Retrying {test_name} (attempt {attempt + 1}/{spec.retries})...")
                    ctx.results.pop()
                    # Destroy sandboxes created during the failed attempt so
                    # the retry can create fresh ones with the same names.
                    _destroy_retry_sandboxes(ctx, sandbox_count_before)
                    result = run_test(ctx, test_name, lambda t, s=spec: test_fn(t, s))
                    if result.passed:
                        break

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

    failed = [r for r in ctx.results if not r.passed and not r.skipped]
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
