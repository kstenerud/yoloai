#!/usr/bin/env python3
# ABOUTME: R14 — what a netdev allowlist costs at 1/1000/10000 entries, and whether
# ABOUTME: revoking one stops a transfer already in flight. The two arms R12 declined,
# ABOUTME: and the ones that decide whether the mechanism survives contact with real use.

"""R14: the netdev key at size, and under revocation of a live flow.

R12 established that a netdev chain expresses the policy — but with **one** allowlist
entry, against **new** connections. Two things are missing before a brief can adopt it:

**Cost at size.** The netdev ingress hook runs per packet on the device. `p2` priced
the forward hook at 1/1000/10000 and reached an honest non-result: run-to-run variance
on a single configuration exceeded every difference between configurations, so the
proxy could not separate them. That is the standard this arm has to meet or admit it
cannot; a single fast number would be worse than nothing.

**Revocation of an in-flight transfer.** `p1b` is the shape that mattered most in this
whole workstream: with the conntrack fast-path in front, a revoked transfer ran at
300 KB/s for 30 s with the deny counter at **0** — no packet ever reached the rule.
That was a filter-hook result. A netdev ingress chain sits before conntrack entirely,
so the prediction is that it revokes, and a prediction is exactly what needs measuring.

Run it as: `sudo -v && python3 r14_netdev_scale_and_revocation.py`
"""

from __future__ import annotations

import json
import re
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v1 import Harness, HarnessError  # noqa: E402

NET = "yb_r14_net"
SERVER = "yb_r14_srv"
CLIENT = "yb_r14_cli"
TABLE = "yb_r14"
SUBNET = "10.209.0.0/24"
SIZES = (1, 1000, 10000)
REPS = 3
SAMPLE_SECONDS = 5
SAMPLES = 6


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["sudo", "nft", "delete", "table", "netdev", TABLE])
    _quiet(["docker", "rm", "-f", SERVER, CLIENT])
    _quiet(["docker", "network", "rm", NET])


def host_veth_of(h: Harness, box: str) -> str:
    iflink = h.run(["docker", "exec", box, "cat", "/sys/class/net/eth0/iflink"]).stdout.strip()
    for line in h.run(["ip", "-o", "link"]).stdout.splitlines():
        if line.startswith(iflink + ":"):
            return line.split(":")[1].strip().split("@")[0]
    raise HarnessError(f"no host interface with ifindex {iflink} for {box}")


def address_of(h: Harness, box: str) -> str:
    return h.run(
        ["docker", "inspect", "-f",
         "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", box]
    ).stdout.strip()


def filler(count: int, real: str) -> str:
    """`count` set elements, of which exactly one is the destination that matters.

    The rest are addresses nothing routes to, so a first-match implementation has to
    walk past them — which is the property being priced.
    """
    made = [f"10.{100 + (i // 65536) % 100}.{(i // 256) % 256}.{i % 256}" for i in range(count - 1)]
    return ", ".join([*made, real])


def load_policy(h: Harness, veth: str, elements: str, gateway: str) -> None:
    _quiet(["sudo", "nft", "delete", "table", "netdev", TABLE])
    h.run(
        ["sudo", "nft", "-f", "-"],
        stdin=f"""
table netdev {TABLE} {{
    set allowed {{ type ipv4_addr; elements = {{ {elements} }} }}
    chain c_ingress {{
        type filter hook ingress device "{veth}" priority 0; policy accept;
        meta protocol ip ip daddr @allowed counter accept comment "allow"
        meta protocol ip ip daddr {gateway} counter accept comment "gateway"
        meta protocol ip udp dport 53 counter accept comment "dns"
        meta protocol ip counter drop comment "deny"
    }}
}}
""",
    )


def set_size(h: Harness) -> int:
    """How many elements nft actually holds. Asked because a set that silently
    truncated would make the timings below price a policy nobody loaded."""
    out = h.run(["sudo", "nft", "-j", "list", "set", "netdev", TABLE, "allowed"]).stdout
    doc = json.loads(out)
    for item in doc.get("nftables", []):
        elems = item.get("set", {}).get("elem")
        if elems is not None:
            return len(elems)
    return 0


def counter(h: Harness, comment: str) -> int:
    out = h.run(["sudo", "nft", "list", "table", "netdev", TABLE], check=False).stdout
    for line in out.splitlines():
        if f'comment "{comment}"' in line:
            m = re.search(r"packets (\d+)", line)
            if m:
                return int(m.group(1))
    return 0


def downloaded(h: Harness) -> int:
    out = h.run(
        ["docker", "exec", CLIENT, "sh", "-c", "wc -c < /tmp/dl 2>/dev/null || echo 0"],
        check=False,
    ).stdout.strip()
    return int(out or 0)


def timed_fetch(h: Harness, server_ip: str) -> float:
    """Seconds to pull the fixed-size file. The proxy for per-packet cost."""
    h.run(["docker", "exec", CLIENT, "sh", "-c", "rm -f /tmp/dl"], check=False)
    t0 = time.monotonic()
    h.run(["docker", "exec", CLIENT, "curl", "-s", "-m", "120", "-o", "/tmp/dl",
           f"http://{server_ip}/big"], check=False)
    return time.monotonic() - t0


def main() -> int:
    h = Harness("R14", "What does a netdev allowlist cost at size, and does it revoke a "
                       "transfer already in flight?")
    try:
        cleanup()
        h.require("br_netfilter is NOT loaded",
                  "br_netfilter" not in _quiet(["lsmod"]).stdout)
        h.run(["docker", "network", "create", "--subnet", SUBNET, NET])
        gateway = SUBNET.replace(".0/24", ".1")
        h.run(["docker", "run", "-d", "--name", SERVER, "--network", NET, "nginx:alpine"])
        h.run(["docker", "exec", SERVER, "sh", "-c",
               "dd if=/dev/urandom of=/usr/share/nginx/html/big bs=1M count=100 2>/dev/null"])
        h.run(["docker", "run", "-d", "--name", CLIENT, "--network", NET,
               "alpine", "sleep", "900"])
        h.run(["docker", "exec", CLIENT, "apk", "add", "--no-cache", "curl"])
        time.sleep(2)
        server_ip = address_of(h, SERVER)
        veth = host_veth_of(h, CLIENT)
        h.require("both containers have addresses", bool(server_ip) and bool(veth))

        baseline = [timed_fetch(h, server_ip) for _ in range(REPS)]
        h.control("the transfer works with no policy at all", min(baseline) > 0,
                  f"{[round(t, 2) for t in baseline]} s for 100 MB")

        # -- PART A: cost at size ---------------------------------------------
        timings: dict[int, list[float]] = {}
        for size in SIZES:
            load_policy(h, veth, filler(size, server_ip), gateway)
            loaded = set_size(h)
            h.require(
                f"the {size}-element set actually loaded",
                loaded == size,
                f"nft reports {loaded} elements — timing a set that silently truncated "
                "would price the wrong thing",
            )
            timings[size] = [timed_fetch(h, server_ip) for _ in range(REPS)]
            h.measure(f"100 MB with a {size}-element allowlist",
                      [round(t, 2) for t in timings[size]], "seconds, per repetition")

        spread_within = max(max(v) - min(v) for v in timings.values())
        spread_between = max(min(v) for v in timings.values()) - min(min(v) for v in timings.values())
        h.control(
            "the allowlist was in force during the timings",
            counter(h, "allow") > 0,
            f"allow counter {counter(h, 'allow')} — timing an unenforced path would price "
            "nothing at all",
        )
        separable = h.measure(
            "this proxy can separate the sizes",
            spread_between > spread_within,
            f"between-size spread {spread_between:.2f}s vs within-size {spread_within:.2f}s. "
            "p2 reached the opposite answer for the forward hook and reporting it as a "
            "non-result is the honest outcome",
        )

        # -- PART B: revocation of a live transfer ----------------------------
        load_policy(h, veth, filler(1, server_ip), gateway)
        h.run(["docker", "exec", CLIENT, "sh", "-c", "rm -f /tmp/dl"], check=False)
        h.run(["docker", "exec", "-d", CLIENT, "sh", "-c",
               f"curl -s --limit-rate 300k -m 120 -o /tmp/dl http://{server_ip}/big"])
        time.sleep(6)
        pre = downloaded(h)
        h.require("the transfer is running before revocation", pre > 100_000,
                  f"only {pre} bytes after 6s; a rate of zero would be free")

        deny_before = counter(h, "deny")
        h.run(["sudo", "nft", "delete", "element", "netdev", TABLE, "allowed",
               f"{{ {server_ip} }}"])
        rates, last = [], pre
        for _ in range(SAMPLES):
            time.sleep(SAMPLE_SECONDS)
            now = downloaded(h)
            rates.append((now - last) // SAMPLE_SECONDS // 1024)
            last = now
        deny_after = counter(h, "deny")

        stopped = h.measure("the in-flight transfer stops after the set delete", rates[-1] == 0,
                            f"KB/s across the window: {rates}")
        fired = h.measure("and the deny rule is what stopped it",
                          deny_after > deny_before,
                          f"deny counter {deny_before} -> {deny_after}. p1b's fast-path arm "
                          "held 300 KB/s with this counter at 0")
        h.expect("a netdev chain revokes a transfer already in flight", stopped, want=True)
        h.expect("with the counter to prove the rule decided it", fired, want=True)
        h.measure("cost verdict", "separable" if separable.value else "not separable by this proxy")

        h.not_tried(
            "set sizes above 10000, and the nft SET TYPE. This uses a plain hash set of /32s; "
            "an interval set or a different backing structure may price differently and the "
            "product's allowlist is domains resolved to addresses, which churn",
            "how long loading a 10000-element set TAKES. Only steady-state transfer is timed, "
            "and set-load latency lands on every acquisition",
            "concurrency. One client, one flow, one device — and the hook is per-device, so N "
            "sandboxes mean N chains, which nothing here prices",
            "whether the drop counter's growth rate matches the transfer's, i.e. whether the "
            "sandbox is being dropped efficiently or is retrying hard",
            "the reply direction, and what the SERVER's veth sees",
            "IPv6, UDP, and any non-HTTP protocol",
            "rootless podman and containerd, where the chain lives elsewhere",
            "a run-to-run comparison against the physdev/forward shape on the same hardware, "
            "which is what would say whether netdev costs MORE than the alternative rather "
            "than what it costs in absolute terms",
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
