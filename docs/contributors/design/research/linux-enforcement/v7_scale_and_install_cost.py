#!/usr/bin/env python3
# ABOUTME: V7 — what N per-sandbox netdev chains cost, and how long a 10000-element
# ABOUTME: allowlist takes to load. Both land on the sandbox start path, and r14 priced
# ABOUTME: neither: it measured steady-state transfer on one device.

"""V7: the acquisition-path cost, which nothing has measured.

R14 priced a netdev chain's *steady state* — 100 MB transfers at allowlist sizes 1, 1000
and 10000 — and found run-to-run variance larger than any difference between
configurations, reporting honestly that *"this proxy cannot separate them, not that they
are equal."* Two costs it names in its own bounds are unmeasured and both land on **every
sandbox start**, where they are paid serially and a user is waiting:

* **"how long loading a 10000-element set TAKES. Only steady-state transfer is timed, and
  set-load latency lands on every acquisition."**
* **"concurrency. One client, one flow, one device — and the hook is per-device, so N
  sandboxes mean N chains, which nothing here prices."**

The macOS half has the comparison that makes these matter: `pf-acquire-cost.txt` found slot
acquisition is **14–46% of a `container run -d`** depending on pool size, and 85% of each
call is `sudo` rather than `pfctl`. Linux has no sudo in the path (R15), so the question is
purely what nftables costs — and if a 10000-element set takes a second, that is a second on
every start of a sandbox with a large allowlist.

**Threshold with a reason rather than an arbitrary one.** `pf-lifecycle.txt` measured a
sandbox taking **816 ms** to start on this class of host. An install step that stays well
inside that is absorbed; one that approaches it is a visible regression. So the claim is
that a full install with a 10000-element set completes in under 200 ms — a quarter of the
observed start — and the number is reported either way.

Devices are hand-built veth pairs rather than containers, because the question is what
*nftables* costs. Creating 32 containers would measure docker.

Run it as: `sudo -v && python3 v7_scale_and_install_cost.py`
"""

from __future__ import annotations

import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

NS = "yb_v7_ns"
PREFIX = "yb_v7_v"
TABLE_PREFIX = "yb_v7_t"
PROBE_DEV = "yb_v7_probe"
PROBE_PEER = "yb_v7_probep"
PROBE_TABLE = "yb_v7_probet"
HOST_IP = "10.222.0.1"
NS_IP = "10.222.0.2"
FLEET = 32


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    out = _quiet(["sudo", "nft", "list", "tables"]).stdout
    for line in out.splitlines():
        if TABLE_PREFIX in line or PROBE_TABLE in line:
            parts = line.split()
            if len(parts) >= 3:
                _quiet(["sudo", "nft", "delete", "table", parts[1], parts[2]])
    for i in range(FLEET + 2):
        _quiet(["sudo", "ip", "link", "del", f"{PREFIX}{i}"])
    _quiet(["sudo", "ip", "link", "del", PROBE_DEV])
    _quiet(["sudo", "ip", "netns", "del", NS])


def elements(n: int) -> str:
    """n distinct addresses, which is what a resolved domain allowlist looks like."""
    addrs = [f"{10 + (i >> 24) % 200}.{(i >> 16) % 256}.{(i >> 8) % 256}.{i % 256}"
             for i in range(n)]
    return ", ".join(addrs)


def install(h: Harness, table: str, dev: str, size: int) -> float:
    """Install one sandbox's complete policy. Returns seconds for the nft call alone."""
    ruleset = f"""
table netdev {table} {{
    set allowed {{
        type ipv4_addr
        elements = {{ {elements(size)} }}
    }}
    chain c_ingress {{
        type filter hook ingress device "{dev}" priority 0; policy accept;
        ip daddr @allowed counter accept comment "allow"
        ip daddr 0.0.0.0/0 counter reject comment "deny"
    }}
}}
"""
    start = time.monotonic()
    h.run(["sudo", "nft", "-f", "-"], stdin=ruleset)
    return time.monotonic() - start


def build_probe_link(h: Harness) -> None:
    h.run(["sudo", "ip", "link", "add", PROBE_DEV, "type", "veth", "peer", "name", PROBE_PEER])
    h.run(["sudo", "ip", "link", "set", PROBE_PEER, "netns", NS])
    h.run(["sudo", "ip", "addr", "add", f"{HOST_IP}/24", "dev", PROBE_DEV])
    h.run(["sudo", "ip", "link", "set", PROBE_DEV, "up"])
    h.run(["sudo", "ip", "netns", "exec", NS, "ip", "addr", "add", f"{NS_IP}/24", "dev",
           PROBE_PEER])
    h.run(["sudo", "ip", "netns", "exec", NS, "ip", "link", "set", PROBE_PEER, "up"])


def reaches(h: Harness) -> bool:
    return (
        h.run(["sudo", "ip", "netns", "exec", NS, "ping", "-c", "2", "-W", "2", HOST_IP],
              check=False).returncode
        == 0
    )


def table_count(h: Harness) -> int:
    return sum(1 for ln in h.run(["sudo", "nft", "list", "tables"]).stdout.splitlines()
               if TABLE_PREFIX in ln)


def main() -> int:
    h = Harness("V7", "What do N per-sandbox netdev chains cost, and how long does a "
                      "10000-element allowlist take to install?")
    try:
        cleanup()
        h.run(["sudo", "ip", "netns", "add", NS])
        build_probe_link(h)

        # -- the mechanism must work before its cost means anything ----------
        blocked = h.probe("the guest reaches the denied host", lambda: reaches(h))
        blocked.baseline(want=True, detail="no policy; the floor")
        install(h, PROBE_TABLE, PROBE_DEV, 1)
        h.expect("the policy under test actually enforces before it is priced",
                 blocked.sample("with a 1-element allowlist"), want=False)
        _quiet(["sudo", "nft", "delete", "table", "netdev", PROBE_TABLE])

        # -- set size, on an empty host --------------------------------------
        sizes = (1, 100, 1000, 10000)
        solo: dict[int, float] = {}
        for size in sizes:
            _quiet(["sudo", "nft", "delete", "table", "netdev", PROBE_TABLE])
            solo[size] = install(h, PROBE_TABLE, PROBE_DEV, size)
            h.measure(f"install with a {size}-element allowlist, no other chains",
                      round(solo[size] * 1000, 1), "ms")
        _quiet(["sudo", "nft", "delete", "table", "netdev", PROBE_TABLE])

        # -- the same install, with a fleet of other sandboxes' chains loaded --
        for i in range(FLEET):
            dev = f"{PREFIX}{i}"
            h.run(["sudo", "ip", "link", "add", dev, "type", "veth",
                   "peer", "name", f"{dev}p"])
            h.run(["sudo", "ip", "link", "set", dev, "up"])
            install(h, f"{TABLE_PREFIX}{i}", dev, 100)
        h.control(f"the fleet of {FLEET} chains really is loaded",
                  table_count(h) == FLEET, f"{table_count(h)} tables")

        fleeted: dict[int, float] = {}
        for size in sizes:
            _quiet(["sudo", "nft", "delete", "table", "netdev", PROBE_TABLE])
            fleeted[size] = install(h, PROBE_TABLE, PROBE_DEV, size)
            h.measure(f"install with a {size}-element allowlist, {FLEET} chains present",
                      round(fleeted[size] * 1000, 1), "ms")

        h.control("the probe sandbox is still enforced with the fleet loaded",
                  not reaches(h),
                  "a cost measured on a policy that stopped working is not a cost")

        worst = max(fleeted[10000], solo[10000])
        under_budget = h.measure(
            "a full install with a 10000-element allowlist stays under 200 ms",
            worst < 0.200,
            f"worst case {worst * 1000:.1f} ms, against the 816 ms sandbox start "
            "pf-lifecycle.txt measured on this class of host",
        )
        h.expect(
            "the allowlist install is absorbed by the start path rather than visible in it",
            under_budget,
            want=True,
            unbaselined="a latency measurement against a budget taken from another run; "
                        "there is no mechanism-absent state for 'how long does this take'",
        )

        ratio = (fleeted[10000] / solo[10000]) if solo[10000] > 0 else 0.0
        h.measure("install cost at 10000 elements, fleet vs empty host",
                  round(ratio, 2),
                  f"x — {FLEET} other chains present vs none. Reported as a ratio and NOT "
                  "claimed flat: this proxy has no repeat measurements and r14 established "
                  "that run-to-run variance here exceeds the effects being looked for")
        h.measure("growth from 1 to 10000 elements, on an empty host",
                  round(solo[10000] / solo[1], 2) if solo[1] > 0 else 0.0,
                  "x")

        h.not_tried(
            "REPEATED measurements. Every number here is n=1 per cell on an idle host. R14 "
            "found run-to-run variance exceeding the differences it was looking for, so "
            "treat these as an order of magnitude and not as a comparison between cells",
            "concurrent installs. The fleet is built serially; N sandboxes STARTING at once "
            "is the case a cross-sandbox lock would serialise and nothing here measures it",
            "set UPDATE cost, which is what revocation costs and is the operation that "
            "happens repeatedly rather than once",
            "memory. N chains and a 10000-element set have a kernel footprint nobody has "
            "looked at, and it is the cost that does not show up in a timer",
            "teardown, which happens on every stop and is not timed here at all",
            "the interval set type. These are /32s in a plain hash set; the product's "
            "allowlist is resolved domains, which may want intervals and price differently",
            "containers. Hand-built veths, so this is nftables' cost and not docker's",
            "steady-state throughput, which is r14's question and is not re-asked",
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
