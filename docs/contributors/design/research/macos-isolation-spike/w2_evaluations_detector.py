#!/usr/bin/env python3
# ABOUTME: W2 — can enforcement liveness be read host-side from our own anchor's
# ABOUTME: Evaluations counter, instead of asking the guest whether it is contained?
# ABOUTME: DF192's replacement, measured against the fault, the superset and an idle box.

"""W2: the detector that takes the guest out of the trust path.

[DF192](../../findings-unresolved.md) records that the adopted macOS canary asks the guest
whether it is contained — it flushes the allowlist, probes from inside via `container exec`,
and restores — so the hostile agent it exists to detect can answer for it. It is the third
unstated property of that canary: it also mutates live policy, and its restore is unguarded.
Three ways to be wrong, none of which its own output distinguishes.

`macos-pf-privileged-path.md` § *Candidate: a fourth way out* proposes the replacement and
names, precisely, what has to be measured before it can be adopted — and calls none of it an
argument. This runs three of those four; the fourth (whether the grant widens safely) is a
permit/refuse matrix and needs the blanket sudo grant removed for its duration, so it is
**W2b**.

1. **Does `Evaluations` actually stay flat under the shadowed fault?** `man pf.conf` says a
   `quick` match *"aborts the evaluation of the rules in other anchors and the main
   ruleset"*, so an anchor that is never reached cannot count. Predicted — and this
   document's own history is that predictions here have been wrong in both directions, so
   it is induced and read.
2. **Does it still discriminate inside the pinned superset?** The pool inversion
   (`pf-grant-matrix.txt` G5) loads rules for ~41 bridge indices, most naming interfaces
   that do not exist. Whether one live sandbox's own slot counts distinguishably in that
   shape is unmeasured.
3. **Can an idle sandbox be told from an inert one?** `Evaluations: 0` is ambiguous between
   *our anchor is not in the path* and *the sandbox sent nothing*. That ambiguity is the
   free negative the canary already shipped once (`pf-liveness-detect.txt` V3b returned
   HEALTHY for a dead container, a dead daemon, and a guest with no egress), and Linux's
   V6b found the same poll wrong in **both** directions. The proposed disambiguator is a
   host-side traffic signal that needs no grant — `netstat -ib` on the bridge.

**The fault is induced with a sibling sub-anchor, not by editing the main ruleset.**
`pf-anchor-eval.txt` reached this state by losing `/etc/pf.conf`'s `anchor "com.apple/*"`
line, which is hazard 1 — the thing this plan forbids touching, and a repair that needs a
backend restart afterwards. A `pass quick all` in a sub-anchor that sorts *ahead* of ours
produces the same condition by the mechanism the plan actually names, is removed by
flushing one anchor we own, and never writes outside `com.apple`.

**The baseline is taken in the BROKEN state**, which is the right way round and worth
saying: the mechanism this detector reports on is *enforcement being in the path*, so the
mechanism-absent state is the shadowed host. A detector that has not been seen reporting
"not enforcing" while enforcement was genuinely gone has not been shown to be a detector.

Instrument boundary: nothing is timed. `sudo pfctl` runs under the round's blanket grant,
which is scaffolding; no result here is a refusal, so the blanket cannot make one free.

Run it as: `python3 w2_evaluations_detector.py`
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

ANCHOR = "com.apple/yoloai_w2"
# Sub-anchors under a wildcard parent are evaluated in name order, so a `000.` prefix sorts
# ahead of ours AND ahead of the system's own `200.AirDrop` / `250.ApplicationFirewall`.
SHADOW = "com.apple/000.yoloai_w2_shadow"
IMAGE = "yoloai-base:latest"
NET = "ybw2net"
BOX = "ybw2"
ALLOW = "1.1.1.1"
DENY = "1.0.0.1"
POOL_LO, POOL_HI = 100, 140

STATE: dict[str, object] = {}

_PF_NOISE = re.compile(
    r"use of -f option|main ruleset added|/etc/pf\.conf for further|ALTQ|^\s*$", re.I
)


def _q(argv: list[str], timeout: int = 90) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def guest(script: str, timeout: int = 30) -> subprocess.CompletedProcess[str]:
    return _q(["container", "exec", BOX, "sh", "-c", script], timeout=timeout)


def pf_load(anchor: str, rules: str) -> str:
    p = subprocess.run(
        ["sudo", "pfctl", "-a", anchor, "-f", "-"],
        input=rules, capture_output=True, text=True, check=False,
    )
    lines = [ln for ln in (p.stderr or "").splitlines() if not _PF_NOISE.search(ln)]
    return "\n".join(lines).strip()


def pf_flush(anchor: str) -> None:
    _q(["sudo", "pfctl", "-a", anchor, "-F", "all"])


def cleanup() -> None:
    pf_flush(SHADOW)
    pf_flush(ANCHOR)
    _q(["container", "stop", BOX])
    _q(["container", "rm", BOX])
    _q(["container", "network", "delete", NET])


def evaluations() -> int:
    """Sum `Evaluations` across every rule in OUR anchor. This is the whole detector.

    Summed rather than read off one rule: the pool inversion makes most rules name
    interfaces that do not exist, and which rule a given packet lands on depends on the
    sandbox's index. The question the detector asks is *was this anchor in the path at
    all*, which is the sum.
    """
    out = _q(["sudo", "pfctl", "-a", ANCHOR, "-vvs", "rules"]).stdout
    return sum(int(m) for m in re.findall(r"Evaluations:\s+(\d+)", out))


def bridge_packets(bridge: str) -> int:
    """A host-side traffic signal that needs no grant at all. `netstat -ib` is unprivileged.

    Read off the `<Link#n>` row only. `netstat -ib` prints one row per address family and
    the per-family rows carry different column counts, so "the first row whose name matches"
    is a different quantity depending on how the interface is configured. Run 1 also summed
    `Ipkts + Ibytes` — a packet count and a byte count added together — which is why its
    idle window read 0 while the anchor counted 41 evaluations.
    """
    i, o = bridge_counts(bridge)
    return i + o


def bridge_counts(bridge: str) -> tuple[int, int]:
    """(Ipkts, Opkts) off the `<Link#n>` row. Reported separately because which one carries
    guest egress is not obvious — an idle `bridge100` on this host reads Ipkts=1 against
    Opkts=53194, so summing them and asserting "traffic" would hide which direction the
    signal actually lives in."""
    for line in _q(["netstat", "-ib", "-I", bridge]).stdout.splitlines():
        f = line.split()
        if len(f) >= 10 and f[0] == bridge and f[2].startswith("<Link"):
            return int(f[4]), int(f[7])
    return -1, -1


def shape(bridge: str) -> str:
    """The adopted shape, protocol-agnostic per W1, for one live bridge."""
    return (
        "table <yw2_dst> persist\n"
        f"pass  in  quick on {bridge} from any to <yw2_dst> no state\n"
        f"pass  out quick on {bridge} from <yw2_dst> to any no state\n"
        f"block drop in  quick on {bridge} from any to any\n"
        f"block drop out quick on {bridge} from any to any\n"
    )


def superset(live_bridge: str) -> str:
    """The inverted pool: one slot per bridge INDEX, most naming interfaces that do not exist."""
    out = []
    for i in range(POOL_LO, POOL_HI + 1):
        out.append(f"table <yw2_dst_{i}> persist")
    for i in range(POOL_LO, POOL_HI + 1):
        out += [
            f"pass  in  quick on bridge{i} from any to <yw2_dst_{i}> no state",
            f"pass  out quick on bridge{i} from <yw2_dst_{i}> to any no state",
            f"block drop in  quick on bridge{i} from any to any",
            f"block drop out quick on bridge{i} from any to any",
        ]
    STATE["superset_slot"] = f"yw2_dst_{live_bridge.removeprefix('bridge')}"
    return "\n".join(out) + "\n"


def make_traffic() -> None:
    """Identical packets in every arm: same protocol, same path, same destinations."""
    guest(f"curl -s -o /dev/null -m 3 http://{DENY}/ 2>/dev/null; "
          f"curl -s -o /dev/null -m 3 http://{ALLOW}/ 2>/dev/null; true")


def denied_reachable() -> bool:
    out = guest(
        f"curl -s -o /dev/null -m 5 -w '%{{http_code}}' http://{DENY}/ 2>/dev/null; echo"
    ).stdout.strip()
    STATE["denied"] = out
    return out not in ("", "000")


def main() -> int:
    h = Harness(
        "W2",
        "Can enforcement liveness be read from our own anchor's Evaluations counter?",
    )
    try:
        cleanup()
        _q(["container", "network", "create", NET])
        _q(["container", "run", "-d", "--name", BOX, "--network", NET, IMAGE, "sleep", "1800"])
        for _ in range(60):
            if guest("echo ok").stdout.strip() == "ok":
                break
            time.sleep(1)
        h.require("the guest is up", guest("echo ok").stdout.strip() == "ok")

        net = json.loads(_q(["container", "inspect", BOX]).stdout)[0]["status"]["networks"][0]
        gw = net["ipv4Gateway"].split("/")[0]
        bridge = ""
        for line in _q(["ifconfig", "-a"]).stdout.splitlines():
            if not line.startswith((" ", "\t")):
                cur = line.split(":")[0]
            elif line.strip().startswith("inet ") and line.split()[1] == gw:
                bridge = cur
                break
        h.require("the guest's gateway is on a host bridge", bool(bridge), f"gateway {gw}")
        h.measure("guest", f"{BOX} via {gw} on {bridge}")

        # -- the detector, as one named callable sampled in every state -----------------
        def detector_says_in_path() -> bool:
            before = evaluations()
            make_traffic()
            after = evaluations()
            STATE["delta"] = f"{before} -> {after}"
            return after > before

        probe = h.probe("our anchor counted evaluations for the guest's traffic",
                        detector_says_in_path)

        # ================= ARM 1: the fault, then the healthy host =====================
        err = pf_load(ANCHOR, shape(bridge))
        h.require("our shape loaded", not err, err)
        _q(["sudo", "pfctl", "-a", ANCHOR, "-t", "yw2_dst", "-T", "add", gw, ALLOW])

        # The mechanism this detector reports on is ENFORCEMENT BEING IN THE PATH, so the
        # mechanism-absent state is the shadowed host — and that is where the baseline
        # belongs. A `quick` match in a sub-anchor sorting ahead of ours aborts evaluation
        # of every later anchor, which is the fault by the mechanism the plan names.
        err = pf_load(SHADOW, "pass quick all\n")
        h.require("the shadowing anchor loaded", not err, err)

        h.control(
            "the shadowed host is genuinely fail-OPEN, so the fault is real",
            denied_reachable(),
            f"denied host answers {STATE.get('denied')} while our rules are loaded and "
            "correct — this is pf-anchor-eval's three-green-checks state, reproduced",
        )
        # No `detail=STATE.get(...)` here: the argument is evaluated before the call that
        # runs the probe and fills STATE, so it would print the previous state's number
        # beside this one's verdict. W1 shipped that for one run.
        probe.baseline(want=False)
        h.measure("evaluations across the shadowed baseline", STATE.get("delta"),
                  "an anchor pf never reaches cannot count")

        pf_flush(SHADOW)
        h.control(
            "with the shadow gone the sandbox is contained again",
            denied_reachable() is False,
            f"denied host answers {STATE.get('denied')}",
        )
        healthy = probe.sample("healthy host, same traffic, same path")
        h.measure("evaluations across the healthy sample", STATE.get("delta"))
        h.expect(
            "the counter advances exactly when our anchor is in the path",
            healthy, want=True,
        )

        # ================= ARM 2: the same question inside the superset ================
        pf_flush(ANCHOR)
        err = pf_load(ANCHOR, superset(bridge))
        if err:
            h.void_arm("superset", f"the superset did not load: {err}")
        else:
            slot = str(STATE["superset_slot"])
            _q(["sudo", "pfctl", "-a", ANCHOR, "-t", slot, "-T", "add", gw, ALLOW])
            # `-s rules` does NOT print `@N` prefixes; only `-vvs rules` does. Run 1 counted
            # `^@\d+` against the plain form and reported 0 rules loaded while 164 were
            # enforcing — a predicate that matched nothing, printed beside a working arm.
            n_rules = len([
                ln for ln in _q(["sudo", "pfctl", "-a", ANCHOR, "-s", "rules"]).stdout
                .splitlines() if ln.strip().startswith(("pass", "block"))
            ])
            h.measure("superset rules loaded", n_rules,
                      f"bridge{POOL_LO}-bridge{POOL_HI}, live slot {slot}", arm="superset")
            h.control("[superset] the sandbox is contained by its own slot",
                      denied_reachable() is False,
                      f"denied host answers {STATE.get('denied')} — keyed only by table "
                      "membership, no rule text written", arm="superset")

            err = pf_load(SHADOW, "pass quick all\n")
            h.control("[superset] the shadow re-opens it", denied_reachable(),
                      f"denied host answers {STATE.get('denied')}", arm="superset")
            shadowed_super = probe.sample("superset, shadowed")
            h.measure("[superset] evaluations while shadowed", STATE.get("delta"),
                      arm="superset")
            pf_flush(SHADOW)
            healthy_super = probe.sample("superset, healthy")
            h.measure("[superset] evaluations while healthy", STATE.get("delta"),
                      arm="superset")

            h.expect(
                "the counter still discriminates with 41 indices loaded, most of them "
                "naming interfaces that do not exist",
                healthy_super, want=True, arm="superset",
            )
            h.measure("[superset] shadowed sample read", shadowed_super.value,
                      "False is the inert signature", arm="superset")

        # ================= ARM 3: idle versus inert ====================================
        # `Evaluations: 0` is ambiguous, and that ambiguity is the free negative the canary
        # shipped. The proposed disambiguator is a host-side traffic signal needing no grant.
        pf_flush(SHADOW)
        pf_flush(ANCHOR)
        pf_load(ANCHOR, shape(bridge))
        _q(["sudo", "pfctl", "-a", ANCHOR, "-t", "yw2_dst", "-T", "add", gw, ALLOW])

        # TWO idle windows, because the capture below is a candidate instrument-in-region
        # defect and this directory has a specimen of that costing a 2.09x inflation
        # (`pf-pool-scaling.txt` run 1). `tcpdump` puts the interface in promiscuous mode,
        # which changes what the interface receives — and therefore what pf evaluates. The
        # quiet window runs FIRST and with nothing attached.
        eq0, (iq0, oq0) = evaluations(), bridge_counts(bridge)
        time.sleep(30)
        eq1, (iq1, oq1) = evaluations(), bridge_counts(bridge)
        h.measure("idle window with NO capture attached (30s): evaluations, Ipkts, Opkts",
                  f"{eq1 - eq0}, {iq1 - iq0}, {oq1 - oq0}",
                  "the uncontaminated floor")

        e0, (i0, o0) = evaluations(), bridge_counts(bridge)
        # What the idle traffic IS, rather than "whatever this is". A detector whose floor
        # is set by unidentified background traffic has an unexamined dependency on it.
        # Run CONCURRENTLY with the second idle window, so it observes the same 30 seconds
        # the counters do — and terminated rather than waited on, because a `-c` count that
        # never fills would otherwise hang the run (it did).
        cap_proc = subprocess.Popen(
            ["sudo", "tcpdump", "-i", bridge, "-nn", "-q", "-l"],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True,
        )
        time.sleep(30)
        e_idle, (i1, o1) = evaluations(), bridge_counts(bridge)
        cap_proc.terminate()
        try:
            cap_out = cap_proc.communicate(timeout=5)[0] or ""
        except subprocess.TimeoutExpired:
            cap_proc.kill()
            cap_out = ""
        census: dict[str, int] = {}
        for ln in cap_out.splitlines():
            m = re.search(r"\b(ICMP6|ICMP|UDP|tcp|TCP)\b", ln)
            key = (m.group(1).upper() if m else "other")
            if ".5353" in ln or "5353" in ln:
                key = "mDNS"
            elif ".67" in ln or ".68" in ln:
                key = "DHCP"
            census[key] = census.get(key, 0) + 1
        h.measure("what the idle bridge traffic is (tcpdump over the same 30s)",
                  ", ".join(f"{k}={v}" for k, v in sorted(census.items())) or "nothing captured",
                  f"{len(cap_out.splitlines())} packets — the floor a zero reading has to "
                  "be compared against")
        make_traffic()
        e_busy, (i2, o2) = evaluations(), bridge_counts(bridge)
        b_idle, b0 = i1 + o1, i0 + o0
        b_busy = i2 + o2

        h.measure("idle window WITH the capture attached (30s): evaluations, Ipkts, Opkts",
                  f"{e_idle - e0}, {i1 - i0}, {o1 - o0}",
                  "compare against the quiet window above — a large gap means the capture "
                  "is inside the region it measures, and every idle figure here is the "
                  "instrument's")
        h.measure(
            "does attaching a capture change the floor?",
            f"quiet {eq1 - eq0} vs captured {e_idle - e0} evaluations over the same "
            f"interval on the same host",
            "promiscuous mode changes what the interface receives, so this is checked "
            "rather than assumed",
        )
        h.measure("busy window: evaluations, Ipkts, Opkts",
                  f"{e_busy - e_idle}, {i2 - i1}, {o2 - o1}")
        pairing = h.measure(
            "the pairing distinguishes idle from inert",
            (b_busy - b_idle) > 0 and (e_busy - e_idle) > 0,
            "a healthy but silent sandbox shows both flat; a shadowed but talking one "
            "shows bridge packets moving while evaluations do not",
        )
        h.expect(
            "an unprivileged host-side traffic signal moves with the anchor's counter, so "
            "a quiet sandbox need not be read as an inert one",
            pairing, want=True,
            unbaselined="a pairing check across two counters, not a before/after on one "
                        "mechanism. The half that matters — bridge packets moving while "
                        "evaluations stay flat — is the shadowed baseline above, taken "
                        "from the same anchor on the same host",
        )

        h.not_tried(
            "the OTHER shadowing fault. This induces the one `man pf.conf` describes — a "
            "`quick` match in an anchor evaluated earlier. `pf-anchor-eval.txt`'s host had "
            "lost the main ruleset's `anchor \"com.apple/*\"` line instead, which is a "
            "different cause with the same signature and was not reproduced here because "
            "inducing it means writing the main ruleset (hazard 1) and repairing with a "
            "backend restart",
            "whether the grant can widen to `-vvs rules` safely. That is a permit/refuse "
            "matrix and needs the blanket sudo grant removed for its duration — W2b",
            "what supplies the idle floor, beyond its DIRECTION. `Ipkts` stayed at 0 "
            "through both idle windows while `Opkts` moved, so the evaluations counted "
            "there are host-to-guest traffic and not the sandbox's — but the capture's "
            "composition varied run to run (mDNS and ICMPv6 in some, almost nothing in "
            "others) and the count itself ranged 27-944 across five 30s windows. So the "
            "floor is real, host-side, and NOT a rate. A detector treating a non-zero "
            "count as 'this sandbox is alive' is reading someone else's traffic; the "
            "signal that actually tracks the guest is `Ipkts`, which is 0 idle and moved "
            "to 9 under load",
            "a hostile guest against this detector. It has no guest component at all, "
            "which is the point, but nothing here tries to influence the counter from "
            "inside the sandbox by shaping traffic",
            "what the detector costs. Every read is a `sudo pfctl` at ~7.9 ms "
            "(`pf-pool-scaling.txt`) and nothing multiplies that by a polling cadence",
            "an idle sandbox over a REALISTIC interval. The idle windows here are 30 "
            "seconds each on a machine doing nothing; a sandbox idle for an hour is the "
            "case the ambiguity actually arises in, and the floor is run-to-run noisy "
            "enough that one 30s sample is not a rate",
            "the counter's behaviour across a pf reload, a reboot, or `pfctl -F all`. "
            "Evaluations reset with the ruleset, so a detector comparing against a stored "
            "previous value has a wraparound case nobody has looked at",
            "multiple sandboxes. One guest, one slot; whether one sandbox's traffic can "
            "make another's slot look alive is unasked and is the obvious next question",
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
