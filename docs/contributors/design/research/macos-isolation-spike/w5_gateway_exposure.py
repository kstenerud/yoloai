#!/usr/bin/env python3
# ABOUTME: W5 — the egress block makes the guest's own gateway unreachable unless it is
# ABOUTME: allowlisted, and allowlisting a whole address exposes every host service on it.
# ABOUTME: Does credential injection survive, and can the exposure be narrowed?

"""W5: what the adopted shape costs the credential broker, and what the remedy opens.

`pf-no-state.txt` established that revocation needs a `block drop out` on the bridge, and
its own bounding section names the consequence without measuring it:

> *the credential broker's injector endpoint on the gateway address was not exercised, and
> every sandbox's allowlist would have to contain its gateway for injection to work at all.
> That is a design consequence, not a detail.*

W1 then widened every rule to be protocol-agnostic, which can only make this tighter. And
the same hazard arrives independently from the prior-art gate: a maintainer on
`apple/container` #719 records that **the host gateway stays reachable from an `--internal`
network, so any host service bound to `0.0.0.0` is accessible**. So this run asks three
things in order, and the third is the one with teeth:

1. **Does the broker break under the shape?** On apple the injector binds the *vmnet
   gateway* (`runtime/apple/reach.go:61` — `BindHost: gw, DialHost: gw`) and the agent
   dials it there. With the gateway absent from the allowlist, the shape should refuse it.
2. **Does allowlisting the gateway fix it?** The remedy the plan assumes.
3. **What else does that open?** A second host service on a different port, bound to
   `0.0.0.0`, that the sandbox has no business reaching. If the guest can reach it, the
   remedy is "grant the sandbox the whole host gateway" and the maintainer's hazard is ours.

**And the narrowing that would fix it does not fit, which is the point of measuring rather
than proposing.** A rule scoped to `gateway port <injector>` would permit the broker and
nothing else — but the injector binds an **ephemeral** port (`internal/broker/host.go:237`,
`respawnBindPort` returns `"0"` when there is no prior record), so its number is not known
until the sandbox starts. D132's pinned file is static text root wrote at install time and
the unprivileged side can never author a rule. So the port-scoped rule is measured here to
establish that it *would* work, and its unavailability is a fact about the grant rather
than about pf.

**No real broker is started.** A plain HTTP listener on the same address stands in for it,
because what is under test is pf's treatment of `guest → gateway:port`, not the injector's
own behaviour. That substitution is the run's largest bound and is stated again below.

Instrument boundary: nothing is timed. `sudo pfctl` runs under the round's blanket grant,
which is scaffolding.

Run it as: `python3 w5_gateway_exposure.py`
"""

from __future__ import annotations

import http.server
import json
import re
import socketserver
import subprocess
import sys
import threading
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

ANCHOR = "com.apple/yoloai_w5"
IMAGE = "yoloai-base:latest"
NET = "ybw5net"
BOX = "ybw5"
BROKER_PORT = 18751      # stands in for the injector's ephemeral port
BYSTANDER_PORT = 18752   # a host service the sandbox has no business reaching
ALLOW = "1.1.1.1"

STATE: dict[str, object] = {}

_PF_NOISE = re.compile(
    r"use of -f option|main ruleset added|/etc/pf\.conf for further|ALTQ|^\s*$", re.I
)


def _q(argv: list[str], timeout: int = 120) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def guest(script: str) -> str:
    return _q(["container", "exec", BOX, "sh", "-c", script]).stdout.strip()


def reach(addr: str, port: int) -> str:
    return guest(
        f"curl -s -o /dev/null -m 5 -w '%{{http_code}}' http://{addr}:{port}/ 2>/dev/null; echo"
    )


class _Quiet(http.server.BaseHTTPRequestHandler):
    def do_GET(self) -> None:  # noqa: N802
        self.send_response(204)
        self.end_headers()

    def log_message(self, *a: object) -> None:
        pass


def serve(port: int) -> socketserver.TCPServer:
    """Bound to 0.0.0.0 deliberately — that is the maintainer's stated hazard."""
    socketserver.TCPServer.allow_reuse_address = True
    srv = socketserver.TCPServer(("0.0.0.0", port), _Quiet)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


def cleanup() -> None:
    _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
    _q(["container", "stop", BOX])
    _q(["container", "rm", BOX])
    _q(["container", "network", "delete", NET])


def load(bridge: str, *, allow_gateway: bool, gw: str, port_scoped: bool = False) -> str:
    """The adopted shape, protocol-agnostic per W1, with the gateway in or out of <dst>."""
    extra = ""
    if port_scoped:
        # What a narrow rule WOULD look like. Static text, so it can only exist in the
        # pinned file — which is why the ephemeral port matters.
        extra = (
            f"pass  in  quick on {bridge} proto tcp from any to {gw} port {BROKER_PORT} no state\n"
            f"pass  out quick on {bridge} proto tcp from {gw} port {BROKER_PORT} to any no state\n"
        )
    rules = (
        "table <yw5_dst> persist\n"
        + extra
        + f"pass  in  quick on {bridge} from any to <yw5_dst> no state\n"
        f"pass  out quick on {bridge} from <yw5_dst> to any no state\n"
        f"block drop in  quick on {bridge} from any to any\n"
        f"block drop out quick on {bridge} from any to any\n"
    )
    _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
    p = subprocess.run(["sudo", "pfctl", "-a", ANCHOR, "-f", "-"],
                       input=rules, capture_output=True, text=True, check=False)
    members = [ALLOW] + ([gw] if allow_gateway else [])
    _q(["sudo", "pfctl", "-a", ANCHOR, "-t", "yw5_dst", "-T", "add", *members])
    lines = [ln for ln in (p.stderr or "").splitlines() if not _PF_NOISE.search(ln)]
    return "\n".join(lines).strip()


def main() -> int:
    h = Harness("W5", "Does credential injection survive the egress block, and what does "
                      "the remedy expose?")
    servers: list[socketserver.TCPServer] = []
    try:
        servers.append(serve(BROKER_PORT))
        servers.append(serve(BYSTANDER_PORT))

        # A PER-SANDBOX network, which is what the rewritten design specifies — and W10 is
        # why that is stated rather than assumed: on the built-in `default` network the
        # gateway is not reachable from its own guests at all, so this whole experiment is
        # unrunnable there.
        #
        # The bring-up retries because DF190 displaces a network's bridge at random and a
        # guest with no egress would fail the baseline for a reason that has nothing to do
        # with pf — W10 lost a trial to exactly that. The attempt count is REPORTED: a run
        # that silently retries until it gets the state it wants is selecting its data.
        gw = bridge = ""
        attempts = 0
        for attempts in range(1, 4):
            cleanup()
            _q(["container", "network", "create", NET])
            _q(["container", "run", "-d", "--name", BOX, "--network", NET, IMAGE,
                "sleep", "1800"])
            for _ in range(60):
                if guest("echo ok") == "ok":
                    break
                time.sleep(1)
            if guest("echo ok") != "ok":
                continue
            net = json.loads(_q(["container", "inspect", BOX]).stdout)[0]["status"]["networks"][0]
            gw = net["ipv4Gateway"].split("/")[0]
            bridge = ""
            for line in _q(["ifconfig", "-a"]).stdout.splitlines():
                if not line.startswith((" ", "\t")):
                    cur = line.split(":")[0]
                elif line.strip().startswith("inet ") and line.split()[1] == gw:
                    bridge = cur
                    break
            if bridge and reach(gw, BROKER_PORT) not in ("", "000"):
                break
        h.measure("bring-up attempts before the rig was usable", attempts,
                  "more than one means DF190 displaced a network on the way")
        h.require("the guest is up", guest("echo ok") == "ok")
        h.require("the guest's gateway is on a host bridge", bool(bridge), f"gateway {gw}")
        h.measure("rig", f"{BOX} on {bridge}, gateway {gw}, broker stand-in :{BROKER_PORT}, "
                         f"bystander :{BYSTANDER_PORT}")

        def broker_reachable() -> bool:
            code = reach(gw, BROKER_PORT)
            STATE["broker"] = code
            return code not in ("", "000")

        def bystander_reachable() -> bool:
            code = reach(gw, BYSTANDER_PORT)
            STATE["bystander"] = code
            return code not in ("", "000")

        p_broker = h.probe("the guest reaches the injector endpoint on its gateway",
                           broker_reachable)
        p_by = h.probe("the guest reaches an unrelated host service on the same gateway",
                       bystander_reachable)

        _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
        p_broker.baseline(want=True, detail="no rules loaded — both host services answer")
        p_by.baseline(want=True, detail="the bystander answers too, so a later 000 is the rule")

        # -- ARM 1: the shape as adopted, gateway NOT allowlisted ---------------------
        err = load(bridge, allow_gateway=False, gw=gw)
        h.require("the shape loaded", not err, err)
        b1 = p_broker.sample("gateway absent from <dst>")
        h.measure("injector endpoint, gateway not allowlisted", STATE.get("broker"),
                  "000 means credential injection is broken by the adopted shape")
        h.control("the guest still has a network — an allowlisted external host answers",
                  reach(ALLOW, 80) not in ("", "000"),
                  f"{ALLOW}:80 -> {reach(ALLOW, 80)}")
        # Phrased as the break, not as its absence. The baseline is "reachable with no rules
        # loaded", so a claim has to want the opposite — and the opposite IS the finding:
        # the adopted shape takes the injector away unless something is done about it.
        h.expect(
            "the adopted shape cuts the guest off from the injector endpoint unless the "
            "gateway is explicitly allowlisted",
            b1, want=False,
        )

        # -- ARM 2: the remedy the plan assumes, and its cost -------------------------
        err = load(bridge, allow_gateway=True, gw=gw)
        h.require("the shape with the gateway allowlisted loaded", not err, err)
        b2 = p_broker.sample("gateway in <dst>")
        by2 = p_by.sample("gateway in <dst> — the bystander service")
        h.measure("injector endpoint, gateway allowlisted", STATE.get("broker"))
        h.measure("unrelated host service, gateway allowlisted", STATE.get("bystander"),
                  "this is the whole cost of the remedy: <dst> holds ADDRESSES, so "
                  "permitting the injector permits every port on the host's bridge address")
        h.expect(
            "allowlisting the gateway does not also expose unrelated host services on it",
            by2, want=False,
        )
        h.control("the remedy does restore the injector", b2.value is True,
                  f"broker {STATE.get('broker')} with the gateway in <dst>")

        # -- ARM 3: the narrowing that would fix it, if it could be written -----------
        err = load(bridge, allow_gateway=False, gw=gw, port_scoped=True)
        h.require("the port-scoped shape loaded", not err, err)
        b3 = p_broker.sample("port-scoped pass, gateway NOT in <dst>")
        by3 = p_by.sample("port-scoped pass — the bystander service")
        h.measure("injector endpoint under a port-scoped rule", STATE.get("broker"))
        h.measure("unrelated host service under a port-scoped rule", STATE.get("bystander"))
        h.expect("a port-scoped rule closes the bystander", by3, want=False, arm="scoped")
        h.control("and still admits the injector", b3.value is True,
                  f"broker {STATE.get('broker')} under the port-scoped rule", arm="scoped")

        h.measure(
            "can that rule actually be written under D132?",
            False,
            "the injector binds an EPHEMERAL port (internal/broker/host.go:237; "
            "respawnBindPort returns '0' with no prior record), so its number is unknown "
            "until the sandbox starts — and the pinned file is static text root wrote at "
            "install time, which the unprivileged side can never author. The narrow rule "
            "works and is unavailable",
        )

        h.not_tried(
            "**a real injector.** A plain HTTP listener stands in for it on the same "
            "address. What is under test is pf's treatment of guest-to-gateway traffic, "
            "not the broker's behaviour — but nothing here starts `yoloai new` with a "
            "broker configured and watches a real credential exchange survive the shape, "
            "and that is the run an implementer would want",
            "pinning the injector to a FIXED port, which would make the narrow rule "
            "writable. That is a code change with its own consequences (collisions between "
            "concurrent sandboxes, most obviously) and this run only shows why someone "
            "might want it",
            "what else is bound to 0.0.0.0 on a real user's Mac. The bystander here is a "
            "listener this harness started; the hazard is about services the user already "
            "runs, and no census of those was taken",
            "the same question on tart, where the gateway is shared between VMs rather "
            "than per-sandbox — the exposure there is potentially wider and pf cannot key "
            "on tart's interfaces anyway (`tart-net-key.txt`)",
            "UDP to the gateway, other than DNS. W1 covers the resolver on :53; the "
            "injector is TCP and nothing here probes other datagram services on the "
            "gateway address",
            "IPv6. The guest holds a v6 address and the gateway may answer on it, which "
            "would be a second path to the same host services",
        )
        return 0 if h.report() else 1
    finally:
        for s in servers:
            s.shutdown()
            s.server_close()
        cleanup()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
