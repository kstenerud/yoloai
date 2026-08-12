#!/usr/bin/env python3
# ABOUTME: W1 — the adopted pf rule shape is written `proto tcp`, and every probe behind
# ABOUTME: this design is TCP too. Does it contain UDP and ICMP, and does DNS survive the
# ABOUTME: egress block the shape needs for revocation?

"""W1: the protocol the whole macOS corpus skipped.

`pf-no-state.txt` settled the rule *shape* — a stateless bidirectional pair plus an egress
block, both ingredients necessary and neither sufficient — and every rule it loaded says
`proto tcp`. So does every rule in `pf-enforce.txt`, `pf-interface-key.txt` and
`pf-grant-matrix.txt`. The results README states the consequence plainly and nothing has
acted on it: *"UDP is untested there, and DNS rides on it."*

That is not a coverage gap, it is a gap in a containment claim. An allowlist that
constrains only TCP is an allowlist an errant agent walks around with a UDP client, and
this corpus already has a specimen of exactly this reasoning error: `pf-main-run.txt`'s
ICMP result is **circular by its own admission** — the block rule was scoped `proto tcp`,
so ICMP passing follows from the qualifier rather than from anything measured. The README
records that the harness was fixed to load a protocol-agnostic form and that **it was never
re-run**. This is the re-run, widened to the protocol that matters.

**Two arms, because "the shape as written" and "the shape it should be" are different
claims and the corpus keeps conflating them.**

- **Arm `as-written`** loads the shape exactly as `pf-no-state.txt` and
  `enforcement-state-reaping.md` state it, `proto tcp` and all. Whatever it fails to
  contain is a hole in the design as it currently stands.
- **Arm `agnostic`** drops the protocol qualifier and nothing else. If the hole closes,
  the fix is one word per rule; if it does not, the mechanism is wrong rather than the
  wording.

**Then the half that decides whether the fix is affordable.** The egress block is what
makes revocation work, and it denies host-initiated traffic to the sandbox unless the
source is allowlisted. The guest resolves through the vmnet gateway, so a
protocol-agnostic egress block puts **DNS itself** inside the policy. That is checked
functionally, in the same arm, rather than reasoned about.

**Sampler liveness is a control, not an assumption.** Every probe here rides
`container exec`. `block drop out` on the bridge could plausibly kill it, and a dead
sampler reads as perfect containment — the same free negative `pf-no-state.txt` guards
against and `pf-liveness-detect.txt` V3b actually shipped. Each arm proves exec still
answers before any of its results are allowed to mean anything.

`ping` is not in the base image, so ICMP is sent from a raw socket by a script dropped into
the guest. The container carries `CAP_NET_RAW` by default, which is asserted rather than
assumed.

Instrument boundary: nothing here is timed. `sudo pfctl` runs under the round's blanket
grant, which is scaffolding — no result below is a refusal, so the blanket cannot make one
free.

Run it as: `python3 w1_protocol_coverage.py`
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

ANCHOR = "com.apple/yoloai_w1"
IMAGE = "yoloai-base:latest"
NET = "ybw1net"
BOX = "ybw1"
ALLOW = "1.1.1.1"
DENY = "1.0.0.1"

STATE: dict[str, str] = {}

ICMP_PY = r'''
import socket, struct, sys
def cksum(b):
    if len(b) % 2: b += b"\0"
    s = sum(struct.unpack("!%dH" % (len(b)//2), b))
    s = (s >> 16) + (s & 0xffff); s += s >> 16
    return (~s) & 0xffff
dst = sys.argv[1]
body = b"yoloai-w1-probe"
hdr = struct.pack("!BBHHH", 8, 0, 0, 4321, 1)
pkt = struct.pack("!BBHHH", 8, 0, cksum(hdr + body), 4321, 1) + body
try:
    s = socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_ICMP)
except PermissionError:
    print("NORAW"); sys.exit(2)
s.settimeout(3)
s.sendto(pkt, (dst, 0))
try:
    while True:
        data, addr = s.recvfrom(2048)
        if addr[0] == dst and len(data) > 20 and data[20] == 0:
            print("OK"); sys.exit(0)
except Exception:
    pass
print("NOREPLY"); sys.exit(1)
'''


def _q(argv: list[str], timeout: int = 60) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def guest(script: str, timeout: int = 30) -> subprocess.CompletedProcess[str]:
    return _q(["container", "exec", BOX, "sh", "-c", script], timeout=timeout)


def cleanup() -> None:
    _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
    _q(["container", "stop", BOX])
    _q(["container", "rm", BOX])
    _q(["container", "network", "delete", NET])


def shape(bridge: str, *, proto_tcp: bool) -> str:
    """The adopted shape. The ONLY difference between the arms is this qualifier."""
    p = "proto tcp " if proto_tcp else ""
    return (
        "table <yw_dst> persist\n"
        f"pass  in  quick on {bridge} {p}from any to <yw_dst> no state\n"
        f"pass  out quick on {bridge} {p}from <yw_dst> to any no state\n"
        f"block drop in  quick on {bridge} {p}from any to any\n"
        f"block drop out quick on {bridge} {p}from any to any\n"
    )


def load(rules: str, gateway: str) -> str:
    """Load a shape and re-claim membership.

    Flushing an anchor destroys TABLE MEMBERSHIP as well as rules, and a shape loaded with
    an empty allowlist enforces nothing while looking correct — `pf-revocation-alt.txt` T3
    lost four arms to exactly that. So every load re-adds, and the caller can check.
    """
    _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
    p = subprocess.run(
        ["sudo", "pfctl", "-a", ANCHOR, "-f", "-"],
        input=rules, capture_output=True, text=True, check=False,
    )
    _q(["sudo", "pfctl", "-a", ANCHOR, "-t", "yw_dst", "-T", "add", gateway, ALLOW])
    return _real_pfctl_error(p.stderr)


# pfctl prints these on EVERY successful `-f`, on stderr. Treating stderr as failure voided
# both arms of run 1 while the rulesets had loaded perfectly — the bash harnesses in this
# directory all carry the same filter, and porting to Python lost it.
_PF_NOISE = re.compile(
    r"use of -f option|main ruleset added|/etc/pf\.conf for further|ALTQ|^\s*$",
    re.I,
)


def _real_pfctl_error(stderr: str) -> str:
    lines = [ln for ln in (stderr or "").splitlines() if not _PF_NOISE.search(ln)]
    return "\n".join(lines).strip()


def held() -> int:
    out = _q(["sudo", "pfctl", "-a", ANCHOR, "-t", "yw_dst", "-T", "show"]).stdout
    return len([x for x in out.split() if x.strip()])


def rule_packets(match: str) -> int:
    """Per-rule counters selected by rule TEXT, never by index.

    Dropping the `proto tcp` qualifier changes the rule text but not the order; selecting
    by index would still work here, and it is not worth the habit — `pf_no_state.sh` was
    burned by an index reader when a rule was inserted ahead of the one being quoted.
    """
    out = _q(["sudo", "pfctl", "-a", ANCHOR, "-vvs", "rules"]).stdout
    total, want = 0, False
    for line in out.splitlines():
        if line.startswith("@"):
            want = match in line
            continue
        if want and "Packets:" in line:
            m = re.search(r"Packets:\s+(\d+)", line)
            if m:
                total += int(m.group(1))
            want = False
    return total


# -- probes. Every one is a zero-arg bool, sampled from the same callable in both arms. --

def tcp_denied_reachable() -> bool:
    out = guest(
        f"curl -s -o /dev/null -m 5 -w '%{{http_code}}' http://{DENY}/ 2>/dev/null; echo"
    ).stdout.strip()
    STATE["tcp_denied"] = out
    return out not in ("", "000")


def udp_denied_reachable() -> bool:
    r = guest(f"dig @{DENY} +time=3 +tries=1 +short example.com 2>/dev/null; echo rc=$?")
    STATE["udp_denied"] = r.stdout.strip().replace("\n", " ")
    return "rc=0" in r.stdout


def icmp_denied_reachable() -> bool:
    r = guest(f"python3 /tmp/icmp.py {DENY}")
    STATE["icmp_denied"] = (r.stdout or r.stderr).strip()
    return r.stdout.strip() == "OK"


def udp_allowed_reachable() -> bool:
    r = guest(f"dig @{ALLOW} +time=3 +tries=1 +short example.com 2>/dev/null; echo rc=$?")
    STATE["udp_allowed"] = r.stdout.strip().replace("\n", " ")
    return "rc=0" in r.stdout


def dns_via_gateway_works() -> bool:
    gw = STATE["gw"]
    r = guest(f"dig @{gw} +time=3 +tries=1 +short example.com 2>/dev/null; echo rc=$?")
    STATE["dns_gw"] = r.stdout.strip().replace("\n", " ")
    return "rc=0" in r.stdout


def exec_alive() -> bool:
    return guest("echo ok").stdout.strip() == "ok"


def main() -> int:
    h = Harness(
        "W1",
        "Does the adopted pf shape contain UDP and ICMP, and does DNS survive its egress block?",
    )
    try:
        cleanup()

        # -- N0 setup: a per-sandbox network, the shape the rewritten design specifies ---
        _q(["container", "network", "create", NET])
        _q(["container", "run", "-d", "--name", BOX, "--network", NET, IMAGE, "sleep", "1800"])
        for _ in range(60):
            if exec_alive():
                break
            time.sleep(1)
        h.require("the guest is up and `container exec` answers", exec_alive())

        insp = _q(["container", "inspect", BOX]).stdout
        net = json.loads(insp)[0]["status"]["networks"][0]
        ip = net["ipv4Address"].split("/")[0]
        gw = net["ipv4Gateway"].split("/")[0]
        STATE["ip"], STATE["gw"] = ip, gw

        bridge = ""
        for line in _q(["ifconfig", "-a"]).stdout.splitlines():
            if not line.startswith((" ", "\t")):
                cur = line.split(":")[0]
            elif line.strip().startswith("inet ") and line.split()[1] == gw:
                bridge = cur
                break
        h.require("the guest's gateway is on a host bridge", bool(bridge), f"gateway {gw}")
        STATE["bridge"] = bridge

        guest(f"cat > /tmp/icmp.py <<'EOF'\n{ICMP_PY}\nEOF")
        h.require(
            "the guest can open a raw ICMP socket",
            guest(f"python3 /tmp/icmp.py {ALLOW}").stdout.strip() != "NORAW",
            "without CAP_NET_RAW every ICMP result below would be free",
        )

        h.measure("guest", f"{BOX} {ip} via {gw} on {bridge}")

        # -- baselines: the anchor is empty, so every probe must REACH ------------------
        p_tcp = h.probe("TCP to the denied host", tcp_denied_reachable)
        p_udp = h.probe("UDP/53 to the denied host", udp_denied_reachable)
        p_icmp = h.probe("ICMP echo to the denied host", icmp_denied_reachable)

        _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
        p_tcp.baseline(want=True, detail="anchor empty — without this every block is free")
        p_udp.baseline(want=True, detail="the protocol the corpus never loaded a rule for")
        p_icmp.baseline(want=True, detail="the protocol pf-main-run.txt tested circularly")

        arms_run = 0
        for arm, proto_tcp in (("as-written", True), ("agnostic", False)):
            err = load(shape(bridge, proto_tcp=proto_tcp), gw)
            if err:
                h.void_arm(arm, f"the ruleset did not load: {err}")
                continue
            arms_run += 1
            h.control(f"[{arm}] membership was re-added after the flush", held() == 2,
                      f"{held()} addresses in <yw_dst>", arm=arm)
            h.control(f"[{arm}] `container exec` still answers, so the sampler is alive",
                      exec_alive(),
                      "a dead sampler reads as perfect containment", arm=arm)

            before_in = rule_packets("block drop in")
            before_out = rule_packets("block drop out")

            # `detail=STATE.get(...)` is NOT available here: Python evaluates the argument
            # before `sample()` runs the probe that fills STATE, so every detail would be
            # the PREVIOUS arm's output printed beside this arm's verdict. Run 2 did that
            # and rendered `False (rc=0)` — a verdict contradicting the number next to it,
            # which is the specific thing this workstream keeps shipping. Sample first,
            # then read.
            t = p_tcp.sample(f"[{arm}] TCP to the denied host")
            raw_tcp = STATE.get("tcp_denied")
            u = p_udp.sample(f"[{arm}] UDP to the denied host")
            raw_udp = STATE.get("udp_denied")
            i = p_icmp.sample(f"[{arm}] ICMP to the denied host")
            raw_icmp = STATE.get("icmp_denied")
            h.measure(f"[{arm}] raw probe output — tcp / udp / icmp",
                      f"{raw_tcp!r} / {raw_udp!r} / {raw_icmp!r}", arm=arm)

            h.control(f"[{arm}] the allowlisted host still answers over UDP",
                      udp_allowed_reachable(),
                      f"dig @{ALLOW}: {STATE.get('udp_allowed')} — an allowlist that blocks "
                      "everything is not an allowlist", arm=arm)

            dns_ok = h.measure(f"[{arm}] DNS through the vmnet gateway still resolves",
                               dns_via_gateway_works(),
                               f"dig @{gw}: {STATE.get('dns_gw')}", arm=arm)

            h.measure(f"[{arm}] block rule packets, in / out",
                      f"{before_in}->{rule_packets('block drop in')} / "
                      f"{before_out}->{rule_packets('block drop out')}",
                      arm=arm)

            h.expect(f"[{arm}] the shape denies TCP to a non-allowlisted host", t,
                     want=False, arm=arm)
            h.expect(f"[{arm}] the shape denies UDP to a non-allowlisted host", u,
                     want=False, arm=arm)
            h.expect(f"[{arm}] the shape denies ICMP to a non-allowlisted host", i,
                     want=False, arm=arm)
            h.expect(
                f"[{arm}] the egress block does not break the guest's own DNS", dns_ok,
                want=True, arm=arm,
                unbaselined="a functional-preservation check. When the design is right this "
                            "reads the same with and without the mechanism, so it has no "
                            "mechanism-absent state that comes out the other way. Recorded "
                            "as a claim rather than a control so that a failure renders "
                            "instead of voiding the arm that found it",
            )

        # v2 reports "all live expectations held" when there are none, which is vacuously
        # true and reads as a pass. Run 1 voided both arms on a stderr filter and printed
        # exactly that. A run with nothing live has no verdict to render.
        h.require(
            "at least one arm loaded its ruleset",
            arms_run > 0,
            "every arm voided, so there is nothing to conclude from",
        )

        h.not_tried(
            "UDP that is not DNS. `dig` is the probe because its exit code is unambiguous; "
            "QUIC on 443, WireGuard, and plain datagram exfiltration are the same table "
            "match in principle and none is run",
            "fragmented UDP, and pf's `scrub`/reassembly interaction with `no state`. A "
            "stateless ruleset has no reassembly context, and nothing here sends a datagram "
            "larger than one MTU",
            "IPv6. `pf-v6-hole.txt` already measured the allowlist as v4-only on both "
            "backends and this run does not re-open it; the protocol-agnostic form here is "
            "still `inet`-implicit and would need its own v6 pair",
            "revocation. `pf-no-state.txt` measured that for TCP; whether removing a UDP "
            "peer from the table stops an in-flight UDP flow is a different question, and a "
            "connectionless one, so its answer may not follow",
            "tart and seatbelt. `tart-net-key.txt` established pf cannot key on tart's "
            "per-VM interfaces at all, so there is no shape to give this test",
            "what the protocol-agnostic form costs. Removing `proto tcp` widens what every "
            "rule must be evaluated against and W3 prices evaluation, not this",
            "the credential broker's injector endpoint, which is W5 and rides the same "
            "egress block this run only checks DNS against",
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
