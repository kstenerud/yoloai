#!/usr/bin/env python3
# ABOUTME: W6b — the bounding set is a property of a process TREE, and `container exec`
# ABOUTME: is parented by the daemon rather than by the entrypoint. Does the backend's own
# ABOUTME: exec path hand the capability back?

"""W6b: added mid-round from W6's result, and it decides whether W6 is implementable.

W6 measured that a root agent cannot remove its own allowlist once `CAP_NET_ADMIN` is out
of the bounding set. That result is about **descendants of the process that dropped it**.
`container exec` is not one: the daemon spawns it, from the container's *configured*
capability set, so if that set still carries `NET_ADMIN` the drop is invisible to
everything reached that way.

Why it matters is not the obvious reason. A hostile agent inside the guest cannot invoke
`container exec` — that is a host-side command — so this is **not an escape** for the
threat model this workstream targets. What it decides is narrower and more practical:

- **yoloAI's own `exec` would carry the capability.** A user shelling into a sandbox to
  debug it would hold the power to remove that sandbox's enforcement without knowing it,
  and so would anything else the host runs in there.
- **The canary runs this way.** `pf-liveness-detect.txt`'s adopted probe reaches the guest
  through `container exec`. [DF192](../../findings-unresolved.md) already records that the
  guest is in that detector's trust path; if exec is also a capability-restoring path,
  that is a second reason on the same mechanism.
- **And it bounds where the drop must happen.** If exec restores it, then dropping it in
  `entrypoint.py` protects the agent and nothing else, and a design that wants the
  property everywhere has to ask the backend for a narrower configured set —
  `--cap-drop NET_ADMIN` after install, which is not expressible because install needs it.

**The probe is the same shape as W6's and behavioural for the same reason**: whether a
destination the allowlist denied becomes reachable again, not whether `iptables` exited
non-zero.

Instrument boundary: nothing is timed; no host privilege is used.

Run it as: `python3 w6b_exec_vs_bounding_set.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

IMAGE = "yoloai-base"
BOX = "yb-w6b"
ALLOWED = "1.1.1.1"
DENIED = "1.0.0.1"

MODE = {"drop": False}
LAST: dict[str, str] = {}


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["container", "stop", BOX])
    _quiet(["container", "rm", BOX])


def boot_script(drop: bool) -> str:
    """Install the allowlist, optionally drop the capability, then stay alive.

    The `sleep` has to be INSIDE the capsh subtree in the dropped arm, because that is what
    makes the container's long-lived process a descendant of the drop — which is the state
    a real sandbox would be in while its agent runs.
    """
    hold = "sleep 3600"
    launch = f"capsh --drop=cap_net_admin -- -c '{hold}'" if drop else hold
    return f"""
set -u
iptables -F OUTPUT
iptables -A OUTPUT -o lo -j ACCEPT
iptables -A OUTPUT -d {ALLOWED} -j ACCEPT
iptables -P OUTPUT DROP
{launch}
"""


def start_box() -> None:
    cleanup()
    _quiet(
        [
            "container", "run", "-d", "--name", BOX,
            "--entrypoint", "/bin/sh",
            "--cap-add", "NET_ADMIN",
            IMAGE, "-c", boot_script(MODE["drop"]),
        ]
    )
    # The container is useless until the rules are in. Poll for the state the run needs
    # rather than sleeping a guessed interval.
    for _ in range(60):
        out = _quiet(["container", "exec", BOX, "sh", "-c",
                      "iptables -S OUTPUT 2>/dev/null | tr '\\n' ' '"]).stdout
        if "-P OUTPUT DROP" in out:
            return
        time.sleep(1)
    raise HarnessError(f"{BOX} never reached the enforcing state; nothing below would mean anything")


def exec_defeated_the_allowlist() -> bool:
    """THE probe. From a `container exec` process, remove the allowlist and reach for a packet."""
    script = (
        "iptables -F OUTPUT >/dev/null 2>&1; "
        "iptables -P OUTPUT ACCEPT >/dev/null 2>&1; "
        f"curl -s -o /dev/null -m 5 -w '%{{http_code}}' http://{DENIED}/ 2>/dev/null; echo"
    )
    out = _quiet(["container", "exec", BOX, "sh", "-c", script]).stdout.strip()
    LAST["denied_after"] = out
    return out not in ("", "000")


def exec_capbnd() -> str:
    return _quiet(
        ["container", "exec", BOX, "sh", "-c",
         "grep CapBnd /proc/self/status | tr -s ' \\t' ' ' | cut -d' ' -f2"]
    ).stdout.strip()


def holder_capbnd() -> str:
    """The bounding set of the container's long-lived process — the one that DID drop."""
    return _quiet(
        ["container", "exec", BOX, "sh", "-c",
         "pid=$(pgrep -x sleep | head -1); "
         "grep CapBnd /proc/$pid/status | tr -s ' \\t' ' ' | cut -d' ' -f2"]
    ).stdout.strip()


def probe_from_exec(addr: str) -> str:
    return _quiet(
        ["container", "exec", BOX, "sh", "-c",
         f"curl -s -o /dev/null -m 5 -w '%{{http_code}}' http://{addr}/ 2>/dev/null; echo"]
    ).stdout.strip()


def main() -> int:
    h = Harness(
        "W6b",
        "Does `container exec` restore CAP_NET_ADMIN that the entrypoint dropped?",
    )
    try:
        h.require(
            "the apple container daemon is reachable",
            _quiet(["container", "ls"]).returncode == 0,
        )

        probe = h.probe("a `container exec` process removed the allowlist and reached a "
                        "denied host", exec_defeated_the_allowlist)

        # -- BASELINE: no drop anywhere. exec obviously can; without this the refusal
        # below would be indistinguishable from an exec path that never works.
        MODE["drop"] = False
        start_box()
        h.control(
            "the allowlist enforced before exec touched it (undropped arm)",
            probe_from_exec(DENIED) == "000" and probe_from_exec(ALLOWED) != "000",
        )
        probe.baseline(want=True, detail="nothing dropped — exec holds the capability")

        # -- SAMPLE: the entrypoint dropped it; exec is not its descendant.
        MODE["drop"] = True
        start_box()
        holder = holder_capbnd()
        execbnd = exec_capbnd()
        h.control(
            "the container's long-lived process really did lose it",
            _bit_set(holder, 12) is False,
            f"holder CapBnd={holder} — bit 12 clear, so the drop took",
        )
        h.control(
            "the allowlist enforced before exec touched it (dropped arm)",
            probe_from_exec(DENIED) == "000" and probe_from_exec(ALLOWED) != "000",
        )
        after = probe.sample("with the entrypoint's subtree dropped")

        h.measure(
            "the exec'd process's bounding set",
            execbnd,
            f"against the holder's {holder}; equal means exec is outside the drop",
        )
        h.measure("the denied host as seen from exec, after its flush", LAST.get("denied_after"))

        h.expect(
            "the entrypoint's drop also covers processes the daemon starts with "
            "`container exec`",
            after,
            want=False,
        )

        h.not_tried(
            "whether a guest process can REACH `container exec`. It cannot — it is a "
            "host-side command — so nothing here is an escape for the errant-agent threat "
            "model. This measures a property of yoloAI's own tooling",
            "`--cap-drop NET_ADMIN` on the container itself, which would fix the exec path "
            "and break the install path, since the allowlist needs the capability to be "
            "written. Whether apple's container can express 'drop after start' is unasked",
            "tart and seatbelt. tart has no capability model of this shape and seatbelt has "
            "no container at all",
            "whether yoloAI's `exec` uses `container exec` on apple, which is a code "
            "question this run does not open",
            "the canary. `pf-liveness-detect.txt`'s probe reaches the guest this way, so "
            "the result bears on it, but no canary was run here",
        )
        return 0 if h.report() else 1
    finally:
        cleanup()


def _bit_set(hexmask: str, bit: int) -> bool:
    try:
        return bool(int(hexmask, 16) & (1 << bit))
    except ValueError:
        return False


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
