#!/usr/bin/env python3
# ABOUTME: W7 — pf-lifecycle.txt could not exercise the re-read half of the lifecycle rule,
# ABOUTME: because a returning sandbox reclaims its old index. DF190's workaround should
# ABOUTME: make it reachable, and the dangerous release paths were never tried at all.

"""W7: the half of the lifecycle rule that has never been run.

`pf-lifecycle.txt` implemented *withdraw on detach* and raced it — 33 ms against a stranger
arriving at 816 ms, a 783 ms margin. Its other half was not so lucky, and the file says so
rather than passing:

> *The re-read half is NOT REACHABLE by this route and stays unverified: a returning sandbox
> reclaims its old index rather than taking a new one, so the case that half exists for
> never arises.*

That reclaim is [DF190](../../findings-unresolved.md), and DF190 now has a workaround —
**delete the network when its last sandbox goes**, which stops the departing network's vmnet
helper outliving it. If the workaround is applied, a returning sandbox *cannot* reclaim, so
it must land somewhere new — and the case the re-read half exists for finally arises. So the
thing that made the half untestable is the thing whose fix makes it testable.

Two arms, one variable:

- **`reclaim`** — the state `pf-lifecycle.txt` measured. Stop the sandbox, leave its network
  in place, let a stranger take the index, bring it back. It reclaims.
- **`workaround`** — identical, except the network is deleted while empty. It cannot reclaim.

**And the release paths nobody tried.** The results README is explicit that
`pf-lifecycle.txt` *"stops sandboxes cleanly. `container stop` is not the dangerous case; a
killed VM or a daemon restart may release an index differently, and that is where a
lifecycle rule would actually be tested. Untried."* So a third arm compares how long the
index stays held after a clean `stop`, after a `kill`, and across a daemon restart.

**The probe is "the returning sandbox got its ORIGINAL index back"**, which is the one fact
both halves turn on, and its baseline is the reclaim arm where it must come out **true**.

Instrument boundary: the release-timing arm reports wall-clock intervals and names what is
inside each — the backend's own teardown latency is measured separately from anything
yoloAI would do, because `pf-lifecycle.txt` L4 found 5212 ms of the former being counted as
the latter until it was split out.

Run it as: `python3 w7_lifecycle_reread.py`
"""

from __future__ import annotations

import json
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

IMAGE = "yoloai-base:latest"
A_NET, A_BOX = "ybw7neta", "ybw7a"
S_NET, S_BOX = "ybw7nets", "ybw7s"

STATE: dict[str, object] = {}


def _q(argv: list[str], timeout: int = 240) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def gw_of(box: str) -> str:
    try:
        net = json.loads(_q(["container", "inspect", box]).stdout)[0]["status"]["networks"][0]
        return str(net["ipv4Gateway"]).split("/")[0]
    except Exception:
        return ""


def bridge_of(gw: str) -> str:
    cur = ""
    for line in _q(["ifconfig", "-a"]).stdout.splitlines():
        if not line.startswith((" ", "\t")):
            cur = line.split(":")[0]
        elif line.strip().startswith("inet ") and line.split()[1] == gw:
            return cur
    return ""


def bridge_exists(br: str) -> bool:
    return br in _q(["ifconfig", "-l"]).stdout.split()


def up(box: str, net: str, *, create_net: bool = True) -> str:
    if create_net:
        _q(["container", "network", "create", net])
    _q(["container", "run", "-d", "--name", box, "--network", net, IMAGE, "sleep", "900"])
    for _ in range(90):
        gw = gw_of(box)
        if gw and bridge_of(gw):
            return bridge_of(gw)
        time.sleep(1)
    return ""


def down(box: str, *, kill: bool = False) -> None:
    if kill:
        if _q(["container", "kill", box]).returncode != 0:
            _q(["container", "stop", "-s", "SIGKILL", box])
    else:
        _q(["container", "stop", box])
    _q(["container", "rm", box])


def cleanup() -> None:
    for b in (A_BOX, S_BOX):
        _q(["container", "stop", b])
        _q(["container", "rm", b])
    for n in (A_NET, S_NET):
        _q(["container", "network", "delete", n])


def wait_gone(br: str, limit: float = 30.0) -> float:
    """Seconds until the bridge disappears. The BACKEND's latency, not ours."""
    t0 = time.monotonic()
    while time.monotonic() - t0 < limit:
        if not bridge_exists(br):
            return time.monotonic() - t0
        time.sleep(0.05)
    return -1.0


def main() -> int:
    h = Harness("W7", "Is the lifecycle rule's re-read half reachable once DF190's "
                      "workaround is applied, and do kill and stop release differently?")
    try:
        cleanup()
        h.require("the apple container daemon is reachable",
                  _q(["container", "ls"]).returncode == 0)

        def reclaimed() -> bool:
            """THE probe. Did the returning sandbox come back on its ORIGINAL index?"""
            return bool(STATE.get("returned") == STATE.get("original"))

        probe = h.probe("the returning sandbox reclaimed its original bridge index",
                        reclaimed)

        for arm, delete_net in (("reclaim", False), ("workaround", True)):
            cleanup()
            a_br = up(A_BOX, A_NET)
            if not a_br:
                h.void_arm(arm, "the sandbox never got a bridge")
                continue
            STATE["original"] = a_br
            h.measure(f"[{arm}] sandbox started on", a_br, arm=arm)

            down(A_BOX)
            released = wait_gone(a_br)
            h.measure(f"[{arm}] backend latency: stop -> bridge released, seconds",
                      round(released, 2),
                      "the backend's own teardown, measured separately because "
                      "pf-lifecycle.txt L4 spent 5212 ms of it inside 'our' reaction time",
                      arm=arm)
            if delete_net:
                # DF190's workaround: delete the network while it is empty, so no vmnet
                # helper survives to re-attach on the same id later.
                _q(["container", "network", "delete", A_NET])

            s_br = up(S_BOX, S_NET)
            h.control(f"[{arm}] a stranger took the freed index",
                      s_br == a_br,
                      f"stranger landed on {s_br}, freed index was {a_br} — without this "
                      "the arm is not testing anything", arm=arm)

            back_br = up(A_BOX, A_NET, create_net=delete_net)
            STATE["returned"] = back_br
            h.measure(f"[{arm}] sandbox returned on", back_br, arm=arm)

            if arm == "reclaim":
                probe.baseline(want=True,
                               detail=f"{a_br} -> stranger -> {back_br}: the reclaim "
                                      "pf-lifecycle.txt could not get past")
            else:
                m = probe.sample("with DF190's workaround applied")
                h.expect(
                    "with the network deleted while empty, a returning sandbox CANNOT "
                    "reclaim its old index — so the re-read half of the lifecycle rule is "
                    "finally reachable",
                    m, want=False, arm=arm,
                )
                h.measure(
                    "does the stranger keep its egress through the return?",
                    _q(["container", "exec", S_BOX, "sh", "-c",
                        "curl -s -o /dev/null -m 6 -w '%{http_code}' http://1.1.1.1/ "
                        "2>/dev/null; echo"]).stdout.strip(),
                    "DF190's displacement is what the workaround exists to stop; a 000 "
                    "here would mean it did not work", arm=arm,
                )

        # ---- ARM 3: the release paths pf-lifecycle.txt never tried --------------------
        rel: dict[str, float] = {}
        for label, kill in (("stop", False), ("kill", True)):
            cleanup()
            br = up(A_BOX, A_NET)
            if not br:
                continue
            down(A_BOX, kill=kill)
            rel[label] = round(wait_gone(br), 2)

        cleanup()
        br = up(A_BOX, A_NET)
        if br:
            t0 = time.monotonic()
            _q(["container", "system", "stop"])
            daemon = -1.0
            while time.monotonic() - t0 < 60:
                if not bridge_exists(br):
                    daemon = time.monotonic() - t0
                    break
                time.sleep(0.05)
            _q(["container", "system", "start"])
            time.sleep(5)
            rel["daemon restart"] = round(daemon, 2)

        h.measure("seconds from the release event to the bridge disappearing", rel,
                  "-1 means it had not gone within the limit. This is the window a "
                  "withdraw-on-detach mechanism has to beat, per release path")
        h.measure(
            "do the release paths differ?",
            len({v for v in rel.values() if v >= 0}) > 1,
            "pf-lifecycle.txt measured only the clean stop and its own bounds call the "
            "others the dangerous case",
        )

        h.not_tried(
            "the withdraw mechanism itself. `pf-lifecycle.txt` implements and races it; "
            "this run measures only WHICH INDEX a returning sandbox lands on and how fast "
            "each release path frees one. No pf rule is loaded here at all",
            "the re-read actually being performed. This establishes that the case is now "
            "reachable — the returning sandbox lands somewhere new — not that a re-read "
            "implementation handles it. That is the next run and it needs the mechanism",
            "a host crash or a hard power loss, which is the release path with no software "
            "involvement at all and the one no harness can schedule",
            "whether DF190's workaround is safe to adopt. Deleting a network when its last "
            "sandbox goes is measured here as preventing the reclaim; what it costs a "
            "sandbox that expects its subnet back is not examined",
            "n. Each arm runs once. `pf-lifecycle.txt`'s own margin is n=1 and this does "
            "not improve on that",
            "tart, which has no per-sandbox networks pf can key on anyway",
        )
        return 0 if h.report() else 1
    finally:
        cleanup()
        _q(["container", "system", "start"])


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
