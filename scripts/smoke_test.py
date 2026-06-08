#!/usr/bin/env python3
"""End-to-end smoke tests for yoloai against real agents.

Run with: python3 scripts/smoke_test.py [--full]
Or via:   make smoketest / make smoketest-full

Base tier (default): docker + container-privileged + containerd-vm on Linux,
                     docker + tart on macOS.
Full tier (--full):  all backends including podman, gVisor, vm-enhanced.

Tests that don't need a real agent (files exchange, reset, start-after-done,
overlay) have been moved to Go integration tests (sandbox/integration_test.go,
internal/cli/integration_test.go).

Requires ANTHROPIC_API_KEY and configured backends.
See docs/contributors/design/plans/smoke-test-v2.md for the full design.
"""
from __future__ import annotations

import argparse
import atexit
import contextlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable, Optional

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

# A reusable no-op gate for container backends (no host-wide VM cap to honor).
# nullcontext does nothing on enter/exit, so it is safe to share across threads.
_NULL_GATE = contextlib.nullcontext()

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

# Host data-dir layout (D60 bifurcation): the top dir defaults to ~/.yoloai and
# splits into ~/.yoloai/library (sandboxes, profiles, config, ...) and
# ~/.yoloai/cli (CLI app state). Sandbox state lives under the library namespace.
# Resolve Path.home() lazily on each call so tests can monkeypatch the home dir.


def library_dir() -> Path:
    """TOP/library — the library data dir holding sandboxes, config, etc."""
    return Path.home() / ".yoloai" / "library"


def sandbox_state_dir(name: str) -> Path:
    """Host path to a sandbox's state dir (TOP/library/sandboxes/<name>)."""
    return library_dir() / "sandboxes" / name


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
            return str(sandbox_state_dir(sandbox_name) / "files")
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
    # Sandbox names this attempt created (copied from the Test's local_sandboxes).
    # Used by the parallel runner to destroy exactly this attempt's sandboxes
    # before a retry, instead of slicing the shared ctx.sandboxes list (which is
    # unsafe when other backends are running concurrently).
    sandboxes: list[str] = field(default_factory=list)


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
    # Concurrency (Phase 2). jobs=0 → auto (one worker per matrix spec); jobs=1
    # forces the original serial behavior. vm_concurrency caps how many VM-backed
    # backends run at once — Tart enforces a hard 2-VM macOS limit and concurrent
    # containerd QEMU VMs raise peak disk, so VM specs share a semaphore while
    # container specs run unthrottled.
    jobs: int = 0
    vm_concurrency: int = 2
    # Monotonic sandbox-name counter (allocated under state_lock). Appended to
    # every sandbox name so no two are string prefixes of one another — see
    # Test.sandbox() for why the containerd-vm (Kata) backend requires this.
    name_seq: int = 0
    # Guards mutation of the shared sandboxes/results lists, the name counter,
    # and the single JUnit file handle; print_lock serializes each test's output
    # block so concurrent backends don't interleave their PASS/FAIL lines.
    state_lock: threading.Lock = field(default_factory=threading.Lock)
    print_lock: threading.Lock = field(default_factory=threading.Lock)


# ---------------------------------------------------------------------------
# Backend matrices
# ---------------------------------------------------------------------------

# Base tier: fast, reliable backends for PR gates and nightly smoke.
BASE_LINUX_BACKENDS: list[BackendSpec] = [
    BackendSpec("linux", "container",          "docker", "docker",
                check_backend="docker", retries=1),
    BackendSpec("linux", "container-privileged", "docker", "docker-priv",
                check_backend="docker", retries=1),
    BackendSpec("linux", "vm",                 None,     "containerd-vm",
                check_backend="containerd", is_vm=True, check_isolation="vm",
                sentinel_timeout_override=QEMU_TIMEOUT, stall_grace_secs=120,
                retries=1),
]

BASE_MACOS_BACKENDS: list[BackendSpec] = [
    BackendSpec("linux", "container", "docker", "docker",
                check_backend="docker", retries=1),
    # Docker Desktop / OrbStack / Podman Machine run --privileged inside their
    # Linux VM, so privileged + dind work on macOS exactly as on Linux (verified
    # on OrbStack / Apple Silicon). Mirrors the Linux base tier.
    BackendSpec("linux", "container-privileged", "docker", "docker-priv",
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
    BackendSpec("linux", "container-privileged", "docker", "docker-priv",
                check_backend="docker", retries=1),
    # Rootless Podman privileged maps the host user onto yoloai via
    # keep-id:uid=1001 (see podman.go Create): the agent gets sudo + docker for
    # dind, and host-written 0600 files stay readable. Verified on this Linux host:
    # new + docker-in-docker + --network-isolated all pass.
    BackendSpec("linux", "container-privileged", "podman", "podman-priv",
                check_backend="podman", retries=1),
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
    # container-privileged runs on macOS via the container backend's Linux VM
    # (Docker Desktop / OrbStack / Podman Machine). Mirrors the Linux spec so
    # the dind + isolation_check matrix tests run here too. Verified on OrbStack
    # / Apple Silicon: new + docker-in-docker + --network-isolated all pass.
    BackendSpec("linux", "container-privileged", "docker", "docker-priv",
                check_backend="docker", retries=1),
    # Podman privileged also works on macOS — verified on a rootless Podman
    # Machine (Apple Silicon): new + docker-in-docker + --network-isolated all
    # pass. (Also in the Linux matrix, via keep-id:uid=1001.)
    BackendSpec("linux", "container-privileged", "podman", "podman-priv",
                check_backend="podman", retries=1),
]

# Required for non-matrix tests. Must be available on both platforms.
DEFAULT_BACKEND = BackendSpec(
    "linux", "container", "docker", "docker", check_backend="docker"
)

# Backends whose host OS is fixed by the underlying daemon: seatbelt/tart are
# macOS-only, containerd (Kata) is Linux-only. docker/podman bridge to a VM on the
# foreign host (Docker Desktop / Podman Machine), so they run on both and are
# absent here. Keyed by check_backend → required host OS.
HOST_OS_LOCKED: dict[str, str] = {
    "seatbelt": "mac",
    "tart": "mac",
    "containerd": "linux",
}

# A backend's daemon may run on both hosts while its *isolation mode* is host-
# specific, so the cross-host matrix schedules it on only one side. This note
# explains such an omission (keyed by isolation) so a backend that's silently
# absent from this host's matrix still gets a reason in the end-of-run summary.
#
# Only container-enhanced (gVisor) remains here. As of D71 yoloai *rejects* it on
# macOS hosts entirely — the R-DD spike found it isn't turn-key on either provider
# (Docker Desktop's engine fails when runsc is registered; OrbStack's /tmp chroot
# collision; a nested cgroup-v2 hazard). gVisor is Linux-primary; see
# docs/contributors/design/plans/setup-gvisor.md. So on a macOS run the Linux-only
# docker-cenhanced backend is uncovered with that reason. container-privileged is
# NOT listed: it runs on macOS via the Linux VM and is scheduled in the mac matrices.
ISOLATION_HOST_NOTE: dict[str, str] = {
    "container-enhanced": "gVisor (container-enhanced) is Linux-only — not supported "
    "on macOS (the macOS Docker VMs can't run runsc turn-key; D71). Use a Linux host.",
}

# Tests restricted to --full tier.
FULL_ONLY_TESTS = {"clone"}

def is_full_test(name: str) -> bool:
    """Return True if this test requires --full."""
    return name.split("/")[0] in FULL_ONLY_TESTS


# Labels of the tests that fan out across the backend matrix (one slot per
# backend). A bare `--test <label>` selects every <label>/<backend>; the full
# `--test <label>/<backend>` pins one. Module-level (with the two predicates
# below) so the --test/--backend filter logic is unit-testable.
MATRIX_TEST_LABELS = ("stop_start", "tag_transfer", "isolation_check", "dind")


def should_run_under_filter(test_name: str, test_filter: Optional[list[str]]) -> bool:
    """Whether a "<test>/<backend>" (or bare "<test>") name passes --test.

    A None filter matches everything. Otherwise an entry matches either the full
    name or the bare label, so `--test stop_start` runs every stop_start/<backend>
    (as documented in --help) while `--test stop_start/tart` pins one. Must mirror
    spec_needed_for_filters so scheduling and prereq selection never disagree (DF19).
    """
    if test_filter is None:
        return True
    label = test_name.split("/", 1)[0]
    return test_name in test_filter or label in test_filter


def spec_needed_for_filters(
    spec_label: str,
    test_filter: Optional[list[str]],
    backend_filter: Optional[list[str]],
) -> bool:
    """Whether a backend must be prereq-checked/built given the active filters.

    Mirrors should_run_under_filter: a bare `--test <label>` needs every backend,
    `--test <label>/<backend>` needs only that one, and `--backend <label>` needs
    that backend. With no filters every backend is needed.
    """
    if not (test_filter or backend_filter):
        return True
    if backend_filter and spec_label in backend_filter:
        return True
    if test_filter:
        for label in MATRIX_TEST_LABELS:
            if label in test_filter or f"{label}/{spec_label}" in test_filter:
                return True
    return False


# Structural applicability of matrix tests. Some (test × backend) pairings are
# impossible by construction — no host change makes them runnable — so the runner
# excludes them from scheduling instead of emitting a misleading SKIP, mirroring
# how the Linux matrix simply never lists tart/seatbelt. Conditional gating (host
# prereqs, --full tier) stays a runtime skip; only capability-level impossibility
# belongs here.

def dind_applies(spec: BackendSpec) -> bool:
    """dind needs a nested dockerd, which requires the --privileged caps and shared
    mount propagation that only the container-privileged isolation mode grants."""
    return spec.isolation == "container-privileged"


# Docker contexts that map to a distinct local daemon worth cycling under
# --all-docker-providers. Each has its own image store and VM kernel, so the
# docker-backed tiers behave differently across them (see dind-storage-drivers).
KNOWN_DOCKER_PROVIDERS = ("orbstack", "desktop-linux")


def docker_provider_candidates(context_names: list[str], active: str) -> list[str]:
    """Pick and order the docker provider contexts to cycle, active-first.

    From the contexts that exist on the machine, keep the known multi-daemon
    providers (OrbStack, Docker Desktop). If none are present (e.g. a Linux host
    with only `default`), fall back to the single active context — so the
    --all-docker-providers path collapses to one run. Active-first avoids an
    unnecessary context switch and tests the daemon the user is already on."""
    known = [c for c in KNOWN_DOCKER_PROVIDERS if c in context_names]
    if not known:
        return [active] if active else []
    if active in known:
        return [active] + [c for c in known if c != active]
    return known


def isolation_check_applies(spec: BackendSpec) -> bool:
    """--network-isolated blocks egress via iptables rules in the container netns —
    a capability of the container daemons (docker/podman/containerd). gVisor's
    (-enhanced) netstack doesn't honor those rules and the seatbelt/tart backends
    have no iptables netns at all, so those pairings can never enforce isolation."""
    return (
        spec.check_backend in {"docker", "podman", "containerd"}
        and not spec.isolation.endswith("-enhanced")
    )


def uncovered_backends(
    other_os_matrix: list[BackendSpec], this_os_matrix: list[BackendSpec]
) -> list[BackendSpec]:
    """Backends present in the other host's tier matrix but not this host's.

    These can't be covered by the current run — some are OS-locked daemons
    (seatbelt/tart/containerd), others are daemons that run on both hosts but whose
    isolation mode the matrix only schedules on the other side (docker-cenhanced,
    whose gVisor runtime yoloai blocks on macOS). Either way the omission must not
    be silent, so they're reported as a distinct end-of-run group with a per-backend
    reason (see uncovered_reason). Deduped by label; backends scheduled here
    (docker/podman, and now docker-priv on both hosts) never appear."""
    here = {s.label for s in this_os_matrix}
    out: list[BackendSpec] = []
    seen: set[str] = set()
    for spec in other_os_matrix:
        if spec.label not in here and spec.label not in seen:
            seen.add(spec.label)
            out.append(spec)
    return out


def uncovered_reason(spec: BackendSpec, other_host_label: str) -> str:
    """Explain why an uncovered backend can't run on this host.

    OS-locked daemons need the other hardware; daemons that bridge both hosts but
    carry a host-specific isolation mode get the isolation note."""
    if spec.check_backend in HOST_OS_LOCKED:
        return f"requires a {other_host_label} host"
    return ISOLATION_HOST_NOTE.get(
        spec.isolation, f"only scheduled on the {other_host_label} matrix"
    )


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
        # Detach stdin from the controlling terminal: interactive subcommands
        # (notably `exec`) put the inherited tty into raw mode via WithTerminal,
        # and the VM `-it` exec path doesn't fully restore it — leaving the
        # harness's own terminal stair-stepping. These invocations are never
        # interactive, so DEVNULL makes WithTerminal skip all terminal handling.
        result = subprocess.run(
            cmd, capture_output=True, text=True, timeout=timeout,
            stdin=subprocess.DEVNULL,
        )
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
        """Allocate a globally-unique sandbox name and register it for cleanup.

        The trailing sequence number guarantees no sandbox name is a string
        prefix of another within a run. This is load-bearing on the
        containerd-vm (Kata) backend: the Kata shim resolves a sandbox from the
        container ID by *prefix*, so two coexisting sandboxes whose names are
        prefix-related (e.g. ...-containerd-vm vs ...-containerd-vmenhanced)
        make its lookup ambiguous and `create task` fails with "more than one
        sandbox exists with the provided prefix". Serial runs never had both
        backends alive at once; the parallel matrix does. The "-NNN" suffix
        breaks the prefix relationship (the plain name continues with "-" where
        the enhanced one continues with "e"). See
        docs/contributors/backend-idiosyncrasies.md.
        """
        with self.ctx.state_lock:
            seq = self.ctx.name_seq
            self.ctx.name_seq += 1
            name = f"{self.ctx.run_id}-{label}-{seq:03d}"
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

    def project_git(self, label: str) -> Path:
        """Like project(), but initialize the copy as a git repo with one commit.

        Used by tests that exercise the git-host apply path — commit replay via
        format-patch/am and `apply --tags` tag transfer — which the plain
        project() fixture can't reach (a non-git target makes apply fall back to
        --no-commit, and tag transfer is skipped entirely)."""
        dest = self.project(label)
        (dest / ".smoke-baseline").write_text("baseline\n")
        for args in (
            ["init", "-q", "-b", "main"],
            ["config", "user.email", "smoke@test.local"],
            ["config", "user.name", "smoke"],
            ["add", "-A"],
            ["commit", "-qm", "baseline"],
        ):
            subprocess.run(
                ["git", "-C", str(dest), *args],
                check=True, capture_output=True, text=True,
            )
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

# Files and directories copied out of TOP/library/sandboxes/<name>/ when a test
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

    Sandbox source files under TOP/library/sandboxes/ are often mode 600/700 and
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
    env_path = sandbox_state_dir(sandbox_name) / "environment.json"
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
    src = sandbox_state_dir(sandbox_name) / "logs" / "monitor.jsonl"
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
    cli_jsonl = sandbox_state_dir(sandbox_name) / "logs" / "cli.jsonl"
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
    """Copy diagnostic state from TOP/library/sandboxes/<sandbox_name>/ to
    dest_parent/<sandbox_name>/. Returns the target dir, or None if the source
    doesn't exist (e.g. the test failed before the sandbox was created).
    """
    src = sandbox_state_dir(sandbox_name)
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
_IDIO_DOC = "docs/contributors/backend-idiosyncrasies.md"


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
        "tart :copy diff after restart shows 'No changes' (baseline race)",
        r"diff after restart: expected .* in output",
        "tart-copy-diff-after-restart-shows-no-changes",
        "the agent wrote into the work dir before the host's baseline commit, so "
        "git add -A baked the output into the baseline; get_working_dir must gate "
        "agent launch on a committed HEAD in copy mode",
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


def scan_fingerprints(attempt_dir: Path, extra_text: str = "") -> list[FingerprintHit]:
    """Scan preserved artifacts for known fatal fingerprints.

    Returns hits in FINGERPRINTS order (most-specific first). At most one hit
    per fingerprint — the first matching line found across the artifact files.

    `extra_text` (the harness failure reason) is scanned as an additional
    source. Host-side assertion failures — e.g. the tart :copy diff-after-
    restart race — leave no fatal line in the guest artifacts, so the reason
    is the only place their signature appears.
    """
    compiled = [(fp, re.compile(fp.pattern, re.IGNORECASE)) for fp in FINGERPRINTS]
    files = _autopsy_artifact_files(attempt_dir)
    # (source_name, text) pairs; the harness reason scans first so a host-side
    # signature wins the headline over a generic guest-log catch-all.
    sources: list[tuple[str, str]] = []
    if extra_text:
        sources.append(("harness-reason", extra_text))
    for f in files:
        try:
            sources.append((f.name, f.read_text(errors="replace")))
        except OSError:
            continue
    hits: list[FingerprintHit] = []
    seen: set[str] = set()
    for fp, rx in compiled:
        if fp.label in seen:
            continue
        for source_name, text in sources:
            for line in text.splitlines():
                if rx.search(line):
                    hits.append(FingerprintHit(fp, line.strip()[:200], source_name))
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
    """test_name is like 'stop_start/tart'; keep the slash as a subdir."""
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

    Reads from the still-live TOP/library/sandboxes/<name>/ dirs (preservation
    only happens on failure, and cleanup runs at exit). Best-effort: any error
    just skips the snapshot — a missing baseline only means no diff later."""
    if not sandbox_names:
        return
    logs_dirs = [sandbox_state_dir(n) / "logs" for n in sandbox_names]
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
    environment = _read_environment(sandbox_state_dir(sandbox_names[0]))
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
    hits = scan_fingerprints(attempt_dir, result.reason)
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


def _destroy_named_sandboxes(ctx: RunContext, names: list[str]) -> None:
    """Destroy the given sandboxes and drop them from the shared cleanup list.

    Thread-safe: a backend's retry/VM cleanup destroys exactly the names its own
    attempt created (TestResult.sandboxes), never a positional slice of the
    shared ctx.sandboxes — concurrent backends append to that list, so slicing
    would delete a sibling's sandbox.
    """
    for name in names:
        subprocess.run(
            [ctx.yoloai_bin, "destroy", "--yes", name],
            capture_output=True,
            timeout=30,
        )
    with ctx.state_lock:
        ctx.sandboxes = [n for n in ctx.sandboxes if n not in names]


def _emit(ctx: RunContext, text: str) -> None:
    """Print a block atomically so concurrent backends don't interleave output."""
    with ctx.print_lock:
        print(text)


def _record_result(ctx: RunContext, result: TestResult) -> None:
    """Append a terminal result to the shared list + JUnit, under the state lock.

    Only the *final* result of a test (after any retries) is recorded — run_test
    no longer records, so intermediate failed attempts never reach the summary."""
    with ctx.state_lock:
        ctx.results.append(result)
        if ctx.junit:
            ctx.junit.write_testcase(result)


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
    # Output is buffered into `out` and emitted as one atomic block at the end
    # (see _emit). Under parallel matrix execution, multiple backends run their
    # tests concurrently; streaming `print(..., end="")` would interleave their
    # partial lines into garble. A single block per test keeps the transcript
    # readable regardless of how many backends are in flight.
    out: list[str] = []
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
            out.append(f"  {name} ... PASS  [probe: {' | '.join(probe_notes)}]")
        else:
            out.append(f"  {name} ... PASS")
    except SkipTest as e:
        result = TestResult(name=name, skipped=True, reason=str(e), elapsed_s=time.monotonic() - start)
        out.append(f"  *** SKIP [{name}]: {e}")
    except AssertionError as e:
        result = TestResult(name=name, passed=False, reason=str(e), elapsed_s=time.monotonic() - start)
        out.append(f"  *** FAIL [{name}]")
        for line in str(e).splitlines():
            out.append(f"      {line}")
        out.append(f"      log: {t.log_file}")
    except subprocess.TimeoutExpired as e:
        result = TestResult(name=name, passed=False, reason=f"command timed out: {e}", elapsed_s=time.monotonic() - start)
        out.append(f"  *** FAIL [{name}]: command timed out")
        out.append(f"      log: {t.log_file}")
    except Exception as e:
        result = TestResult(name=name, passed=False, reason=f"{type(e).__name__}: {e}", elapsed_s=time.monotonic() - start)
        out.append(f"  *** ERROR [{name}]: {type(e).__name__}: {e}")
        out.append(f"      log: {t.log_file}")
    # Record the sandbox names this attempt created so the caller can destroy
    # exactly them on retry/VM-cleanup, without slicing the shared ctx.sandboxes
    # list (unsafe when sibling backends append concurrently).
    result.sandboxes = list(t.local_sandboxes)
    # Preserve sandbox state on failure so the user can diagnose later.
    # cleanup() destroys all sandboxes at exit, so this must happen before
    # we return — and retries destroy the prior attempt's sandboxes, so the
    # copy happens per attempt rather than only at end-of-run.
    if not result.passed and not result.skipped and t.local_sandboxes:
        preserved = _preserve_failed_attempt(ctx, name, t.local_sandboxes, attempt)
        if preserved is not None:
            out.append(f"      preserved: {preserved}")
            autopsy = write_failure_autopsy(ctx, result, preserved)
            if autopsy is not None:
                if result.fingerprints:
                    out.append(f"      autopsy: {result.fingerprints[0]}")
                out.append(f"      details: {autopsy}")
    elif result.passed and t.local_sandboxes:
        # Snapshot last-good event surface for future failure diffs (Tier 3).
        # Must read the live sandbox dirs before cleanup destroys them.
        save_baseline(ctx, name, t.local_sandboxes)
    _emit(ctx, "\n".join(out))
    # Recording (ctx.results + JUnit) is the caller's job via _record_result,
    # so only the final attempt of a retried test reaches the summary.
    return result


def skip_test(ctx: RunContext, name: str, reason: str) -> TestResult:
    result = TestResult(name=name, skipped=True, reason=reason)
    _emit(ctx, f"  *** SKIP [{name}]: {reason}")
    _record_result(ctx, result)
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
         once instead of three times. Both seatbelt workflow legs (then the
         separate full_workflow + stop_start tests) flaked (run
         20260529-034737): the haiku agent dropped the
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
         `{work}` still runs in the cwd (test_stop_start asserts output.txt
         lands in the work dir after apply).
    """
    cmd = f"{work} && touch {exdir}/{sentinel}"
    return f"Run this shell command exactly as written, using your shell/bash tool:\n{cmd}"


def test_stop_start(t: Test, spec: BackendSpec) -> None:
    """new → wait → restart with new prompt → wait → diff → apply → log → info.

    Uses `yoloai restart --prompt` (= stop + start internally) to verify
    credential re-injection after a container restart. The second prompt
    writes to the work copy so we can verify diff/apply end-to-end.

    This is the matrix's single end-to-end workflow test: the restart leg is
    its unique coverage, and it also folds in the basic new→diff→apply→log→info
    path that the old standalone `full_workflow` test asserted (dropped to halve
    the per-backend boot+inference cost — stop_start was already a superset).

    The second prompt also commits a change and creates an annotated git tag
    inside the work copy, so `diff --log` exercises the tag-read pipeline
    (DF12: ListTagsBeyondBaseline + GetTagMessage). On Tart that pipeline runs
    git *inside the VM* via runtime.GitExecFor; before DF12 it read the empty
    host staging copy and found nothing. The tag creation is chained ahead of
    the completion sentinel, so the existing wait_for_sentinel already gates on
    it — a flaked tag step fails as a sentinel timeout, not a confusing
    tag-assertion miss.
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
    # The prompt writes to the work copy, then commits it and tags the commit so
    # diff/apply and the tag-read pipeline (DF12) can verify. The tag step is
    # chained before the sentinel, so the sentinel only fires once the tag
    # exists. git identity is pre-configured in the work copy (yoloai sets
    # user.name/user.email on the baseline), so the commit needs no -c flags.
    sentinel2 = "done2"
    work2 = (
        "echo restarted > output2.txt"
        " && git add -A && git commit -qm checkpoint"
        " && git tag -a v1 -m smoketag"
    )
    prompt2 = _prompt(exdir, work2, sentinel=sentinel2)
    r = t.run("restart", name, "--prompt", prompt2, timeout=spec.new_timeout())
    t.assert_ok(r, "restart")

    # Restart adds stop+start overhead on top of model inference, so allow extra time.
    t.wait_for_sentinel(name, sentinel=sentinel2, timeout=spec.sentinel_timeout() + 60, stall_grace_secs=spec.sentinel_stall_grace())

    # Verify diff shows the restarted agent's output (now committed, so it shows
    # in the cumulative baseline→worktree diff).
    r = t.run("diff", name)
    t.assert_ok(r, "diff after restart")
    t.assert_in("output2.txt", r.stdout, "diff after restart")

    # DF12: the annotated tag created inside the sandbox must be discoverable
    # from the host. `diff --log --json` lists commits beyond baseline with their
    # tags; the tag's name (v1) and annotated message (smoketag) both come from
    # git run in the sandbox's git context — in the VM on Tart. Asserting the
    # message specifically covers GetTagMessage, the second half of the pipeline.
    r = t.run("diff", name, "--log", "--json")
    t.assert_ok(r, "diff --log --json for tags")
    t.assert_in("v1", r.stdout, "tag name beyond baseline (ListTagsBeyondBaseline)")
    t.assert_in("smoketag", r.stdout, "annotated tag message (GetTagMessage)")

    # Apply and verify the file lands in the project directory. The fixture
    # project isn't a git repo, so apply auto-uses --no-commit (working-tree
    # patch); the committed output2.txt is part of that patch and still lands.
    r = t.run("apply", name, "--yes", "--include-uncommitted")
    t.assert_ok(r, "apply after restart")

    output2 = project / "output2.txt"
    if not output2.exists():
        raise AssertionError("output2.txt not found in project dir after apply")
    if "restarted" not in output2.read_text():
        raise AssertionError(
            f"output2.txt does not contain 'restarted': {output2.read_text()!r}"
        )

    # Folded-in full_workflow coverage: the agent-log and sandbox-info read
    # commands work against a sandbox that has actually run an agent.
    r = t.run("log", name)
    t.assert_ok(r, "log")
    if not r.stdout.strip():
        raise AssertionError("log is empty after agent run")

    r = t.run("sandbox", name, "info")
    t.assert_ok(r, "sandbox info")
    t.assert_in(name, r.stdout, "sandbox info (name)")
    t.assert_in("claude", r.stdout, "sandbox info (agent)")


def _git_out(project: Path, *args: str) -> str:
    """Run git in the host project dir and return stdout (raises on failure)."""
    r = subprocess.run(
        ["git", "-C", str(project), *args],
        capture_output=True, text=True, check=True,
    )
    return r.stdout


def test_tag_transfer(t: Test, spec: BackendSpec) -> None:
    """Apply commits + transfer an annotated tag to a GIT host repo (DF12).

    The complement to stop_start's non-git path: with a git target, `apply`
    replays the agent's commit via format-patch/am and `apply --tags` re-creates
    the agent's annotated tag on the host, mapped to the applied commit. On Tart
    the tag read runs git inside the VM (runtime.GitExecFor); the transfer writes
    to the host repo. Full-tier only — it's a second VM boot per backend, so it's
    a release-gate check rather than a per-PR one.
    """
    project = t.project_git(f"tag-transfer-{spec.label}")
    name = t.sandbox(f"tag-transfer-{spec.label}")
    exdir = spec.exchange_dir(name)
    # Commit a change and annotate it with a tag, all inside the work copy. The
    # tag is chained before the sentinel, so the wait gates on its creation.
    work = (
        "echo feature > feature.txt"
        " && git add -A && git commit -qm 'add feature'"
        " && git tag -a v1 -m smoketag"
    )
    prompt = _prompt(exdir, work)

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

    # One call: replay the commit onto the git host AND transfer the tag, mapped
    # to the just-applied commit via the apply's own sandbox→host SHA map.
    r = t.run("apply", name, "--yes", "--tags")
    t.assert_ok(r, "apply --tags")

    # The committed file landed (format-patch/am replay onto the git host).
    if not (project / "feature.txt").exists():
        raise AssertionError("feature.txt not found in project after apply --tags")

    # The annotated tag was re-created on the host...
    host_tags = _git_out(project, "tag", "-l").split()
    if "v1" not in host_tags:
        raise AssertionError(f"tag v1 not transferred to host repo; tags={host_tags}")
    # ...pointing at the applied commit (subject sanity check).
    subject = _git_out(project, "log", "-1", "--format=%s", "v1").strip()
    if subject != "add feature":
        raise AssertionError(f"tag v1 points at unexpected commit: {subject!r}")
    # ...and it's annotated (carries the message), proving GetTagMessage ran.
    message = _git_out(project, "for-each-ref", "--format=%(contents:subject)", "refs/tags/v1").strip()
    if message != "smoketag":
        raise AssertionError(f"tag v1 message not transferred: {message!r}")


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
    with t.ctx.state_lock:
        t.ctx.sandboxes.append(name_b)
    t.local_sandboxes.append(name_b)

    r = t.run("diff", name_b)
    t.assert_ok(r, "diff on clone")
    t.assert_in("clone-output.txt", r.stdout, "cloned diff output")


def test_isolation_check(t: Test, spec: BackendSpec) -> None:
    """Verify network-isolated sandbox blocks outbound traffic.

    Creates a sandbox with --network-isolated, waits for it to become active,
    then execs curl to an external address (should be blocked) and to localhost
    (should not timeout — proves networking stack is functional, not just broken).

    Scheduled only where iptables-based isolation can be enforced — see
    isolation_check_applies; inapplicable backends are never scheduled.
    """
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

    # Outbound to external address should be blocked by iptables rules.
    #
    # The egress verdict comes from curl's OWN output, not the `yoloai exec`
    # exit code: curl with `-w %{http_code}` prints "000" when it never gets a
    # response (connection blocked/timed out) and a real status ("200", "301",
    # …) when it reaches the host. This is transport-independent — it does not
    # depend on the backend's exec machinery propagating the inner exit code
    # (containerd's InteractiveExec historically discarded it, which silently
    # turned an exit-code probe into a no-op false-pass; see
    # backend-idiosyncrasies.md).
    #
    # "Active" status only means the sentinel is up; on VM backends the guest's
    # iptables/ipset default-deny chain can still be a beat behind installing,
    # so a single egress probe fired the instant we see "active" races the rule
    # setup and reports a false un-blocked result (standalone the same sandbox
    # blocks 5/5). Poll instead: the rules are permanent once installed, so we
    # wait for the first confirmed block. A genuine isolation gap never blocks
    # and trips the deadline below.
    blocked = False
    last_code = ""
    enforce_deadline = time.monotonic() + 30
    while time.monotonic() < enforce_deadline:
        r = t.run(
            "exec", name, "--",
            "curl", "-s", "-o", "/dev/null", "-w", "%{http_code}",
            "--max-time", "5", "http://1.1.1.1",
            timeout=30,
        )
        last_code = r.stdout.strip().splitlines()[-1].strip() if r.stdout.strip() else ""
        if last_code == "000":
            blocked = True
            break
        time.sleep(2)
    if not blocked:
        raise AssertionError(
            f"curl to 1.1.1.1 returned http_code {last_code!r} for 30s but "
            "should be blocked (http_code 000) by network isolation"
        )

    # Localhost should not get exit code 28 (timeout) — proves the networking
    # stack is functional, not just broken
    r = t.run("exec", name, "--", "curl", "-s", "--max-time", "3", "http://127.0.0.1:1", timeout=30)
    if r.returncode == 28:
        raise AssertionError(
            "curl to 127.0.0.1 timed out (exit 28) — networking stack may be broken, not isolated"
        )


def test_dind(t: Test, spec: BackendSpec) -> None:
    """Docker-in-Docker under container-privileged.

    Starts a nested dockerd inside the sandbox and runs `docker run hello-world`,
    exercising the privileged mode's headline use case end-to-end: the
    --privileged caps, the shared mount propagation the entrypoint sets, and the
    real-filesystem /var/lib/docker volume yoloai mounts so the nested daemon
    auto-selects the native overlay driver (works on every provider — see
    docs/contributors/design/research/dind-storage-drivers.md).

    Scheduled only on container-privileged — see dind_applies; the other backends
    lack the caps to start a nested daemon and are never scheduled.
    """
    project = t.project(f"dind-{spec.label}")
    name = t.sandbox(f"dind-{spec.label}")

    # idle agent: a running container to exec into, with no model inference.
    r = t.run(
        "new", name, str(project),
        "--no-start", "--yes",
        "--agent", "idle",
        *spec.new_args(),
        *t.debug_new_flags,
        timeout=60,
    )
    t.assert_ok(r, "new (idle, privileged)")

    r = t.run("start", name, timeout=CMD_TIMEOUT)
    t.assert_ok(r, "start")

    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        status = t._sandbox_status(name)
        if status == "active" or status == "idle":
            break
        time.sleep(1)

    # One exec keeps the backgrounded dockerd alive through the docker run. On
    # failure, dump the daemon log so the autopsy has something to chew on.
    script = (
        "sudo dockerd >/tmp/dockerd.log 2>&1 & "
        "for i in $(seq 1 30); do sudo docker info >/dev/null 2>&1 && break; sleep 1; done; "
        "sudo docker run --rm hello-world "
        "|| { echo '--- dockerd.log ---'; cat /tmp/dockerd.log; exit 1; }"
    )
    r = t.run("exec", name, "--", "bash", "-lc", script, timeout=120)
    t.assert_ok(r, "dind: dockerd + docker run hello-world")
    t.assert_in("Hello from Docker", r.stdout, "dind hello-world output")


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
                "  sudo yoloai system prune --images --yes",
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

    _warm_up_vm_backends(ctx, backends, results)

    return results


def _warm_up_vm_backends(
    ctx: RunContext,
    backends: list[BackendSpec],
    results: dict[str, PrereqResult],
) -> None:
    """Pay each VM backend's cold create cost once, outside the timed matrix.

    Building the image upfront (above) keeps a cold `docker build` out of the
    timed `new`, but a VM backend's *first* create does more cold work than the
    build: kernel/initrd staging, devmapper pool init (vm-enhanced), and the
    page-cache-cold first QEMU boot. On a slightly slow host that lands a real
    `new` near its timeout and trips a spurious flake (see run 20260604-133534).
    A throwaway create+destroy per available VM backend moves that one-time cost
    here, where the budget is build-sized rather than boot-sized, so the first
    timed `new` always runs warm. Failures here only warn: the matrix run remains
    the source of truth, and a genuine breakage will resurface there.
    """
    seen: set[tuple[str, str, Optional[str]]] = set()
    for spec in backends:
        if not spec.is_vm:
            continue
        pr = results.get(spec.label)
        if pr is None or not pr.available:
            continue
        key = (spec.os, spec.isolation, spec.backend)
        if key in seen:
            continue
        seen.add(key)

        name = f"{ctx.run_id}-warmup-{spec.label}"
        print(f"  Warming up {spec.label} (cold create, outside the timed run)...")
        new_cmd = [
            ctx.yoloai_bin, "new", name, str(ctx.fixture_dir),
            "--agent", "idle", "--yes", *spec.new_args(),
        ]
        try:
            r = subprocess.run(new_cmd, capture_output=True, text=True, timeout=BASE_BUILD_TIMEOUT)
        except subprocess.TimeoutExpired:
            print(f"  WARNING: {spec.label} warm-up create timed out; matrix run will retry cold.")
            _destroy_named_sandboxes(ctx, [name])
            continue
        if r.returncode != 0:
            print(f"  WARNING: {spec.label} warm-up create failed (exit {r.returncode}); matrix run will retry cold.")
        _destroy_named_sandboxes(ctx, [name])


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

def print_summary(
    results: list[TestResult],
    uncovered_notes: Optional[list[tuple[str, str]]] = None,
) -> None:
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

    # Backends the complete matrix covers but this host can't run are kept in their
    # own group: unlike the skips above, no install fixes them here — they need a
    # different host. Each carries a reason so no part of the matrix is silently
    # absent (see uncovered_reason).
    if uncovered_notes:
        print("\nNot covered on this host (re-run on the other host to complete the matrix):")
        for label, reason in uncovered_notes:
            print(f"  N/A  {label}: {reason}")


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
            "Examples: --test stop_start --test stop_start/seatbelt"
        ),
    )
    parser.add_argument(
        "--backend",
        action="append",
        help=(
            "Run the matrix tests only for specific backend(s). "
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
    parser.add_argument(
        "--jobs",
        type=int,
        default=0,
        metavar="N",
        help=(
            "Max backends to run concurrently within a matrix phase. "
            "0 (default) = auto (all backends at once). 1 = strictly serial "
            "(reproduces the historical one-backend-at-a-time behavior)."
        ),
    )
    parser.add_argument(
        "--vm-concurrency",
        type=int,
        default=int(os.environ.get("YOLOAI_SMOKE_VM_CONCURRENCY", "2")),
        metavar="N",
        help=(
            "Max VM-backed backends to run concurrently (macOS allows 2 Tart VMs; "
            "concurrent containerd QEMU VMs raise peak disk). Default 2, or "
            "$YOLOAI_SMOKE_VM_CONCURRENCY. Container backends are unaffected."
        ),
    )
    parser.add_argument(
        "--all-docker-providers",
        action="store_true",
        help=(
            "Run the suite against every installed docker provider (macOS: OrbStack "
            "and Docker Desktop), not just the active one. The first (active) provider "
            "runs the full matrix; the rest run only the docker-backed tiers (the only "
            "ones that depend on the docker daemon). Errors out if an installed "
            "provider's daemon isn't running — start it and retry. No app is launched "
            "or stopped. On a single-provider host (e.g. Linux) this is one run."
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


def _docker(*cmd: str, host: str = "") -> subprocess.CompletedProcess[str]:
    """Run a `docker` CLI command, optionally pinned to a specific daemon host."""
    argv = ["docker"]
    if host:
        argv += ["--host", host]
    argv += list(cmd)
    return subprocess.run(argv, capture_output=True, text=True, timeout=30)


def _docker_context_endpoint(name: str) -> str:
    """Resolve a docker context's daemon endpoint, or '' if it can't be read."""
    r = _docker("context", "inspect", name, "--format", "{{.Endpoints.docker.Host}}")
    return r.stdout.strip() if r.returncode == 0 else ""


def _resolve_docker_providers() -> list[tuple[str, str]]:
    """Return [(context-name, endpoint)] for the docker providers to cycle,
    active-first. Empty if docker is unusable."""
    ls = _docker("context", "ls", "--format", "{{.Name}}")
    if ls.returncode != 0:
        return []
    names = [n.strip() for n in ls.stdout.splitlines() if n.strip()]
    active = _docker("context", "show").stdout.strip()
    out: list[tuple[str, str]] = []
    for name in docker_provider_candidates(names, active):
        endpoint = _docker_context_endpoint(name)
        if endpoint:
            out.append((name, endpoint))
    return out


def run_all_providers(args: argparse.Namespace) -> int:
    """Orchestrate one smoke run per installed docker provider (re-exec model).

    Each provider runs as an isolated child process with DOCKER_HOST pinned to it,
    so contexts are never mutated. The active provider runs the full suite; the
    rest run only the docker-backed tiers (the only ones whose daemon differs).
    Errors out if any detected provider's daemon isn't running."""
    providers = _resolve_docker_providers()
    if not providers:
        print("ERROR: no usable docker provider (is docker installed and a context set?)", file=sys.stderr)
        return 1

    unreachable = [(n, ep) for n, ep in providers if _docker("info", host=ep).returncode != 0]
    if unreachable:
        print("ERROR: these docker providers are installed but not running — start them and retry:", file=sys.stderr)
        for n, ep in unreachable:
            print(f"  {n} ({ep})", file=sys.stderr)
        return 1

    print(f"Cycling docker providers: {', '.join(n for n, _ in providers)}")
    base_argv = [a for a in sys.argv[1:] if a != "--all-docker-providers"]
    rollup: list[tuple[str, int]] = []
    for i, (name, endpoint) in enumerate(providers):
        child_argv = list(base_argv)
        scope = "full matrix"
        if i > 0:
            # Only the docker-daemon-dependent tiers differ per provider; podman/
            # seatbelt/tart are daemon-independent and already ran on the first.
            child_argv += ["--backend", "docker", "--backend", "docker-priv"]
            scope = "docker tiers only"
        print(f"\n{'=' * 70}\n=== docker provider: {name} ({endpoint}) — {scope}\n{'=' * 70}")
        env = dict(os.environ)
        env["DOCKER_HOST"] = endpoint
        cp = subprocess.run([sys.executable, __file__, *child_argv], env=env)
        rollup.append((name, cp.returncode))

    print(f"\n{'=' * 70}\nAll-docker-providers rollup:")
    for name, rc in rollup:
        print(f"  {name:<16} {'PASS' if rc == 0 else 'FAIL'}")
    return 0 if all(rc == 0 for _, rc in rollup) else 1


def main() -> int:
    args = parse_args()

    if args.all_docker_providers:
        return run_all_providers(args)

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
        jobs=args.jobs,
        vm_concurrency=args.vm_concurrency,
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
    other_host_label = "macOS" if is_linux else "Linux"
    # When filters are active, use the full matrix so --test / --backend can
    # reach backends (e.g. seatbelt) that live outside the base tier.
    if ctx.full or ctx.test_filter or ctx.backend_filter:
        matrix = FULL_LINUX_BACKENDS if is_linux else FULL_MACOS_BACKENDS
        other_os_matrix = FULL_MACOS_BACKENDS if is_linux else FULL_LINUX_BACKENDS
    else:
        matrix = BASE_LINUX_BACKENDS if is_linux else BASE_MACOS_BACKENDS
        other_os_matrix = BASE_MACOS_BACKENDS if is_linux else BASE_LINUX_BACKENDS

    # Backends in the complete (both-host) tier matrix that this host can't run
    # (situation 2): OS-locked daemons plus host-specific isolation modes the matrix
    # only schedules on the other side. Surfaced as a distinct end-of-run group with
    # a per-backend reason so no part of the matrix is silently absent.
    uncovered_notes = [
        (s.label, uncovered_reason(s, other_host_label))
        for s in uncovered_backends(other_os_matrix, matrix)
    ]

    # Build the list of specs to prereq-check.  When explicit filters narrow
    # the run, restrict prereq checking (and image builds) to only the backends
    # that will actually be exercised.  DEFAULT_BACKEND is always included
    # because it supplies the credentials check.
    matrix_labels = {s.label for s in matrix}
    def _spec_needed(spec: "BackendSpec") -> bool:
        return spec_needed_for_filters(spec.label, ctx.test_filter, ctx.backend_filter)

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
        # Base tier is designed to be runnable with partial backends, so it
        # warns and skips. Full tier is the release gate: an unprovisioned
        # backend that *could* run on this host is an environment defect, so
        # its tests fail loudly (see _run_backend_test). The notice mirrors that.
        if ctx.full:
            print("ERROR: some backends unavailable on this host; full tier will FAIL their tests:")
        else:
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
        return should_run_under_filter(test_name, ctx.test_filter)

    def _run_backend_test(
        test_label: str,
        spec: BackendSpec,
        test_fn: Callable[[Test, BackendSpec], None],
        vm_sema: "threading.Semaphore",
    ) -> None:
        """Run one backend's slot of a matrix test, with prereq/skip + retries.

        Self-contained per backend so it can run on its own thread: it filters
        (should_run/backend_filter), skips unavailable backends, then runs the
        test under a VM-concurrency gate. Each attempt destroys exactly the
        sandboxes its own attempt created (result.sandboxes) — never a slice of
        the shared ctx.sandboxes, which sibling backends mutate concurrently.
        Only the terminal result is recorded, via _record_result.
        """
        test_name = f"{test_label}/{spec.label}"
        if not should_run_test(test_name):
            return
        if ctx.backend_filter and spec.label not in ctx.backend_filter:
            return

        pr = preq.get(spec.label)
        if pr is None or not pr.available:
            reason = pr.note if pr else "not in prereq results"
            # This backend runs on this host but its prereqs aren't satisfied
            # (daemon down, missing perms, etc.) — fixable here. Base tier tolerates
            # a partial run and skips; full tier is the release gate, so an
            # unprovisioned backend is an environment defect that must fail loudly.
            if ctx.full:
                _record_result(ctx, TestResult(
                    name=test_name, passed=False,
                    reason=f"prerequisite unavailable (full tier requires it): {reason}",
                ))
            else:
                skip_test(ctx, test_name, reason)
            return

        # VM backends share a host-wide concurrency cap (macOS allows 2 Tart VMs;
        # concurrent containerd QEMU VMs raise peak disk). Container backends run
        # unthrottled. Hold the slot for the test's full lifetime incl. retries.
        gate = vm_sema if spec.is_vm else _NULL_GATE
        with gate:
            result = run_test(ctx, test_name, lambda t: test_fn(t, spec), attempt=1)
            if not result.passed and not result.skipped and spec.retries > 0:
                for retry_idx in range(spec.retries):
                    attempt = retry_idx + 2  # attempt 1 was the initial run
                    _emit(ctx, f"      Retrying {test_name} (attempt {retry_idx + 1}/{spec.retries})...")
                    # Destroy the failed attempt's sandboxes so they don't leak
                    # or hold a VM slot; the retry allocates fresh names (each
                    # Test.sandbox() call draws a new sequence number). run_test
                    # already preserved their state under
                    # <log_dir>/sandboxes/<test>/attempt<N>/.
                    _destroy_named_sandboxes(ctx, result.sandboxes)
                    result = run_test(ctx, test_name, lambda t: test_fn(t, spec), attempt=attempt)
                    if result.passed:
                        break
            # On VM backends, a failed test can leave a running VM that consumes a
            # host VM slot and blocks subsequent VM tests. Destroy immediately
            # after retries are exhausted (state already preserved at this point).
            if not result.passed and spec.is_vm:
                _destroy_named_sandboxes(ctx, result.sandboxes)
        _record_result(ctx, result)

    def run_matrix_test(
        test_label: str,
        test_fn: Callable[[Test, BackendSpec], None],
        applies_to: Optional[Callable[[BackendSpec], bool]] = None,
        vm_serial: bool = False,
    ) -> None:
        """Run a test across the backend matrix, one slot per backend.

        When applies_to is given, structurally inapplicable specs (the test could
        never run there regardless of host — e.g. dind without privileged caps) are
        excluded from scheduling and reported once, rather than each emitting a
        misleading per-backend SKIP. Conditional gating (host prereqs, --full) is
        handled downstream as a genuine runtime skip.

        Backends fan out concurrently (bounded by --jobs and, for VM backends, by
        --vm-concurrency); the phase joins before the caller moves on. --jobs 1
        reproduces the historical strictly-serial behavior exactly. vm_serial pins
        the VM cap to 1 for this phase regardless of --vm-concurrency: used by
        isolation_check, where concurrent guest-rule setup under VM load was racing
        the egress probe (the isolation itself is sound — see test_isolation_check).
        """
        specs = matrix if applies_to is None else [s for s in matrix if applies_to(s)]
        if applies_to is not None:
            excluded = [s for s in matrix if not applies_to(s)]
            if excluded:
                labels = ", ".join(s.label for s in excluded)
                print(f"  ({len(excluded)} not applicable, not scheduled: {labels})")
        vm_cap = 1 if vm_serial else max(1, ctx.vm_concurrency)
        vm_sema = threading.Semaphore(vm_cap)
        workers = ctx.jobs if ctx.jobs > 0 else max(1, len(specs))
        if workers <= 1:
            for spec in specs:
                _run_backend_test(test_label, spec, test_fn, vm_sema)
            return
        with ThreadPoolExecutor(max_workers=workers) as pool:
            futures = [
                pool.submit(_run_backend_test, test_label, spec, test_fn, vm_sema)
                for spec in specs
            ]
            for f in futures:
                f.result()  # surface worker exceptions to the main thread

    # -------------------------------------------------------------------------
    # Non-matrix tests (full tier only)
    # -------------------------------------------------------------------------

    if should_run_test("clone"):
        if ctx.full:
            _record_result(ctx, run_test(ctx, "clone", lambda t: test_clone(t, DEFAULT_BACKEND)))
        else:
            skip_test(ctx, "clone", "full tier only (use --full)")

    # -------------------------------------------------------------------------
    # Matrix tests: stop_start (end-to-end workflow + restart), isolation_check
    # -------------------------------------------------------------------------

    print("\nBackend matrix (stop_start):")
    run_matrix_test("stop_start", test_stop_start)

    # tag_transfer needs a second VM boot per backend (its own git-host sandbox),
    # so it's a full-tier release-gate check rather than a per-PR base test.
    print("\nBackend matrix (tag_transfer):")
    if ctx.full:
        run_matrix_test("tag_transfer", test_tag_transfer)
    else:
        print("  full tier only (use --full)")

    print("\nBackend matrix (isolation_check):")
    run_matrix_test(
        "isolation_check", test_isolation_check,
        applies_to=isolation_check_applies, vm_serial=True,
    )

    print("\nBackend matrix (dind):")
    run_matrix_test("dind", test_dind, applies_to=dind_applies)

    # Parallel execution records results in completion order; sort by test name
    # so the summary and manifest are deterministic regardless of --jobs.
    ctx.results.sort(key=lambda r: r.name)

    print_summary(ctx.results, uncovered_notes)

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
