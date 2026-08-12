#!/usr/bin/env python3
# ABOUTME: Version 2 of the hardware-research harness library. Adds the invariant
# ABOUTME: v1 could not express: a probe must demonstrate it can report failure
# ABOUTME: before any expectation is allowed to rest on it.

"""A harness whose probes must exhibit the negative before you trust the positive.

**This file is frozen**, on the same terms as v1: research written against it must
keep reproducing, so v2's contract never changes. The next mandatory invariant goes
in `research_harness_v3.py`. See D134 for why versioning works this way and D136 for
what v2 adds.

Why v2 exists, and it is one class rather than an accumulation of polish. v1 removed
two shapes: a control that passed for free, and a verdict written in advance. The
2026-08 enforcement corpus was then re-counted (D136) and **28 of ~29 invalidated
runs were harness defects rather than the world differing from the model** -- but the
survivors of v1's rules failed a way v1 cannot see:

    The probe never demonstrated that it could produce the other answer.

`pf-spoof` run 2 is the specimen. It recorded 7 PASS / 0 FAIL concluding a guest
cannot modify its own interfaces -- the exact inverse of the truth. Every command it
ran was real, every output it recorded was genuine, and its controls held. The
sandbox it measured simply lacked the capability under test, so every "Operation not
permitted" was free. It satisfies `testing-principles.md` §11 cleanly, because §11
asks whether the *mechanism* was present and this is a question about whether the
*instrument* discriminates.

**And a bad run that agrees with you is the one that survives.** In the same count,
invalidated runs split 29 confirming the hypothesis to 8 contradicting it. The
contradicting ones were all chased inside their round, because a contradicting result
makes you look. So the rule cannot be "be suspicious of results you like"; it has to
be structural.

So in v2 an expectation may only rest on a `probe()` -- a named callable, sampled
more than once. Before the mechanism is installed you take a `baseline()`, and the
baseline must come out **opposite** to what you will later claim. That is the same
callable, in the same run, on the same host, shown answering both ways. A probe that
cannot produce the opposite answer has not been shown to be a probe.

The technique is not new here; it is two existing runs generalised.
`pf-grant-matrix.txt` removes its own blanket sudo grant and refuses to report until
`sudo -n /usr/bin/true` is *shown* to fail. `r15_unelevated_install.py` controls on
"plain nft is refused to this user, so the helper is doing real work."

Some measurements have no mechanism-absent state -- a capability probe, a census, a
cost. Those pass `unbaselined="<reason>"` to `expect()`, and the reason is rendered
in the report under its own heading. The waiver is the escape hatch; silence is not.

The three defects v1 shipped with (D134 recorded them as v2's specification) are
fixed here:

  * `expect()` rejects a non-`bool` value instead of judging truthiness. In v1 a
    measurement reading `"blocked"` FAILED the claim "the host is blocked" and a
    measurement of `""` -- a probe whose command produced no output at all -- PASSED
    it. A probe that never ran certified containment.
  * A failed control renders the measurements collected so far and *then* refuses to
    render a verdict. v1 raised before printing, so the evidence explaining the
    failure died with it.
  * An arm can be voided without voiding the run (`void_arm`). v1's `require()` only
    did the whole run, so a two-arm experiment discarded a valid arm when the other
    failed to start.

What v2 still cannot do, stated so it is not oversold: it cannot tell that a
conclusion is wider than the run supporting it. That was the most expensive class in
the same corpus and no library can see it. `not_tried()` remains the partial answer,
and remains mandatory.

See `docs/contributors/procedures/verification-rounds.md` for the surrounding
process, and `testing-principles.md` §11 for the class v1 covers.
"""

from __future__ import annotations

import shlex
import subprocess
import sys
from dataclasses import dataclass, field
from typing import Callable, Sequence, TextIO


HARNESS_VERSION = 2
"""The contract version. A breaking change is a new file, never an edit to this one."""


class HarnessError(RuntimeError):
    """Raised when a run violates an invariant that makes its output meaningless."""


@dataclass(frozen=True)
class Measurement:
    """One recorded observation.

    `probe` names the probe it was sampled from, or is empty for a standalone
    measurement. `arm` attributes it to an arm that can be voided on its own.
    """

    label: str
    value: object
    detail: str = ""
    is_control: bool = False
    is_baseline: bool = False
    probe: str = ""
    arm: str = ""

    def __str__(self) -> str:
        return f"{self.label}={self.value!r}"


@dataclass(frozen=True)
class Expectation:
    """A claim about a recorded measurement, evaluated at record time."""

    claim: str
    measurement: Measurement
    want: bool
    holds: bool
    arm: str = ""
    waiver: str = ""


@dataclass
class Probe:
    """A named callable that can be sampled repeatedly.

    The point of naming it is that the baseline and the later sample provably come
    from the *same* instrument. Two hand-written checks that look equivalent are the
    shape that produced `pf-no-state` run 5, where the control was built from one
    census cell and the verdict drawn from another.
    """

    name: str
    fn: Callable[[], bool]
    harness: Harness

    def baseline(self, *, want: bool, detail: str = "") -> Measurement:
        """Sample with the mechanism absent, requiring the stated answer.

        This is the negative self-test. `want` is what the probe must report *now*,
        before anything is installed -- and any expectation resting on this probe
        must later claim the opposite. A baseline that disagrees voids the run,
        because a probe answering the wrong way with nothing installed is measuring
        something other than what its author believes.
        """
        value = self.fn()
        if not isinstance(value, bool):
            raise HarnessError(
                f"[{self.harness.name}] probe {self.name!r} returned "
                f"{value!r} ({type(value).__name__}), not a bool. Record "
                "`proc.returncode == 0`, never `proc.stdout`."
            )
        m = Measurement(
            label=f"baseline: {self.name}",
            value=value,
            detail=detail,
            is_control=True,
            is_baseline=True,
            probe=self.name,
        )
        self.harness.measurements.append(m)
        if value is not want:
            self.harness._void(
                f"baseline for probe {self.name!r} read {value!r}, wanted {want!r}. "
                "With the mechanism absent this probe must report the opposite of "
                "what the run will claim; it does not, so it is not measuring what "
                "it is believed to measure."
            )
        self.harness._baselines[self.name] = value
        return m

    def sample(self, label: str = "", detail: str = "") -> Measurement:
        """Sample the probe and record the result."""
        value = self.fn()
        if not isinstance(value, bool):
            raise HarnessError(
                f"[{self.harness.name}] probe {self.name!r} returned "
                f"{value!r} ({type(value).__name__}), not a bool."
            )
        m = Measurement(
            label=label or self.name,
            value=value,
            detail=detail,
            probe=self.name,
        )
        self.harness.measurements.append(m)
        return m


@dataclass
class Harness:
    """Records a run and renders its report.

    Typical shape::

        h = Harness("R16", "does the netdev chain survive a daemon restart?")
        h.require("nft present", shutil.which("nft") is not None)

        reach = h.probe("guest reaches the denied host", lambda: curl_ok(DENIED))
        reach.baseline(want=True)          # nothing installed yet -- must reach
        install_policy()
        blocked = reach.sample("after policy loads")

        h.control("the drop counter fired", counter() > 0)
        h.expect("policy blocks the denied host", blocked, want=False)
        h.not_tried("IPv6", "concurrent churn")
        h.report()
    """

    name: str
    description: str = ""
    measurements: list[Measurement] = field(default_factory=list)
    expectations: list[Expectation] = field(default_factory=list)
    _not_tried: list[str] = field(default_factory=list)
    _commands: list[str] = field(default_factory=list)
    _baselines: dict[str, bool] = field(default_factory=dict)
    _void_arms: dict[str, str] = field(default_factory=dict)
    _stated_not_tried: bool = False
    _stream: TextIO | None = None

    # -- preconditions ----------------------------------------------------

    def require(self, label: str, ok: bool, detail: str = "") -> None:
        """Abort the run unless a precondition holds.

        Use for anything whose absence would make a later negative free: a missing
        tool, an unstarted service, an address that was never assigned. To void one
        arm of several rather than the whole run, use `void_arm`.
        """
        if not ok:
            suffix = f" ({detail})" if detail else ""
            self._void(
                f"precondition failed: {label}{suffix}. Every result below would "
                "be free, so the run is void."
            )

    def void_arm(self, arm: str, reason: str) -> None:
        """Void one arm without voiding the run.

        A two-arm experiment where one arm cannot start should still report the
        other. v1 had no way to say this, so P1b made a per-arm precondition fatal
        -- safe, but it discarded a valid arm.
        """
        self._void_arms[arm] = reason

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

    def probe(self, name: str, fn: Callable[[], bool]) -> Probe:
        """Declare a named, repeatable measurement.

        An expectation may only rest on a probe, so this is the entry point for
        anything the run intends to conclude from.
        """
        if name in self._baselines:
            raise HarnessError(
                f"[{self.name}] probe {name!r} is already declared. Two probes "
                "sharing a name defeats the point of naming them."
            )
        return Probe(name=name, fn=fn, harness=self)

    def measure(self, label: str, value: object, detail: str = "", arm: str = "") -> Measurement:
        """Record a standalone observation.

        Fine for anything the run reports but does not conclude from. To make a
        claim about it, `expect()` needs either a probe or an explicit waiver.
        """
        m = Measurement(label=label, value=value, detail=detail, arm=arm)
        self.measurements.append(m)
        return m

    def control(self, label: str, value: object, detail: str = "", arm: str = "") -> Measurement:
        """Record an observation that must hold for the run to mean anything."""
        m = Measurement(label=label, value=value, detail=detail, is_control=True, arm=arm)
        self.measurements.append(m)
        return m

    def expect(
        self,
        claim: str,
        measurement: Measurement,
        *,
        want: bool = True,
        unbaselined: str = "",
        arm: str = "",
    ) -> Expectation:
        """Declare a claim about an already-recorded measurement.

        The measurement must have been sampled from a probe whose baseline came out
        **opposite** to `want` -- the probe shown answering both ways in this run.
        A measurement with no such baseline needs `unbaselined="<reason>"`, which is
        rendered in the report rather than being silently accepted.
        """
        if measurement not in self.measurements:
            raise HarnessError(
                f"[{self.name}] expectation {claim!r} references a measurement this "
                "harness never recorded."
            )
        if not isinstance(measurement.value, bool):
            raise HarnessError(
                f"[{self.name}] expectation {claim!r} rests on a non-bool value "
                f"{measurement.value!r} ({type(measurement.value).__name__}). v1 "
                "judged truthiness here, so `''` -- a probe that produced no output "
                "at all -- passed a containment claim. Record a comparison, not a "
                "string."
            )
        if not unbaselined:
            if not measurement.probe:
                raise HarnessError(
                    f"[{self.name}] expectation {claim!r} rests on a standalone "
                    "measurement. Sample it from a probe() with a baseline, or pass "
                    "unbaselined='<why this has no mechanism-absent state>'."
                )
            if measurement.probe not in self._baselines:
                raise HarnessError(
                    f"[{self.name}] probe {measurement.probe!r} was never "
                    "baselined. Sample it with the mechanism absent first, so the "
                    "run shows it can report the other answer."
                )
            base = self._baselines[measurement.probe]
            if base is want:
                raise HarnessError(
                    f"[{self.name}] probe {measurement.probe!r} baselined at "
                    f"{base!r} and this claim wants {want!r}. The probe was never "
                    "shown producing the opposite answer, so a result matching the "
                    "baseline is indistinguishable from an instrument that only "
                    "ever says one thing. If this is a discrimination check rather "
                    "than a claim, record it with control()."
                )
        holds = measurement.value is want
        e = Expectation(
            claim=claim,
            measurement=measurement,
            want=want,
            holds=holds,
            arm=arm or measurement.arm,
            waiver=unbaselined,
        )
        self.expectations.append(e)
        return e

    def not_tried(self, *items: str) -> None:
        """State what this run did not cover. Required before `report()`."""
        self._not_tried.extend(items)
        self._stated_not_tried = True

    # -- rendering --------------------------------------------------------

    def live(self) -> list[Expectation]:
        """Expectations whose arm was not voided."""
        return [e for e in self.expectations if e.arm not in self._void_arms]

    def failures(self) -> list[Expectation]:
        return [e for e in self.live() if not e.holds]

    def _void(self, reason: str) -> None:
        """Render the evidence, then refuse to render a verdict.

        v1 raised before printing anything, so the measurements that would explain
        a failed control were lost with it. Void the conclusion, not the evidence.
        """
        stream = self._stream if self._stream is not None else sys.stdout
        print(f"=== {self.name} — VOID ===", file=stream)
        print(f"{reason}\n", file=stream)
        self._render_measurements(stream)
        print("\n-- no verdict is rendered from a void run --", file=stream)
        raise HarnessError(f"[{self.name}] {reason}")

    def _render_measurements(self, stream: TextIO) -> None:
        if not self.measurements:
            print("-- no measurements were recorded before the run went void --", file=stream)
            return
        controls = [m for m in self.measurements if m.is_control]
        if controls:
            print("-- controls and baselines --", file=stream)
            for c in controls:
                print(f"  {c.label}: {c.value!r}{_suffix(c.detail)}", file=stream)
        others = [m for m in self.measurements if not m.is_control]
        if others:
            print("\n-- measurements --", file=stream)
            for m in others:
                arm = f" [arm: {m.arm}]" if m.arm else ""
                print(f"  {m.label}{arm}: {m.value!r}{_suffix(m.detail)}", file=stream)

    def report(self, out: TextIO | None = None) -> bool:
        """Render the run. Returns True when every live expectation held.

        Refuses to render, rather than rendering something misleading, when the run
        cannot support a conclusion.
        """
        stream = out if out is not None else sys.stdout
        self._stream = stream
        controls = [m for m in self.measurements if m.is_control]
        if not controls:
            self._void(
                "no control or baseline was declared. A negative result with no "
                "control is indistinguishable from a broken rig."
            )
        # Baselines are excluded: they are validated against `want` at record time, and a
        # baseline whose correct value is False -- "nothing has happened yet, so silence is
        # the floor" -- would otherwise void every run that took one. That defect made half
        # this API unusable until V1 hit it.
        broken = [
            c for c in controls
            if not c.value and not c.is_baseline and c.arm not in self._void_arms
        ]
        if broken:
            names = ", ".join(c.label for c in broken)
            self._void(f"control(s) did not hold: {names}. Their results would be free.")
        if not self._stated_not_tried:
            self._void(
                "not_tried() was never called. State the bounds of the run, or its "
                "conclusion will be read wider than its evidence."
            )

        print(f"=== {self.name} ===", file=stream)
        if self.description:
            print(self.description, file=stream)

        print("\n-- controls and baselines (all held, or this would not render) --", file=stream)
        for c in controls:
            print(f"  {c.label}: {c.value!r}{_suffix(c.detail)}", file=stream)

        others = [m for m in self.measurements if not m.is_control]
        if others:
            print("\n-- measurements --", file=stream)
            for m in others:
                arm = f" [arm: {m.arm}]" if m.arm else ""
                print(f"  {m.label}{arm}: {m.value!r}{_suffix(m.detail)}", file=stream)

        if self._void_arms:
            print("\n-- VOIDED ARMS, whose expectations decide nothing --", file=stream)
            for arm, reason in self._void_arms.items():
                print(f"  {arm}: {reason}", file=stream)

        print("\n-- expectations, rendered from the values above --", file=stream)
        for e in self.expectations:
            if e.arm in self._void_arms:
                status = "VOID"
            else:
                status = "PASS" if e.holds else "FAIL"
            print(f"  [{status}] {e.claim}  <- {e.measurement}", file=stream)

        waived = [e for e in self.expectations if e.waiver]
        if waived:
            print(
                "\n-- UNBASELINED CLAIMS, resting on a probe never shown to report "
                "failure --",
                file=stream,
            )
            for e in waived:
                print(f"  {e.claim}: {e.waiver}", file=stream)

        print("\n-- WHAT WAS NOT TRIED --", file=stream)
        for item in self._not_tried:
            print(f"  - {item}", file=stream)

        ok = not self.failures()
        print(
            f"\n-- {'all live expectations held' if ok else 'SOME EXPECTATIONS FAILED'} --",
            file=stream,
        )
        return ok


def _suffix(detail: str) -> str:
    return f"   ({detail})" if detail else ""


def counter_moved(before: int, after: int) -> bool:
    """True when a rule counter advanced.

    A verdict rendered while every counter reads zero means no packet reached the
    rule at all -- the shape that made one whole run's results free.
    """
    return after > before
