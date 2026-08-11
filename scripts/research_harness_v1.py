#!/usr/bin/env python3
# ABOUTME: Version 1 of the hardware-research harness library. Makes a run's
# ABOUTME: invariants structural: you record measurements and declare expectations,
# ABOUTME: and the report is rendered from that data rather than written by hand.

"""A harness that will not let you write a verdict your data does not support.

**This file is frozen.** Research written against it must keep working and keep
reproducing, so v1's contract never changes. A new mandatory invariant -- which is
what every newly-discovered failure mode becomes -- goes in `research_harness_v2.py`
as a new file, and only new research adopts it. See D134.

Why this exists, concretely. The 2026-08 host-enforcement research produced roughly
a dozen invalid runs across two platforms, and they fell into two mechanical shapes:

  * A control that passed for free -- satisfied by a still-loaded policy, by missing
    tooling, by comparing two empty strings, or by travelling a different protocol
    than the test it was controlling for.
  * A verdict written in advance -- an `echo` of a sentence that contradicted the
    number printed one line above it. Five files across the two research directories
    contain exactly that.

Neither is a lapse of care; every one of those harnesses ran cleanly and exited 0.
So this module removes the option rather than restating the rule:

  * You never write a verdict. You record measurements and declare expectations over
    them (`expect`), and `report()` renders PASS/FAIL from the recorded value. A
    verdict cannot disagree with its data because it *is* its data.
  * `report()` refuses to render at all unless at least one control was declared,
    every declared control held, and the run stated what it did not try.
  * `run()` raises on non-zero exit by default. Opting out is explicit and recorded.

What it deliberately does NOT do: judge whether a conclusion is wider than the run
that produced it. That was the most expensive failure class in the same corpus and no
library can see it. `not_tried()` is the partial answer -- enumerate the untested, so
the gap is visible on the page.

See `docs/contributors/principles/testing-principles.md` §11.
"""

from __future__ import annotations

import shlex
import subprocess
import sys
from dataclasses import dataclass, field
from typing import Callable, Sequence, TextIO


HARNESS_VERSION = 1
"""The contract version. A breaking change is a new file, never an edit to this one."""


class HarnessError(RuntimeError):
    """Raised when a run violates an invariant that makes its output meaningless."""


@dataclass(frozen=True)
class Measurement:
    """One recorded observation. `value` is whatever the probe returned."""

    label: str
    value: object
    detail: str = ""
    is_control: bool = False

    def __str__(self) -> str:
        return f"{self.label}={self.value!r}"


@dataclass(frozen=True)
class Expectation:
    """A claim about a recorded measurement, evaluated at record time."""

    claim: str
    measurement: Measurement
    holds: bool


@dataclass
class Harness:
    """Records a run and renders its report.

    Typical shape::

        h = Harness("L1", "can policy key on the cgroup instead of the address?")
        h.require("nft present", shutil.which("nft") is not None)
        base = h.control("denied host reachable with no policy", probe_denied)
        ...
        h.expect("the denied host is blocked once policy loads", after, want=False)
        h.not_tried("IPv6", "concurrent churn")
        h.report()
    """

    name: str
    description: str = ""
    measurements: list[Measurement] = field(default_factory=list)
    expectations: list[Expectation] = field(default_factory=list)
    _not_tried: list[str] = field(default_factory=list)
    _commands: list[str] = field(default_factory=list)
    _stated_not_tried: bool = False

    # -- preconditions ----------------------------------------------------

    def require(self, label: str, ok: bool, detail: str = "") -> None:
        """Abort the run unless a precondition holds.

        Use for anything whose absence would make a later negative free: a missing
        tool, an unstarted service, an address that was never assigned.
        """
        if not ok:
            suffix = f" ({detail})" if detail else ""
            raise HarnessError(
                f"[{self.name}] precondition failed: {label}{suffix}. "
                "Every result below would be free, so the run is void."
            )

    # -- running things ---------------------------------------------------

    def run(
        self,
        argv: Sequence[str],
        *,
        stdin: str | None = None,
        check: bool = True,
        cwd: str | None = None,
    ) -> subprocess.CompletedProcess[str]:
        """Run a command, raising on failure unless `check=False` is explicit.

        `stdin` is passed as text rather than piped through a shell heredoc, which
        is the failure that silently fed an empty ruleset to `nft -f` and still
        exited 0.
        """
        self._commands.append(shlex.join(argv))
        proc = subprocess.run(  # noqa: S603 - argv is caller-supplied by design
            list(argv),
            input=stdin,
            capture_output=True,
            text=True,
            cwd=cwd,
            check=False,
        )
        if check and proc.returncode != 0:
            raise HarnessError(
                f"[{self.name}] command failed ({proc.returncode}): "
                f"{shlex.join(argv)}\nstderr: {proc.stderr.strip()}"
            )
        return proc

    # -- recording --------------------------------------------------------

    def measure(self, label: str, value: object, detail: str = "") -> Measurement:
        """Record an observation and return it."""
        m = Measurement(label=label, value=value, detail=detail)
        self.measurements.append(m)
        return m

    def control(self, label: str, value: object, detail: str = "") -> Measurement:
        """Record an observation that must hold for the run to mean anything.

        A control is checked at `report()` time: it must be truthy. A run with no
        controls will not render.
        """
        m = Measurement(label=label, value=value, detail=detail, is_control=True)
        self.measurements.append(m)
        return m

    def expect(self, claim: str, measurement: Measurement, *, want: bool = True) -> Expectation:
        """Declare a claim about an already-recorded measurement.

        There is deliberately no way to state a claim about a value that was not
        recorded -- that is what made a written-in-advance verdict possible.
        """
        if measurement not in self.measurements:
            raise HarnessError(
                f"[{self.name}] expectation {claim!r} references a measurement this "
                "harness never recorded."
            )
        holds = bool(measurement.value) is want
        e = Expectation(claim=claim, measurement=measurement, holds=holds)
        self.expectations.append(e)
        return e

    def not_tried(self, *items: str) -> None:
        """State what this run did not cover. Required before `report()`."""
        self._not_tried.extend(items)
        self._stated_not_tried = True

    # -- rendering --------------------------------------------------------

    def failures(self) -> list[Expectation]:
        return [e for e in self.expectations if not e.holds]

    def report(self, out: TextIO | None = None) -> bool:
        """Render the run. Returns True when every expectation held.

        Refuses to render, rather than rendering something misleading, when the run
        cannot support a conclusion.
        """
        stream = out if out is not None else sys.stdout
        controls = [m for m in self.measurements if m.is_control]
        if not controls:
            raise HarnessError(
                f"[{self.name}] no control was declared. A negative result with no "
                "control is indistinguishable from a broken rig."
            )
        broken = [c for c in controls if not c.value]
        if broken:
            names = ", ".join(c.label for c in broken)
            raise HarnessError(
                f"[{self.name}] control(s) did not hold: {names}. The run is void; "
                "its results would be free."
            )
        if not self._stated_not_tried:
            raise HarnessError(
                f"[{self.name}] not_tried() was never called. State the bounds of the "
                "run, or its conclusion will be read wider than its evidence."
            )

        print(f"=== {self.name} ===", file=stream)
        if self.description:
            print(self.description, file=stream)

        print("\n-- controls (all held, or this would not render) --", file=stream)
        for c in controls:
            print(f"  {c.label}: {c.value!r}{_suffix(c.detail)}", file=stream)

        others = [m for m in self.measurements if not m.is_control]
        if others:
            print("\n-- measurements --", file=stream)
            for m in others:
                print(f"  {m.label}: {m.value!r}{_suffix(m.detail)}", file=stream)

        print("\n-- expectations, rendered from the values above --", file=stream)
        for e in self.expectations:
            status = "PASS" if e.holds else "FAIL"
            print(f"  [{status}] {e.claim}  <- {e.measurement}", file=stream)

        print("\n-- WHAT WAS NOT TRIED --", file=stream)
        for item in self._not_tried:
            print(f"  - {item}", file=stream)

        ok = not self.failures()
        print(f"\n-- {'all expectations held' if ok else 'SOME EXPECTATIONS FAILED'} --", file=stream)
        return ok


def _suffix(detail: str) -> str:
    return f"   ({detail})" if detail else ""


def counter_moved(before: int, after: int) -> bool:
    """True when a rule counter advanced.

    A verdict rendered while every counter reads zero means no packet reached the
    rule at all -- the shape that made one whole run's results free.
    """
    return after > before
