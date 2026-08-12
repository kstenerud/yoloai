#!/usr/bin/env python3
# ABOUTME: The in-guest allowlist is IPv4-only by design, and apple guests are dual-stack on
# ABOUTME: a shared ULA /64. Can an agent simply use IPv6 and ignore the allowlist entirely?

"""Does IPv6 walk around the in-guest allowlist?

**Post-round, owner-prompted.** Verification round 2 is closed
([`archive/plans/macos-verification-round-2.md`](../../../archive/plans/macos-verification-round-2.md));
this is not one of its items and is not covered by its synthesis. It exists because the owner
asked whether the design leaves the IPv6 door open.

**What was already on record, from two directions.** `firewall.py` installs IPv4 rules only
and says so deliberately — a v6 nameserver is skipped rather than kept, because feeding one to
`iptables -d` aborted the whole install and took the correct v4 rules down with it (DF134), and
*"full dual-stack filtering … is the network-families design, not this fix."* Separately,
[`pf-v6-hole.txt`](results/pf-v6-hole.txt) measured that **host pf** has the identical omission:
a destination refused over v4 answers over v6 on both backends, and pf *does* enforce v6 inside
the anchor, so the hole is an omission in the rules rather than a limit of the mechanism.

**What nobody had measured is whether the two compose into a live bypass.** Both statements
are about rules. Neither says what a guest can actually reach. This does.

**The rig is two sandboxes on one network**, because that is the reachable case on this host:
the host has **no working IPv6 route to the internet** (its only v6 defaults are Tailscale
`utun` interfaces), so an external v6 destination is unreachable here regardless of policy and
measuring against one would be a free negative. Apple's `container` assigns every guest on a
network a **ULA** from a shared `/64` — `container inspect` reports it and the guest configures
it via SLAAC a few seconds after start — so sandbox-to-sandbox over v6 is the path that exists.
That is also the path the product has today, because `runtime/apple/apple.go` never passes
`--network` and every sandbox shares the built-in network.

**The control is the same probe over IPv4.** A v6 success means nothing unless the allowlist is
shown working at all in the same second, on the same pair of guests, against the same listener.

Instrument boundary: nothing timed, no host privilege, no pf rule loaded at any point. The
allowlist is installed with the same `iptables` shape `firewall.py` installs.

Run it as: `python3 ipv6_sidestep.py`
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
A, B = "yb6a", "yb6b"
PORT = 8080

STATE: dict[str, object] = {}


def _q(argv: list[str], timeout: int = 180) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def guest(box: str, script: str) -> str:
    return _q(["container", "exec", box, "sh", "-c", script]).stdout.strip()


def addrs(box: str) -> tuple[str, str]:
    n = json.loads(_q(["container", "inspect", box]).stdout)[0]["status"]["networks"][0]
    return n["ipv4Address"].split("/")[0], n.get("ipv6Address", "").split("/")[0]


def cleanup() -> None:
    for b in (A, B):
        _q(["container", "stop", b])
        _q(["container", "rm", b])


def reach(box: str, url: str) -> str:
    return guest(box, f"curl -s -o /dev/null -m 6 -w '%{{http_code}}' '{url}' 2>/dev/null; echo")


def main() -> int:
    h = Harness("IPV6", "Does the in-guest IPv4 allowlist constrain IPv6 at all?")
    try:
        cleanup()
        for b in (B, A):
            _q(["container", "run", "-d", "--name", b, "--network", "default",
                "--cap-add", "NET_ADMIN", IMAGE, "sleep", "600"])
        for b in (A, B):
            for _ in range(60):
                if guest(b, "echo ok") == "ok":
                    break
                time.sleep(1)
        h.require("both guests are up",
                  guest(A, "echo ok") == "ok" and guest(B, "echo ok") == "ok")

        # SLAAC needs a few seconds; before it completes the guest has only a link-local and
        # the v6 arm would be measuring an address that does not exist yet.
        b4 = b6 = ""
        for _ in range(30):
            b4, b6 = addrs(B)
            if b6 and b6 in guest(B, "ip -6 addr show eth0"):
                break
            time.sleep(1)
        h.require("the target guest configured its ULA", bool(b6) and b6 in
                  guest(B, "ip -6 addr show eth0"),
                  f"daemon reports {b6!r}; guest must actually hold it")
        h.measure("target guest", f"{B} v4={b4} v6={b6}")
        # A hand check run BEFORE SLAAC/RA completed saw only a link-local and no default
        # route, and concluded the guest was not v6-routed at all. It is: the vmnet gateway
        # sends a Router Advertisement and the guest installs a v6 default via it. Whether
        # that route leads anywhere is the HOST's business, and this host has no v6 internet
        # — so external v6 is unreachable here while the guest is fully configured to try.
        h.measure("the guest's own v6 default route",
                  guest(A, "ip -6 route show default | head -1") or "NONE",
                  "learned by RA from the vmnet gateway. The guest is v6-routed by default; "
                  "only the host's lack of v6 upstream stops it leaving, which is a property "
                  "of this host and not of the design")

        guest(B, f"nohup python3 -m http.server {PORT} --bind :: >/dev/null 2>&1 & sleep 1")
        time.sleep(2)

        def v6_reaches_peer() -> bool:
            code = reach(A, f"http://[{b6}]:{PORT}/")
            STATE["v6"] = code
            return code not in ("", "000")

        probe = h.probe("the guest reaches its peer over IPv6", v6_reaches_peer)

        # -- BASELINE: no allowlist installed anywhere -------------------------------
        guest(A, "iptables -F OUTPUT; iptables -P OUTPUT ACCEPT")
        h.control("with no allowlist, the peer answers over IPv4 too",
                  reach(A, f"http://{b4}:{PORT}/") not in ("", "000"),
                  "so the listener and the path both work before anything is installed")
        probe.baseline(want=True)
        h.measure("baseline v6 read", STATE.get("v6"))

        # -- SAMPLE: the shape firewall.py installs, default-deny ---------------------
        guest(A, "iptables -F OUTPUT; iptables -A OUTPUT -o lo -j ACCEPT; "
                 "iptables -P OUTPUT DROP")
        v4_after = reach(A, f"http://{b4}:{PORT}/")
        h.control("the allowlist DOES work — the same peer is refused over IPv4",
                  v4_after in ("", "000"),
                  f"v4 -> {v4_after}. Without this a v6 success proves nothing, because an "
                  "allowlist that blocks nothing would look identical")
        m = probe.sample("with an IPv4 default-deny allowlist installed")
        h.measure("v6 read under the allowlist", STATE.get("v6"))
        h.measure("ip6tables OUTPUT policy inside the guest",
                  guest(A, "ip6tables -L OUTPUT -n 2>/dev/null | head -1") or "ip6tables absent",
                  "the tool is present and its policy is ACCEPT — nothing installs a v6 rule")

        h.expect(
            "the in-guest allowlist constrains IPv6 as well as IPv4",
            m, want=False,
        )

        h.not_tried(
            "an EXTERNAL v6 destination. The GUEST is v6-routed — it installs a default via "
            "the vmnet gateway from an RA — but this HOST has no working v6 route to the "
            "internet, its only v6 defaults being Tailscale `utun` interfaces. So an "
            "internet v6 probe is unreachable here whatever the policy, and reporting that "
            "as containment would be a free negative. **On a host with real IPv6 the same "
            "unfiltered path reaches the internet with no guest-side configuration at all**, "
            "and that is the case this run cannot exercise",
            "host pf. This measures the IN-GUEST layer only. `pf-v6-hole.txt` already "
            "measured the same omission host-side and established that pf CAN enforce v6, so "
            "neither layer currently closes it and neither rescues the other",
            "whether `--network-isolated` behaves differently from this hand-installed "
            "shape. The rules here are the same `iptables` form `firewall.py` installs, but "
            "the product path also sets up ipset and DNS handling and was not run",
            "the guest's v6 route to the GATEWAY. Host services on the vmnet gateway may be "
            "reachable over v6 as well as v4, which is W5's exposure question in the other "
            "family and is unprobed",
            "per-sandbox networks. Both guests share the built-in network, which is what the "
            "product does today; whether the rewritten per-sandbox design puts guests in "
            "separate v6 subnets — and whether they can still route between them — is a "
            "different question",
            "IPv6 DNS. `getent ahosts` returned only A records here, so ordinary "
            "hostname traffic stayed on v4; a resolver handing back AAAA records would send "
            "normal agent traffic down the unfiltered path without the agent doing anything "
            "deliberate. That is the more dangerous version of this and it is not measured",
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
