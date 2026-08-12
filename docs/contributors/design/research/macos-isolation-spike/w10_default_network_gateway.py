#!/usr/bin/env python3
# ABOUTME: W10 — runtime/apple/reach.go binds the credential injector on the DEFAULT vmnet
# ABOUTME: network's gateway and has the guest dial it there. On this host the guest cannot
# ABOUTME: reach that address at all, while a per-sandbox network's gateway answers.

"""W10: a shipped code path whose central premise does not hold here.

Added mid-round, from W5 failing its baseline. W5 wanted a host listener on the guest's
gateway as a stand-in for the credential injector; the guest could not reach it, so the
probe was not measuring what it was believed to measure and v2 voided the run. Chasing
that produced something bigger than W5.

**What `reach.go` claims.** Every apple sandbox attaches to the built-in `default` vmnet
network (`runtime/apple/reach.go:18`), and `InjectorReach` returns
`{BindHost: gw, DialHost: gw}` — the host injector binds the default network's gateway and
the in-guest agent dials the same address. The file states this as measured:

> *Verified on the 2026-06-28 Mac spike: a host process binds 192.168.64.1 and the guest
> curls it successfully.*

**What this host does.** With a host listener bound to `0.0.0.0` and answering on
`192.168.64.1` from the host itself, a guest on the `default` network cannot reach it —
and cannot reach the gateway's DNS either — while it holds **full external egress**. A
guest on a *created* per-sandbox network reaches its own gateway on the same port in the
same second. Neither network has any pf rule loaded; this sits entirely beneath the
enforcement design.

**Why this is the shape of the run.** The comparison IS the control. A guest that cannot
reach a listener proves nothing on its own — the listener could be wrong, the host could be
firewalled, the guest could be dead. Two guests, two network kinds, one listener, one
second, with external egress checked on both: that makes the difference attributable to the
network kind and nothing else. The baseline is taken on the **created** network, which is
where the probe must come out *reachable* before its `unreachable` on the default network
can mean anything.

**Order is varied deliberately.** Trials alternate which guest is created first, because
bridge indices are assigned in creation order (W4) and a result that follows the index
rather than the network kind would otherwise be indistinguishable.

**What this run does NOT do is explain it.** vmnet's NAT gateway is implemented inside the
framework rather than by the host's IP stack, and why a *built-in* network's gateway
behaves differently from a *created* one — when `container network inspect` reports both as
`mode: nat`, same plugin, no options — is not settled here. The fact is decisive for
`reach.go`; the mechanism is not in hand.

Instrument boundary: nothing is timed. No pf rule is loaded by this run at any point, and
the round's blanket sudo grant is not used.

Run it as: `python3 w10_default_network_gateway.py`
"""

from __future__ import annotations

import http.server
import json
import socketserver
import subprocess
import sys
import threading
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

IMAGE = "yoloai-base:latest"
PORT = 18761
TRIALS = 3

# Which guest the probe is currently pointed at. One callable, one variable.
TARGET = {"box": "", "gw": ""}
STATE: dict[str, object] = {}


class _Quiet(http.server.BaseHTTPRequestHandler):
    def do_GET(self) -> None:  # noqa: N802
        self.send_response(204)
        self.end_headers()

    def log_message(self, *a: object) -> None:
        pass


def _q(argv: list[str], timeout: int = 180) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def guest(box: str, script: str) -> str:
    return _q(["container", "exec", box, "sh", "-c", script]).stdout.strip()


def gw_of(box: str) -> str:
    try:
        net = json.loads(_q(["container", "inspect", box]).stdout)[0]["status"]["networks"][0]
        return str(net["ipv4Gateway"]).split("/")[0]
    except Exception:
        return ""


def up(box: str, network: str) -> bool:
    _q(["container", "run", "-d", "--name", box, "--network", network, IMAGE, "sleep", "600"])
    for _ in range(60):
        if guest(box, "echo ok") == "ok" and gw_of(box):
            return True
        time.sleep(1)
    return False


def down(box: str) -> None:
    _q(["container", "stop", box])
    _q(["container", "rm", box])


def gateway_unreachable() -> bool:
    """THE probe, phrased as the NEGATIVE so its baseline can come out the other way.

    The finding is that the default network's gateway cannot be reached, so that is what
    has to be claimed — and a claim needs a baseline that answers the opposite. The
    per-sandbox network supplies it: same callable, same host, same second, one variable.
    Written as `gateway_reachable()` the run refused to render, correctly, because the
    baseline and the claim both wanted True.
    """
    box, gw = TARGET["box"], TARGET["gw"]
    code = guest(box, f"curl -s -o /dev/null -m 6 -w '%{{http_code}}' http://{gw}:{PORT}/ "
                      "2>/dev/null; echo")
    STATE["code"] = code
    return code in ("", "000")


def main() -> int:
    h = Harness("W10", "Can an apple guest on the DEFAULT vmnet network reach a host "
                       "injector bound to its gateway?")
    srv: socketserver.TCPServer | None = None
    created: list[str] = []
    nets: list[str] = []
    try:
        for i in range(TRIALS * 2 + 2):
            down(f"ybw10d{i}")
            down(f"ybw10o{i}")
            _q(["container", "network", "delete", f"ybw10net{i}"])

        h.require("the apple container daemon is reachable",
                  _q(["container", "ls"]).returncode == 0)

        socketserver.TCPServer.allow_reuse_address = True
        srv = socketserver.TCPServer(("0.0.0.0", PORT), _Quiet)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        time.sleep(1)

        probe = h.probe("the guest CANNOT reach a host listener on its own gateway",
                        gateway_unreachable)

        results: list[dict[str, object]] = []
        dropped: list[int] = []
        baselined = False

        for t in range(TRIALS):
            dbox, obox, onet = f"ybw10d{t}", f"ybw10o{t}", f"ybw10net{t}"
            _q(["container", "network", "create", onet])
            nets.append(onet)
            # Alternate which comes up first: bridge indices follow creation order (W4),
            # so a result tracking the index rather than the network kind would look
            # identical unless the order is varied.
            order = [("default", dbox, "default"), ("own", obox, onet)]
            if t % 2 == 1:
                order.reverse()
            for _kind, box, network in order:
                if up(box, network):
                    created.append(box)

            dgw, ogw = gw_of(dbox), gw_of(obox)
            arm = f"t{t}"
            if not (dgw and ogw):
                h.void_arm(arm, "one of the guests never came up")
                for b in (dbox, obox):
                    down(b)
                _q(["container", "network", "delete", onet])
                continue

            # Health FIRST, because a trial whose guests are not both live decides nothing
            # and would otherwise void the whole run. Trial 1 of run 3 is exactly this: the
            # per-sandbox guest lost its network when the default-network guest came up
            # after it -- DF190, reproducing here between a CREATED and the BUILT-IN
            # network, which is a pairing that finding does not currently name.
            ext_d = guest(dbox, "curl -s -o /dev/null -m 6 -w '%{http_code}' "
                                "http://1.1.1.1/ 2>/dev/null; echo")
            ext_o = guest(obox, "curl -s -o /dev/null -m 6 -w '%{http_code}' "
                                "http://1.1.1.1/ 2>/dev/null; echo")
            h.measure(f"[{arm}] external egress, default / own", f"{ext_d} / {ext_o}",
                      arm=arm)
            if ext_d in ("", "000") or ext_o in ("", "000"):
                h.void_arm(arm, f"a guest had no external egress at all (default {ext_d}, "
                                f"own {ext_o}) -- DF190's displacement, not a gateway "
                                "result. Voided rather than scored.")
                dropped.append(t)
                for b in (dbox, obox):
                    down(b)
                _q(["container", "network", "delete", onet])
                continue

            h.control(f"[{arm}] the host itself reaches the listener on the default gateway",
                      _q(["curl", "-s", "-o", "/dev/null", "-m", "5", "-w", "%{http_code}",
                          f"http://{dgw}:{PORT}/"]).stdout.strip() not in ("", "000"),
                      f"so an unreachable guest is not an unbound listener ({dgw}:{PORT})",
                      arm=arm)

            TARGET.update(box=obox, gw=ogw)
            if not baselined:
                # Mechanism-absent = a per-sandbox network, where the gateway DOES answer.
                probe.baseline(want=False)
                baselined = True
            else:
                probe.sample(f"[{arm}] per-sandbox network")
            own = STATE.get("code")
            h.control(f"[{arm}] the per-sandbox network's gateway still answers",
                      str(own) not in ("", "000"),
                      f"{ogw}:{PORT} -> {own} -- this is what makes the default "
                      "network's silence attributable to the network kind", arm=arm)

            TARGET.update(box=dbox, gw=dgw)
            dflt_m = probe.sample(f"[{arm}] DEFAULT network")
            dflt = STATE.get("code")

            dns_d = "ok" if "rc=0" in guest(
                dbox, f"dig @{dgw} +time=2 +tries=1 +short example.com >/dev/null 2>&1; "
                      "echo rc=$?") else "FAILED"
            dns_o = "ok" if "rc=0" in guest(
                obox, f"dig @{ogw} +time=2 +tries=1 +short example.com >/dev/null 2>&1; "
                      "echo rc=$?") else "FAILED"

            results.append({
                "trial": t, "first_up": order[0][0],
                "default_gw": dgw, "own_gw": ogw,
                "default_listener": dflt, "own_listener": own,
                "default_ext": ext_d, "own_ext": ext_o,
                "default_dns": dns_d, "own_dns": dns_o,
            })
            h.measure(f"trial {t} ({order[0][0]} created first)", results[-1], arm=arm)

            h.expect(
                f"[{arm}] a guest on the DEFAULT network CANNOT reach a host injector "
                "bound to its gateway -- contradicting reach.go",
                dflt_m, want=True, arm=arm,
            )

            for b in (dbox, obox):
                down(b)
            _q(["container", "network", "delete", onet])

        h.require("at least two trials produced a pair", len(results) >= 2,
                  f"{len(results)} trials completed")
        h.measure("summary — default listener / own listener, per trial",
                  [(r["trial"], r["default_listener"], r["own_listener"]) for r in results])
        h.measure("summary — default DNS / own DNS, per trial",
                  [(r["trial"], r["default_dns"], r["own_dns"]) for r in results])
        h.measure("trials dropped because a guest lost its network (DF190)", dropped or "none",
                  "reported rather than silently excluded — a run that drops trials without "
                  "saying so is reporting a filtered population")

        h.not_tried(
            "**WHY.** `container network inspect` reports both networks as `mode: nat`, "
            "same plugin, no options, so nothing in the configuration explains the "
            "difference. vmnet's NAT gateway lives inside the framework rather than in the "
            "host's IP stack, and no privileged tracing was done — this run establishes "
            "the fact and not the mechanism",
            "whether it was ever true. `reach.go` cites a 2026-06-28 spike; this host runs "
            "`container` CLI 1.0.0 and nothing here establishes which version changed, or "
            "whether the built-in network's age (created 2026-06-10) matters. Deleting and "
            "recreating the default network was NOT attempted, and it is the obvious next "
            "step",
            "a real broker. The listener is a plain HTTP server standing in for the "
            "injector; nothing here runs `yoloai new --broker` and watches a credential "
            "exchange fail, which is the run that would turn this into a filed defect "
            "rather than a measured asymmetry",
            "what `InjectorReach` does about it. `ipAssignedToHost(gw)` is TRUE here — the "
            "address is on bridge101 — so the reach check passes and the injector binds "
            "successfully. Whether the failure then surfaces as an error or as a hang is "
            "not measured, and reach.go's own comment says the degradation path is a "
            "silent security downgrade",
            "IPv6. Guests hold a v6 address on both network kinds and the gateway may "
            "answer there; only v4 is probed",
            "tart and seatbelt, which have their own reach implementations",
            "more than one host. Every result here is one Mac, one `container` version.",
        )
        return 0 if h.report() else 1
    finally:
        if srv is not None:
            srv.shutdown()
            srv.server_close()
        for b in created:
            down(b)
        for n in nets:
            _q(["container", "network", "delete", n])


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
