#!/usr/bin/env python3
# ABOUTME: W10 — on one vmnet bridge index a guest cannot reach ANYTHING addressed to its
# ABOUTME: own gateway, while everything routed through it works. That gateway is where
# ABOUTME: runtime/apple/reach.go binds the credential injector.

"""W10: the injector binds an address its guests sometimes cannot reach, and why.

Added mid-round from W5 failing its baseline: W5 wanted a host listener on the guest's
gateway as a stand-in for the credential injector, and the guest could not reach it.

**What the design depends on.** Every yoloAI apple sandbox lands on the built-in `default`
vmnet network — `runtime/apple/apple.go:236` never passes `--network` — and
`runtime/apple/reach.go` returns `{BindHost: gw, DialHost: gw}`, so the host injector binds
that network's gateway and the in-guest agent dials the same address. The file states it as
measured: *"Verified on the 2026-06-28 Mac spike: a host process binds 192.168.64.1 and the
guest curls it successfully."*

**Run 1 of this file got the cause wrong, and it is kept as
`w10-default-network-gateway-run1-confounded.txt`.** It compared the built-in `default`
network against a *created* per-sandbox one, found the default unreachable and the created
one fine, and concluded the difference was the network kind. It was not: in both of its live
trials the default network happened to hold the lower bridge index and the created one the
higher, so *network kind* and *bridge index* moved together. Its own third trial was the one
that would have separated them and it was voided for an unrelated reason (DF190 displaced a
guest's network). **`Class: confounded-arms`. `Direction: confirmed`** — it agreed with the
hypothesis in play, which is that `reach.go` was wrong about the default network.

Three further interventions settled it, and none of them involves the network kind at all:

- Three *created* networks in one run: `bridge100` reachable, `bridge101` **not**,
  `bridge102` reachable. Same kind, same creation path, same second.
- A synthetic stale bridge occupying index 100 pushes the next network to 101, which is
  unreachable; destroying it lets the next network take 100, which is reachable. That looked
  like *"the bridge after a stale one is broken"*.
- **That reading is refuted too**: occupying 100 *and* 101 pushes the next network to 102,
  which is **reachable**. So it is not adjacency to a stale bridge.

**Two candidate mechanisms were tested and both are dead**, which is worth more than a
guess would be:

- **A missing `lo0` host route for the gateway.** One snapshot showed exactly that pattern
  and it did not survive: a later fleet had the `lo0` route present for *every* gateway,
  including the unreachable one, and another had it absent for gateways that worked.
- **Layer 2.** ARP resolves in both directions on the affected bridge, the host holds an
  entry for the guest and the guest for the gateway, and the member flags are identical to
  a working bridge's.

**What survives is one narrow behaviour, stated no wider than it reproduces.** On the
affected bridge a guest cannot reach a host service bound on its own gateway (`000`), while
on every other bridge in the same fleet it can (`204`). Everything else is healthy there:
external egress is a clean `301`, the **host itself reaches that same address and port**
(`204`), ARP resolves in both directions, and no pf rule is loaded anywhere.

**That is why this is invisible to every existing check.** A sandbox with working egress
looks entirely healthy, and the one thing broken is the one thing the credential broker
needs. Note the gateway's own DNS is recorded per-guest and does **not** co-vary — it fails
on some reachable bridges too — so it is reported and deliberately not built on.

It tracks the bridge index on this host with no exception across a dozen observations,
surviving daemon restarts, different subnets, and both creation orders. "Index 101" is not a
cause; it is a symptom of something this run cannot see, and that is stated rather than
guessed at.

Instrument boundary: nothing is timed, and no pf rule is loaded at any point.

Run it as: `python3 w10_gateway_host_route.py`
"""

from __future__ import annotations

import http.server
import json
import socketserver
import subprocess
import sys
import threading
import time
from dataclasses import dataclass
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

IMAGE = "yoloai-base:latest"
PORT = 18761
FLEET = 3

TARGET = {"box": "", "gw": ""}


@dataclass
class Row:
    """One guest's full state. A dataclass rather than a dict so the checker can see the
    field types — the dict form typed every value as `object` and broke `TARGET.update`."""

    bridge: str
    gw: str
    guest_to_gw: str
    host_to_gw: str
    lo0_route: bool
    gw_dns: str
    external: str
    box: str

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


def bridge_of(gw: str) -> str:
    cur = ""
    for line in _q(["ifconfig", "-a"]).stdout.splitlines():
        if not line.startswith((" ", "\t")):
            cur = line.split(":")[0]
        elif line.strip().startswith("inet ") and line.split()[1] == gw:
            return cur
    return ""


def has_lo0_host_route(gw: str) -> bool:
    """THE mechanism, read from the host with no privilege at all.

    A vmnet gateway is delivered locally only if the host installed a host route for it via
    `lo0`. `netstat -rn` shows it as `<gw> <mac> UHLWI[i] lo0`.
    """
    for line in _q(["netstat", "-rn", "-f", "inet"]).stdout.splitlines():
        f = line.split()
        if len(f) >= 4 and f[0] == gw and f[-1] == "lo0":
            return True
    return False


def names(i: int) -> tuple[str, str]:
    return f"ybw10net{i}", f"ybw10box{i}"


def cleanup() -> None:
    for i in range(FLEET + 2):
        net, box = names(i)
        _q(["container", "stop", box])
        _q(["container", "rm", box])
        _q(["container", "network", "delete", net])


def gateway_unreachable() -> bool:
    """The probe, phrased as the NEGATIVE so its baseline can answer the other way.

    The finding is the unreachability, so that is what has to be claimed — and a claim needs
    a baseline that came out opposite. A bridge whose gateway HAS its host route supplies
    it: same callable, same host, same minute.
    """
    box, gw = TARGET["box"], TARGET["gw"]
    code = guest(box, f"curl -s -o /dev/null -m 6 -w '%{{http_code}}' http://{gw}:{PORT}/ "
                      "2>/dev/null; echo")
    STATE["code"] = code
    return code in ("", "000")


def main() -> int:
    h = Harness("W10", "Why can an apple guest sometimes not reach its own vmnet gateway, "
                       "where the credential injector binds?")
    srv: socketserver.TCPServer | None = None
    try:
        cleanup()
        h.require("the apple container daemon is reachable",
                  _q(["container", "ls"]).returncode == 0)

        socketserver.TCPServer.allow_reuse_address = True
        srv = socketserver.TCPServer(("0.0.0.0", PORT), _Quiet)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        time.sleep(1)
        h.control("the listener answers on the host's own loopback",
                  _q(["curl", "-s", "-o", "/dev/null", "-m", "5", "-w", "%{http_code}",
                      f"http://127.0.0.1:{PORT}/"]).stdout.strip() not in ("", "000"),
                  "so an unreachable guest below is not an unbound listener")

        # Identical networks, identical creation path, one after another. Nothing here
        # varies the network KIND, which is what run 1 wrongly attributed the effect to.
        fleet = []
        for i in range(FLEET):
            net, box = names(i)
            _q(["container", "network", "create", net])
            _q(["container", "run", "-d", "--name", box, "--network", net, IMAGE,
                "sleep", "900"])
            for _ in range(60):
                if guest(box, "echo ok") == "ok" and gw_of(box):
                    break
                time.sleep(1)
            gw = gw_of(box)
            if not gw:
                continue
            fleet.append((box, gw, bridge_of(gw)))

        h.require("at least three guests came up", len(fleet) >= 3, f"{len(fleet)} up")

        rows = []
        for box, gw, br in fleet:
            ext = guest(box, "curl -s -o /dev/null -m 6 -w '%{http_code}' http://1.1.1.1/ "
                             "2>/dev/null; echo")
            route = has_lo0_host_route(gw)
            host_side = _q(["curl", "-s", "-o", "/dev/null", "-m", "5", "-w", "%{http_code}",
                            f"http://{gw}:{PORT}/"]).stdout.strip()
            TARGET.update(box=box, gw=gw)
            g2g = guest(box, f"curl -s -o /dev/null -m 6 -w '%{{http_code}}' "
                             f"http://{gw}:{PORT}/ 2>/dev/null; echo")
            dns = "ok" if "rc=0" in guest(
                box, f"dig @{gw} +time=2 +tries=1 +short example.com >/dev/null 2>&1; "
                     "echo rc=$?") else "FAILED"
            rows.append(Row(bridge=br, gw=gw, guest_to_gw=g2g, host_to_gw=host_side,
                            lo0_route=route, gw_dns=dns, external=ext, box=box))

        h.control("every guest has external egress, so any 000 below is the gateway and "
                  "not a dead sandbox",
                  all(r.external not in ("", "000") for r in rows),
                  ", ".join(f"{r.bridge}->{r.external}" for r in rows))
        h.control("the host itself reaches EVERY gateway on the same port",
                  all(r.host_to_gw not in ("", "000") for r in rows),
                  ", ".join(f"{r.bridge}->{r.host_to_gw}" for r in rows)
                  + " — so an unreachable guest is not an unbound listener, and the "
                    "address is live on the host")

        h.measure("per-guest state", [vars(r) for r in rows])
        good = [r for r in rows if r.guest_to_gw not in ("", "000")]
        bad = [r for r in rows if r.guest_to_gw in ("", "000")]
        h.measure("gateways the guest CAN reach", [r.bridge for r in good])
        h.measure("gateways the guest CANNOT reach", [r.bridge for r in bad])
        h.measure("the two mechanisms tested and refuted",
                  {"lo0_host_route_present": {r.bridge: r.lo0_route for r in rows},
                   "gateway_dns_answers": {r.bridge: r.gw_dns for r in rows}},
                  "if the lo0 route explained it, it would be absent exactly on the "
                  "unreachable bridge; it is not")

        if not good or not bad:
            h.void_arm(
                "reach",
                f"this run produced only one class ({len(good)} reachable, {len(bad)} not), "
                "so the probe could not be shown answering both ways. The effect is "
                "index-tracking but not present in every fleet; re-run rather than reading "
                "this as agreement.",
            )
        else:
            TARGET.update(box=good[0].box, gw=good[0].gw)
            probe = h.probe("the guest CANNOT reach a host listener on its own gateway",
                            gateway_unreachable)
            probe.baseline(want=False)
            h.measure(f"baseline on {good[0].bridge} read", STATE.get("code"), arm="reach")

            TARGET.update(box=bad[0].box, gw=bad[0].gw)
            m = probe.sample(f"{bad[0].bridge}")
            h.measure(f"{bad[0].bridge} read", STATE.get("code"), arm="reach")
            h.expect(
                "a guest on the affected bridge cannot reach a host service on its own "
                "gateway — which is exactly where reach.go binds the credential injector",
                m, want=True, arm="reach",
            )

        h.not_tried(
            "**WHY.** Two candidate mechanisms were tested and refuted in the file above "
            "— a missing lo0 host route and a layer-2 fault — and nothing replaces them. "
            "No vmnet tracing, no daemon instrumentation, no packet capture on the bridge "
            "while a SYN is lost. The effect tracks the bridge INDEX, which is a symptom "
            "and not a cause",
            "whether it was ever different. reach.go cites a 2026-06-28 spike and this host "
            "runs `container` CLI 1.0.0; no other version was tried and the built-in "
            "network was not deleted and recreated",
            "a real broker. The listener stands in for the injector; nothing runs "
            "`yoloai new --broker` and watches a credential exchange fail. That is the run "
            "that would turn this into a filed defect rather than a measured asymmetry",
            "what `InjectorReach` does about it. `ipAssignedToHost(gw)` is TRUE in the "
            "broken case — the address IS on a bridge — so the reach check passes and the "
            "injector binds an address the agent cannot dial. It does not take reach.go's "
            "degradation path, which that file says is a silent security downgrade",
            "a packet capture on the affected bridge while the guest sends. That is the "
            "next step and it is cheap; it would say whether the SYN arrives at the host "
            "at all, which splits the remaining space in half",
            "the boundary of the direction split. Two things addressed to the gateway "
            "fail (a host listener and the gateway's own DNS) and one thing routed through "
            "it works (external egress); nothing here probes ICMP to the gateway, other "
            "gateway-hosted services, or traffic to a THIRD party on the same subnet, "
            "which would say whether it is the gateway address or the bridge's local "
            "delivery in general",
            "IPv6, tart, seatbelt, and any second host",
        )
        return 0 if h.report() else 1
    finally:
        if srv is not None:
            srv.shutdown()
            srv.server_close()
        cleanup()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
