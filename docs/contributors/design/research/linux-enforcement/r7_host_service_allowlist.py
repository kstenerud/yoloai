#!/usr/bin/env python3
# ABOUTME: R7 — does a sandbox under a forward-hook deny-all still reach the host
# ABOUTME: gateway (where the credential broker listens), and can an input-hook
# ABOUTME: allowlist permit exactly the broker while denying every other host service?

"""R7, the experiment the sever design rests on.

Two questions the enforcement plan currently answers by inference rather than by
measurement, and one of them was reached by joining two established facts — which is
the exact shape of the five conclusions this workstream has already had to retract.

**Q1 — does a brokered agent survive a sever?** R6 measured that host-destined traffic
never enters the forward hook: packets addressed to an address the host holds go
prerouting -> input and are delivered locally. `applyBrokerEnv` points the agent at
`DialHost:port` — the host gateway. If those compose, dropping everything in the
forward hook severs internet egress while leaving the agent's API path intact, and
"fail closed" for a running sandbox can mean *sever* rather than *kill*. Nobody has
run it.

**Q2 — can the host-service allowlist be expressed at all?** The plan requires an
allowlist of host services rather than a blanket deny, because the sandbox must reach
the broker's injector endpoint on the gateway. Getting it backwards breaks credential
injection on every bridge backend at once, so it is worth measuring before it is
built rather than after.

Run it as: `sudo -v && python3 r7_host_service_allowlist.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v1 import Harness, HarnessError  # noqa: E402

NET = "yb_r7_net"
BOX = "yb_r7_box"
TABLE = "yb_r7"
SUBNET = "10.203.0.0/24"
GATEWAY = "10.203.0.1"
BROKER_PORT = 18771  # stands in for the credential injector
OTHER_PORT = 18772  # stands in for any other host service (sshd, a dev server)
EXTERNAL = "1.1.1.1"


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup(listeners: list[subprocess.Popen[bytes]]) -> None:
    for p in listeners:
        p.terminate()
    _quiet(["sudo", "nft", "delete", "table", "inet", TABLE])
    _quiet(["docker", "rm", "-f", BOX])
    _quiet(["docker", "network", "rm", NET])


def bridge_of(h: Harness) -> str:
    net_id = h.run(["docker", "network", "inspect", NET, "--format", "{{.Id}}"]).stdout.strip()
    return "br-" + net_id[:12]


def reaches(h: Harness, host: str, port: int) -> bool:
    """One TCP probe from inside the sandbox. Test and control travel the same path."""
    return (
        h.run(
            ["docker", "exec", BOX, "curl", "-s", "-m", "4", "-o", "/dev/null",
             f"http://{host}:{port}/"],
            check=False,
        ).returncode
        == 0
    )


def counter(h: Harness, comment: str) -> int:
    """Packets matched by the rule carrying `comment`.

    Selected by comment rather than by index: adding a rule shifts every index below
    it, and an index-based reader goes on quoting the wrong rule's counter silently.
    Borrowed from the macOS pass's `rulestat`, which learned it the hard way.
    """
    listing = h.run(["sudo", "nft", "list", "table", "inet", TABLE]).stdout
    for line in listing.splitlines():
        if f'comment "{comment}"' in line:
            m = re.search(r"packets (\d+)", line)
            if m:
                return int(m.group(1))
    return 0


def main() -> int:
    h = Harness(
        "R7",
        "Does a forward-hook deny-all leave the host gateway reachable, and can an "
        "input-hook allowlist permit only the broker?",
    )
    listeners: list[subprocess.Popen[bytes]] = []
    try:
        h.run(["docker", "network", "create", "--subnet", SUBNET, NET])
        bridge = bridge_of(h)
        h.require(
            f"the bridge {bridge} carries {GATEWAY}",
            GATEWAY in _quiet(["ip", "-4", "addr", "show", bridge]).stdout,
        )

        # Two host services on the gateway address: one standing in for the broker's
        # injector, one for everything else the host happens to run. Started AFTER the
        # bridge exists, because the gateway address does not exist before it.
        for port in (BROKER_PORT, OTHER_PORT):
            listeners.append(
                subprocess.Popen(  # noqa: S603
                    ["python3", "-m", "http.server", str(port), "--bind", GATEWAY],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                )
            )
        time.sleep(1.5)
        for port in (BROKER_PORT, OTHER_PORT):
            h.require(
                f"host service on {port} answers from the host itself",
                _quiet(["curl", "-s", "-m", "3", "-o", "/dev/null",
                        f"http://{GATEWAY}:{port}/"]).returncode == 0,
                "without this every 'blocked' below is free",
            )

        h.run(["docker", "run", "-d", "--name", BOX, "--network", NET,
               "alpine", "sleep", "600"])
        # Before any policy: `apk add` under a denied egress hangs, and the hang reads
        # as a blocked probe. Three runs in this workstream lost to exactly that.
        h.run(["docker", "exec", BOX, "apk", "add", "--no-cache", "curl"])
        h.require(
            "curl present in the sandbox",
            h.run(["docker", "exec", BOX, "sh", "-c", "command -v curl"], check=False).returncode
            == 0,
        )

        # -- baseline: everything reachable, or nothing below discriminates ----
        h.control("sandbox reaches the broker port with no policy",
                  reaches(h, GATEWAY, BROKER_PORT))
        h.control("sandbox reaches the other host service with no policy",
                  reaches(h, GATEWAY, OTHER_PORT))
        h.control("sandbox reaches an external host with no policy",
                  reaches(h, EXTERNAL, 80))

        # -- ARM B: forward-hook deny-all, nothing in the input hook -----------
        h.run(
            ["sudo", "nft", "-f", "-"],
            stdin=f"""
table inet {TABLE} {{
    chain c_forward {{
        type filter hook forward priority -10; policy accept;
        iifname "{bridge}" counter drop comment "fwd-deny"
    }}
}}
""",
        )
        b_broker = h.measure("ARM B: reaches the broker under forward deny-all",
                             reaches(h, GATEWAY, BROKER_PORT))
        b_external = h.measure("ARM B: reaches an external host under forward deny-all",
                               reaches(h, EXTERNAL, 80))
        h.control(
            "ARM B: the forward deny rule actually fired",
            counter(h, "fwd-deny") > 0,
            f"drop counter {counter(h, 'fwd-deny')} — a zero counter with a blocked probe "
            "would be a free negative",
        )
        h.expect("severing the forward hook does NOT cut the path to the host gateway",
                 b_broker, want=True)
        h.expect("severing the forward hook does cut external egress", b_external, want=False)

        # -- ARM C: + an input-hook allowlist for the broker only --------------
        h.run(
            ["sudo", "nft", "-f", "-"],
            stdin=f"""
table inet {TABLE} {{
    chain c_input {{
        type filter hook input priority 0; policy accept;
        iifname "{bridge}" tcp dport {BROKER_PORT} counter accept comment "in-broker"
        iifname "{bridge}" counter drop comment "in-deny"
    }}
}}
""",
        )
        c_broker = h.measure("ARM C: reaches the broker under the host-service allowlist",
                             reaches(h, GATEWAY, BROKER_PORT))
        c_other = h.measure("ARM C: reaches the other host service under the allowlist",
                            reaches(h, GATEWAY, OTHER_PORT))
        h.control(
            "ARM C: the input deny rule actually fired",
            counter(h, "in-deny") > 0,
            f"drop counter {counter(h, 'in-deny')}",
        )
        h.control(
            "ARM C: the broker accept rule actually matched",
            counter(h, "in-broker") > 0,
            f"accept counter {counter(h, 'in-broker')} — proves the reach was decided by "
            "our rule and not by the absence of one",
        )
        h.expect("an input-hook allowlist keeps the broker reachable", c_broker, want=True)
        h.expect("an input-hook allowlist denies every other host service", c_other, want=False)

        h.not_tried(
            "UDP, including DNS. Docker's embedded resolver lives at 127.0.0.11 inside the "
            "sandbox's own netns, so it is not host-destined and this run says nothing about "
            "a sandbox configured to use the host as its resolver",
            "the real credential broker. Both host services here are `python3 -m http.server`; "
            "the injector's actual bind address and reachability (runtime.InjectorReach, "
            "DialHost) are not exercised",
            "rootless podman and containerd. docker only — rootless podman's netns changes "
            "the whole question and containerd shares one bridge",
            "macOS, where the S3 shape blocks in BOTH directions on the bridge and the gateway "
            "is therefore inside the enforced surface. This result must NOT be read across",
            "IPv6",
            "whether the host's REPLY path is affected: replies leave via output/postrouting, "
            "not forward, and that was assumed rather than measured",
            "a hostile guest. Every probe here is a cooperative curl; nothing tests an agent "
            "trying to defeat the input-hook rules",
            "concurrency, and any second sandbox on the same bridge",
        )
        return 0 if h.report() else 1
    finally:
        cleanup(listeners)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
