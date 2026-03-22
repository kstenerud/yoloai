#!/usr/bin/env python3
"""End-to-end smoke tests for yoloai against real agents.

Run with: python3 scripts/smoke_test.py [--limited]
Or via:   make smoketest / make smoketest-limited

Requires ANTHROPIC_API_KEY and configured backends.
See docs/dev/plans/smoke-test-redesign.md for the full design.
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
VM_TIMEOUT = 180        # seconds: VM boot + agent startup (includes nested KVM overhead)
CMD_TIMEOUT = 60        # seconds: individual yoloai commands


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

    @property
    def is_seatbelt(self) -> bool:
        """Seatbelt uses the host filesystem; exchange dir is a host path."""
        return self.os == "mac" and self.isolation == "container" and self.backend is None

    def exchange_dir(self, sandbox_name: str) -> str:
        """Return the exchange dir path as seen from inside the sandbox."""
        if self.is_seatbelt:
            return str(Path.home() / ".yoloai" / "sandboxes" / sandbox_name / "files")
        return "/yoloai/files"

    def sentinel_timeout(self) -> int:
        return VM_TIMEOUT if self.is_vm else DEFAULT_TIMEOUT

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


class SkipTest(Exception):
    pass


@dataclass
class RunContext:
    yoloai_bin: str
    tmpdir: Path
    run_id: str
    fixture_dir: Path
    limited: bool
    sandboxes: list[str] = field(default_factory=list)
    results: list[TestResult] = field(default_factory=list)


# ---------------------------------------------------------------------------
# Backend matrices
# ---------------------------------------------------------------------------

LINUX_BACKENDS: list[BackendSpec] = [
    BackendSpec("linux", "container",          "docker", "docker",
                check_backend="docker"),
    BackendSpec("linux", "container",          "podman", "podman",
                check_backend="podman"),
    BackendSpec("linux", "container-enhanced", None,     "cenhanced",
                check_backend="docker"),
    BackendSpec("linux", "vm",                 None,     "vm",
                check_backend="containerd", is_vm=True, check_isolation="vm"),
    BackendSpec("linux", "vm-enhanced",        None,     "vmenhanced",
                check_backend="containerd", is_vm=True, check_isolation="vm-enhanced"),
]

MACOS_BACKENDS: list[BackendSpec] = [
    BackendSpec("linux", "container", "docker", "docker",
                check_backend="docker"),
    BackendSpec("linux", "container", "podman", "podman",
                check_backend="podman"),
    BackendSpec("linux", "vm",        None,     "linux-vm",
                check_backend="tart",   is_vm=True),
    BackendSpec("mac",   "container", None,     "seatbelt",
                check_backend="seatbelt"),
    BackendSpec("mac",   "vm",        None,     "mac-vm",
                check_backend="tart",   is_vm=True),
]

# Required for non-matrix tests (T2–T6). Must be available on both platforms.
DEFAULT_BACKEND = BackendSpec(
    "linux", "container", "docker", "docker", check_backend="docker"
)


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
        self.log_file = ctx.tmpdir / "logs" / f"{safe}.log"
        self.log_file.parent.mkdir(parents=True, exist_ok=True)

    def run(self, *args: str, timeout: int = CMD_TIMEOUT) -> subprocess.CompletedProcess[str]:
        """Run a yoloai subcommand, logging the invocation and output."""
        cmd = [self.ctx.yoloai_bin, *args]
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

    def wait_for_sentinel(
        self,
        sandbox_name: str,
        sentinel: str = SENTINEL,
        timeout: int = DEFAULT_TIMEOUT,
    ) -> None:
        """Poll `yoloai files ls` until `sentinel` appears as an exact line."""
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            r = self.run("files", sandbox_name, "ls", timeout=15)
            if r.returncode == 0:
                lines = [line.strip() for line in r.stdout.splitlines()]
                if sentinel in lines:
                    return
            time.sleep(3)
        raise AssertionError(
            f"sentinel {sentinel!r} not seen in {timeout}s "
            f"(log: {self.log_file})"
        )


# ---------------------------------------------------------------------------
# Test runner
# ---------------------------------------------------------------------------

def run_test(
    ctx: RunContext,
    name: str,
    fn: Callable[[Test], None],
) -> TestResult:
    t = Test(ctx, name)
    print(f"  {name} ...", end="", flush=True)
    try:
        fn(t)
        result = TestResult(name=name, passed=True)
        print(" PASS")
    except SkipTest as e:
        result = TestResult(name=name, skipped=True, reason=str(e))
        print(f"\n  *** SKIP [{name}]: {e}")
    except AssertionError as e:
        result = TestResult(name=name, passed=False, reason=str(e))
        print(f"\n  *** FAIL [{name}]")
        for line in str(e).splitlines():
            print(f"      {line}")
        print(f"      log: {t.log_file}")
    except subprocess.TimeoutExpired as e:
        result = TestResult(name=name, passed=False, reason=f"command timed out: {e}")
        print(f"\n  *** FAIL [{name}]: command timed out")
        print(f"      log: {t.log_file}")
    except Exception as e:
        result = TestResult(name=name, passed=False, reason=f"{type(e).__name__}: {e}")
        print(f"\n  *** ERROR [{name}]: {type(e).__name__}: {e}")
        print(f"      log: {t.log_file}")
    ctx.results.append(result)
    return result


def skip_test(ctx: RunContext, name: str, reason: str) -> TestResult:
    result = TestResult(name=name, skipped=True, reason=reason)
    print(f"  *** SKIP [{name}]: {reason}")
    ctx.results.append(result)
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
        timeout=120,
    )
    t.assert_ok(r, "new")

    t.wait_for_sentinel(name, timeout=spec.sentinel_timeout())

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
    """new → wait → restart with new prompt → wait for new sentinel.

    Uses `yoloai restart --prompt` (= stop + start internally) to verify
    credential re-injection after a container restart.
    """
    project = t.project("stop-start")
    name = t.sandbox("stop-start")
    exdir = spec.exchange_dir(name)
    prompt = f"touch {exdir}/{SENTINEL}"

    r = t.run(
        "new", name, str(project),
        "--model", "haiku",
        "--prompt", prompt,
        "--yes",
        *spec.new_args(),
        timeout=120,
    )
    t.assert_ok(r, "new")
    t.wait_for_sentinel(name, timeout=spec.sentinel_timeout())

    # restart = stop + start internally.  A new prompt with a different sentinel
    # proves the agent ran successfully with injected credentials after restart.
    sentinel2 = "done2"
    prompt2 = f"touch {exdir}/{sentinel2}"
    r = t.run("restart", name, "--prompt", prompt2, timeout=120)
    t.assert_ok(r, "restart")

    # Restart adds stop+start overhead on top of model inference, so allow extra time.
    t.wait_for_sentinel(name, sentinel=sentinel2, timeout=spec.sentinel_timeout() + 60)


def test_files_exchange(t: Test, spec: BackendSpec) -> None:
    """put → ls → get, without starting an agent."""
    project = t.project("files")
    name = t.sandbox("files")

    r = t.run(
        "new", name, str(project),
        "--no-start", "--yes",
        *spec.new_args(),
        timeout=60,
    )
    t.assert_ok(r, "new --no-start")

    src_file = t.ctx.tmpdir / "somefile.txt"
    src_file.write_text("hello from smoke test\n")

    r = t.run("files", name, "put", str(src_file))
    t.assert_ok(r, "files put")

    r = t.run("files", name, "ls")
    t.assert_ok(r, "files ls")
    lines = [line.strip() for line in r.stdout.splitlines()]
    if "somefile.txt" not in lines:
        raise AssertionError(
            f"somefile.txt not found in files ls output: {r.stdout!r}"
        )

    got_dir = t.ctx.tmpdir / "got"
    got_dir.mkdir(exist_ok=True)
    r = t.run("files", name, "get", "somefile.txt", "-o", str(got_dir))
    t.assert_ok(r, "files get")

    got_file = got_dir / "somefile.txt"
    if not got_file.exists():
        raise AssertionError("somefile.txt not found after files get")
    if "hello from smoke test" not in got_file.read_text():
        raise AssertionError(
            f"somefile.txt content mismatch: {got_file.read_text()!r}"
        )


def test_overlay(t: Test) -> None:
    """Overlay workdir: new → wait → diff → apply.

    Always uses docker/container.  container-enhanced (gVisor) is incompatible
    with overlayfs.  CAP_SYS_ADMIN is added automatically by yoloai when overlay
    dirs are present.
    """
    overlay_spec = BackendSpec(
        "linux", "container", "docker", "overlay", check_backend="docker"
    )
    project = t.project("overlay")
    name = t.sandbox("overlay")
    exdir = "/yoloai/files"
    prompt = f"echo smoke > output.txt && touch {exdir}/{SENTINEL}"

    r = t.run(
        "new", name, f"{project}:overlay",
        "--model", "haiku",
        "--prompt", prompt,
        "--yes",
        *overlay_spec.new_args(),
        timeout=120,
    )
    t.assert_ok(r, "new with :overlay workdir")

    t.wait_for_sentinel(name, timeout=DEFAULT_TIMEOUT)

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
        timeout=120,
    )
    t.assert_ok(r, "new sandbox A")
    t.wait_for_sentinel(name_a, timeout=spec.sentinel_timeout())

    name_b = f"{t.ctx.run_id}-clone-b"
    r = t.run("clone", name_a, name_b, timeout=CMD_TIMEOUT)
    t.assert_ok(r, "clone")
    # Register B after clone succeeds, before assertions, so it is always destroyed.
    t.ctx.sandboxes.append(name_b)

    r = t.run("diff", name_b)
    t.assert_ok(r, "diff on clone")
    t.assert_in("clone-output.txt", r.stdout, "cloned diff output")


def test_reset(t: Test, spec: BackendSpec) -> None:
    """new → wait → diff (non-empty) → reset → diff (empty).

    reset is synchronous; no additional wait is needed after it returns.
    """
    project = t.project("reset")
    name = t.sandbox("reset")
    exdir = spec.exchange_dir(name)
    prompt = f"echo smoke > reset-me.txt && touch {exdir}/{SENTINEL}"

    r = t.run(
        "new", name, str(project),
        "--model", "haiku",
        "--prompt", prompt,
        "--yes",
        *spec.new_args(),
        timeout=120,
    )
    t.assert_ok(r, "new")
    t.wait_for_sentinel(name, timeout=spec.sentinel_timeout())

    r = t.run("diff", name)
    t.assert_ok(r, "diff before reset")
    t.assert_in("reset-me.txt", r.stdout, "diff before reset")

    r = t.run("reset", name, timeout=120)
    t.assert_ok(r, "reset")

    r = t.run("diff", name)
    t.assert_ok(r, "diff after reset")
    t.assert_in("No changes", r.stdout, "diff after reset")


# ---------------------------------------------------------------------------
# Prerequisites check
# ---------------------------------------------------------------------------

def check_prerequisites(
    ctx: RunContext,
    backends: list[BackendSpec],
) -> dict[str, PrereqResult]:
    """Run `yoloai system check` for each unique (daemon, isolation) pair; return per-spec results."""
    print("Checking prerequisites...\n")

    # Deduplicate by (check_backend, check_isolation) so vm and vm-enhanced are
    # checked separately (each needs its own isolation validation).
    unique_keys: set[tuple[str, str]] = {
        (spec.check_backend, spec.check_isolation) for spec in backends
    }
    check_results: dict[tuple[str, str], tuple[bool, str]] = {}

    for daemon, isolation in sorted(unique_keys):
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
            check_results[(daemon, isolation)] = (ok, note)
        except subprocess.TimeoutExpired:
            check_results[(daemon, isolation)] = (False, "system check timed out")
        except (json.JSONDecodeError, KeyError) as e:
            check_results[(daemon, isolation)] = (False, f"could not parse system check output: {e}")
        except FileNotFoundError:
            check_results[(daemon, isolation)] = (False, "yoloai binary not found")

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
    """Destroy all tracked sandboxes and remove the temp dir."""
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
        "--limited",
        action="store_true",
        help=(
            "Warn about missing backends instead of aborting. "
            "Tests requiring unavailable backends are loudly skipped."
        ),
    )
    return parser.parse_args()


def find_yoloai() -> Optional[str]:
    for candidate in [
        shutil.which("yoloai"),
        str(Path.home() / "bin" / "yoloai"),
        "./yoloai",
    ]:
        if candidate and Path(candidate).is_file():
            return candidate
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

    tmpdir = Path(tempfile.mkdtemp(prefix="yoloai-smoke-"))
    run_id = f"smoke-{int(time.time())}"
    fixture_dir = create_fixture(tmpdir)

    ctx = RunContext(
        yoloai_bin=yoloai_bin,
        tmpdir=tmpdir,
        run_id=run_id,
        fixture_dir=fixture_dir,
        limited=args.limited,
    )
    atexit.register(cleanup, ctx)

    is_linux = sys.platform.startswith("linux")
    matrix = LINUX_BACKENDS if is_linux else MACOS_BACKENDS

    # Build the full list of specs to check: default backend + matrix (deduped).
    matrix_labels = {s.label for s in matrix}
    all_specs = [DEFAULT_BACKEND] + [
        s for s in matrix if s.label != DEFAULT_BACKEND.label
    ]

    print(f"yoloai smoke test  run={run_id}")
    print(f"host={'linux' if is_linux else 'macos'}  limited={args.limited}")
    print(f"binary={yoloai_bin}\n")

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
        if args.limited:
            print("WARNING: some backends unavailable (will skip their tests):")
            for label in unavailable_labels:
                print(f"  {label}: {preq[label].note}")
            if setup_tip:
                print(setup_tip)
            print()
        else:
            print(
                "ERROR: some backends are unavailable. "
                "Use --limited to skip them and run with what is available:"
            )
            for label in unavailable_labels:
                print(f"  {label}: {preq[label].note}")
            if setup_tip:
                print(setup_tip)
            return 1

    # -------------------------------------------------------------------------
    # Non-matrix tests (T2–T6) — run once on docker/linux/container
    # -------------------------------------------------------------------------

    print("Non-matrix tests (docker/linux/container):")
    run_test(ctx, "stop_start",     lambda t: test_stop_start(t, DEFAULT_BACKEND))
    run_test(ctx, "files_exchange", lambda t: test_files_exchange(t, DEFAULT_BACKEND))
    run_test(ctx, "clone",          lambda t: test_clone(t, DEFAULT_BACKEND))
    run_test(ctx, "reset",          lambda t: test_reset(t, DEFAULT_BACKEND))

    if is_linux:
        run_test(ctx, "overlay", lambda t: test_overlay(t))

    # -------------------------------------------------------------------------
    # T1: full_workflow — run across the backend matrix
    # -------------------------------------------------------------------------

    print("\nBackend matrix (full_workflow):")
    for spec in matrix:
        test_name = f"full_workflow/{spec.label}"
        pr = preq.get(spec.label)
        if pr is None or not pr.available:
            reason = pr.note if pr else "not in prereq results"
            skip_test(ctx, test_name, reason)
            continue
        run_test(ctx, test_name, lambda t, s=spec: test_full_workflow(t, s))

    print_summary(ctx.results)

    failed = [r for r in ctx.results if not r.passed and not r.skipped]
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
