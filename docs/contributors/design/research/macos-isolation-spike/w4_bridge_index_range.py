#!/usr/bin/env python3
# ABOUTME: W4 — the inverted pool pins rules for a fixed range of bridge indices, and a
# ABOUTME: sandbox landing outside it meets no rule at all. Is the range boundable, and is
# ABOUTME: the fail-open real?

"""W4: the hazard the pool inversion creates, and whether it can be closed.

`pf-grant-matrix.txt` G5 found that interface keying fits D132's grant provided the pool is
inverted — one slot per **bridge index** rather than per sandbox, with the pinned file
enumerating every index the host could hand out. Its own bounding section names the cost and
declines to measure it:

> *What happens when a sandbox lands on an index OUTSIDE the superset range. It would meet
> no rule at all and be silently unenforced — fail-open, the worst direction. That needs a
> preflight assertion nobody has written.*

Fail-open is the direction that does not announce itself, and this corpus has three separate
specimens of it (`pf-assumptions.txt` D6, `pf-anchor-eval.txt`, `pf-flush-reference.txt`).
So two questions, and the second only matters if the first says yes:

1. **Is it real?** Load the superset with the live sandbox's index *excluded* and see whether
   it is enforced. Predicted unenforced; predictions in this area have been wrong in both
   directions in this very document.
2. **Is the range boundable?** `net-ceiling.txt` established that the vmnet allocator **fills
   holes** — a deleted network's subnet is handed straight back. If bridge indices behave the
   same way, the highest index in use is a function of *concurrent* networks rather than of
   how many have ever existed, and a fixed range is sound. If instead the index advances
   monotonically, no fixed range can be safe and the inversion needs a different shape.

That second question is the one that decides whether the whole mechanism survives, and it is
cheap to ask.

**The probe is `contained()`, not `reachable()`, so the fail-open renders as a FAIL rather
than as a passing claim about a hole.** Its baseline is taken with no rules loaded at all,
where it must read False.

Instrument boundary: nothing is timed. `sudo pfctl` runs under the round's blanket grant,
which is scaffolding; no result here is a refusal.

Run it as: `python3 w4_bridge_index_range.py`
"""

from __future__ import annotations

import json
import re
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

ANCHOR = "com.apple/yoloai_w4"
IMAGE = "yoloai-base:latest"
ALLOW = "1.1.1.1"
DENY = "1.0.0.1"
FLEET = 6

STATE: dict[str, object] = {}

_PF_NOISE = re.compile(
    r"use of -f option|main ruleset added|/etc/pf\.conf for further|ALTQ|^\s*$", re.I
)


def _q(argv: list[str], timeout: int = 120) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def names(n: int) -> tuple[str, str]:
    return f"ybw4net{n}", f"ybw4box{n}"


def cleanup() -> None:
    _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
    for i in range(FLEET + 4):
        net, box = names(i)
        _q(["container", "stop", box])
        _q(["container", "rm", box])
        _q(["container", "network", "delete", net])


def bridges() -> dict[str, str]:
    """Every bridge on the host, mapped to its inet address."""
    out: dict[str, str] = {}
    cur = ""
    for line in _q(["ifconfig", "-a"]).stdout.splitlines():
        if not line.startswith((" ", "\t")):
            cur = line.split(":")[0]
        elif line.strip().startswith("inet ") and cur.startswith("bridge"):
            out[cur] = line.split()[1]
    return out


def start(i: int) -> tuple[str, str] | None:
    """Create a network, attach a guest, return (bridge, gateway). None if it never came up."""
    net, box = names(i)
    _q(["container", "network", "create", net])
    _q(["container", "run", "-d", "--name", box, "--network", net, IMAGE, "sleep", "1800"])
    for _ in range(60):
        insp = _q(["container", "inspect", box]).stdout
        try:
            nets = json.loads(insp)[0]["status"]["networks"][0]
        except Exception:
            time.sleep(1)
            continue
        gw = nets.get("ipv4Gateway", "").split("/")[0]
        if gw:
            for br, addr in bridges().items():
                if addr == gw:
                    return br, gw
        time.sleep(1)
    return None


def stop(i: int) -> None:
    net, box = names(i)
    _q(["container", "stop", box])
    _q(["container", "rm", box])
    _q(["container", "network", "delete", net])


def superset(lo: int, hi: int) -> str:
    out = [f"table <yw4_dst_{i}> persist" for i in range(lo, hi + 1)]
    for i in range(lo, hi + 1):
        out += [
            f"pass  in  quick on bridge{i} from any to <yw4_dst_{i}> no state",
            f"pass  out quick on bridge{i} from <yw4_dst_{i}> to any no state",
            f"block drop in  quick on bridge{i} from any to any",
            f"block drop out quick on bridge{i} from any to any",
        ]
    return "\n".join(out) + "\n"


def load(rules: str) -> str:
    _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
    p = subprocess.run(["sudo", "pfctl", "-a", ANCHOR, "-f", "-"],
                       input=rules, capture_output=True, text=True, check=False)
    lines = [ln for ln in (p.stderr or "").splitlines() if not _PF_NOISE.search(ln)]
    return "\n".join(lines).strip()


def main() -> int:
    h = Harness("W4", "Is the inverted pool's index range boundable, and is landing outside "
                      "it really fail-open?")
    box0 = ""
    try:
        cleanup()
        h.require("the apple container daemon is reachable",
                  _q(["container", "ls"]).returncode == 0)

        pre = bridges()
        h.measure("bridges already present before this run", sorted(pre) or "none",
                  "the index space is shared — tart and apple both allocate from it")

        # ============ ARM 1: what indices does this host actually hand out? ============
        seen: list[tuple[int, str, str]] = []
        for i in range(FLEET):
            got = start(i)
            if got is None:
                h.measure(f"guest {i}", "never got a bridge")
                continue
            seen.append((i, got[0], got[1]))
        h.require("at least three guests came up on bridges", len(seen) >= 3,
                  f"{len(seen)} of {FLEET}")

        idx = [int(br.removeprefix("bridge")) for _, br, _ in seen]
        h.measure("indices handed out, in creation order", idx)
        h.measure("index range observed", f"{min(idx)}..{max(idx)}")
        h.measure("are they contiguous from the lowest free index",
                  idx == list(range(min(idx), min(idx) + len(idx))),
                  "contiguous-from-lowest is what makes a fixed pinned range sound")

        # ---- the deciding question: hole-filling, or monotonic advance? --------------
        victim = seen[len(seen) // 2]
        victim_idx = int(victim[1].removeprefix("bridge"))
        stop(victim[0])
        time.sleep(3)
        h.control(f"the victim's bridge{victim_idx} is gone after its guest is removed",
                  f"bridge{victim_idx}" not in bridges(),
                  "otherwise the next allocation is not being offered a hole at all")

        got = start(FLEET + 1)
        h.require("the replacement guest came up", got is not None)
        assert got is not None
        replacement_idx = int(got[0].removeprefix("bridge"))
        filled = h.measure(
            "a new network takes the FREED index rather than advancing past the high-water mark",
            replacement_idx == victim_idx,
            f"freed bridge{victim_idx}, replacement landed on bridge{replacement_idx}, "
            f"high-water was bridge{max(idx)}",
        )
        h.expect(
            "bridge indices fill holes, so the highest index in use is a function of "
            "CONCURRENT networks and a fixed pinned range can be sound",
            filled, want=True,
            unbaselined="an allocator-policy observation, not a before/after on a "
                        "mechanism. Its negative — an index advancing past the high-water "
                        "mark — is what the same measurement would have shown, and "
                        "net-ceiling.txt records the identical hole-filling behaviour for "
                        "SUBNETS independently",
        )

        # ============ ARM 2: is landing outside the pinned range fail-open? ============
        live_idx = int(got[0].removeprefix("bridge"))
        live_gw = got[1]
        box0 = names(FLEET + 1)[1]

        def contained() -> bool:
            out = _q(["container", "exec", box0, "sh", "-c",
                      f"curl -s -o /dev/null -m 5 -w '%{{http_code}}' http://{DENY}/ "
                      "2>/dev/null; echo"]).stdout.strip()
            STATE["code"] = out
            return out in ("", "000")

        probe = h.probe("the sandbox is contained", contained)
        _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
        probe.baseline(want=False, detail="no rules loaded anywhere")

        # covering superset: the sandbox's index IS in the pinned range
        err = load(superset(live_idx - 2, live_idx + 2))
        h.require("the covering superset loaded", not err, err)
        _q(["sudo", "pfctl", "-a", ANCHOR, "-t", f"yw4_dst_{live_idx}", "-T", "add",
            live_gw, ALLOW])
        covered = probe.sample(f"superset covers bridge{live_idx}")
        h.measure("denied host under the covering superset", STATE.get("code"))
        h.expect("a sandbox inside the pinned range is enforced", covered, want=True)

        # excluding superset: identical shape, range shifted off the live index
        err = load(superset(live_idx + 10, live_idx + 14))
        h.require("the excluding superset loaded", not err, err)
        n_rules = len([ln for ln in _q(["sudo", "pfctl", "-a", ANCHOR, "-s", "rules"])
                       .stdout.splitlines() if ln.strip().startswith(("pass", "block"))])
        h.control("the excluding superset really did load its rules", n_rules > 0,
                  f"{n_rules} rules loaded, none of them naming bridge{live_idx}")
        outside = probe.sample(f"superset covers bridge{live_idx + 10}..{live_idx + 14} only")
        h.measure("denied host under the excluding superset", STATE.get("code"))
        h.expect(
            "a sandbox on an index outside the pinned range is still enforced",
            outside, want=True,
        )

        h.not_tried(
            "the ceiling. `net-ceiling.txt` created 16 networks without finding a limit and "
            f"this creates {FLEET}; neither establishes what the allocator's maximum index "
            "is, only that it fills holes below the high-water mark",
            "tart and apple allocating CONCURRENTLY from the same index space. Both were "
            "observed using bridge100+, and a mixed fleet is `pf-mixed-backend.txt`'s "
            "subject — but nothing here starts a tart VM while apple networks exist and "
            "watches the indices interleave",
            "a preflight assertion. This measures that the hazard is real and that the "
            "range is boundable in principle; it does not write the check, and where that "
            "check would run — before the agent, per the ordering dependency — is a design "
            "question this run does not open",
            "whether the index is knowable EARLY enough. The bridge appears when a container "
            "attaches, and `host-controlled-agent-launch.md` is the dependency that gives "
            "the host a turn between attach and agent start. Untouched here",
            "a host that has been up for weeks. Indices here are allocated on a machine "
            "whose bridge table this run largely created; long-running hosts with churned "
            "networks are the case where a monotonic counter would have shown itself",
            "IPv6, and whether a bridge outside the range leaks differently over v6 than v4",
        )
        return 0 if h.report() else 1
    finally:
        cleanup()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
