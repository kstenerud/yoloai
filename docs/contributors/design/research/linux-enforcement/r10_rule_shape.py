#!/usr/bin/env python3
# ABOUTME: R10 — two Linux rule-shape questions the brief needs settled: does `reject`
# ABOUTME: block as well as `drop` while failing fast, and can a netdev ingress hook on
# ABOUTME: the sandbox's own veth key per-sandbox with no br_netfilter dependency?

"""R10: the shape of the Linux ruleset.

**Part A — `reject` instead of `drop`.** The plan now recommends this, on the strength
of a macOS measurement (`block return` answers instantly, and *"agents' blocked
connections stop hanging too"*) plus Calico injecting RSTs for the same reason. Both
are evidence *about other systems*. The Linux half was never run, and a recommendation
in a build brief should not rest on a platform read-across — that is how both of this
plan's rule-shape errors happened. Two things must hold together: it still blocks, and
it fails fast.

**Part B — can we drop the `br_netfilter` dependency entirely?** R8 measured that the
`physdev` key is silently inert without it, and the plan calls the module host-wide,
unowned, enabled by docker only for `icc=false`, and mentioned by neither CNI nor
netavark. That is a real liability for a shipped feature: anything on the machine can
unload it and every veth-keyed sandbox goes quietly unenforced.

A netdev ingress chain attached to the sandbox's own veth might avoid it altogether.
The key stops being a *match* and becomes the *attachment* — a chain bound to one
device is per-sandbox by construction, and it runs before bridging or routing decide
anything. If it works this is strictly better than physdev, and it is the one idea R8
flagged and did not try.

Run it as: `sudo -v && python3 r10_rule_shape.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v1 import Harness, HarnessError  # noqa: E402

NET = "yb_r10_net"
BOX = "yb_r10_box"
BOX_B = "yb_r10_box_b"
TABLE = "yb_r10"
NETDEV_TABLE = "yb_r10_nd"
SUBNET = "10.205.0.0/24"
DENIED = "1.1.1.1"
PROBE_TIMEOUT = 5


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["sudo", "nft", "delete", "table", "inet", TABLE])
    _quiet(["sudo", "nft", "delete", "table", "netdev", NETDEV_TABLE])
    _quiet(["docker", "rm", "-f", BOX, BOX_B])
    _quiet(["docker", "network", "rm", NET])


def bridge_of(h: Harness) -> str:
    net_id = h.run(["docker", "network", "inspect", NET, "--format", "{{.Id}}"]).stdout.strip()
    return "br-" + net_id[:12]


def host_veth_of(h: Harness, box: str) -> str:
    iflink = h.run(["docker", "exec", box, "cat", "/sys/class/net/eth0/iflink"]).stdout.strip()
    for line in h.run(["ip", "-o", "link"]).stdout.splitlines():
        if line.startswith(iflink + ":"):
            return line.split(":")[1].strip().split("@")[0]
    raise HarnessError(f"no host interface with ifindex {iflink} for {box}")


def probe(h: Harness, box: str, host: str) -> tuple[bool, float]:
    """Returns (reachable, elapsed seconds). Elapsed is the whole point of part A."""
    t0 = time.monotonic()
    rc = h.run(
        ["docker", "exec", box, "curl", "-s", "-m", str(PROBE_TIMEOUT), "-o", "/dev/null",
         f"http://{host}/"],
        check=False,
    ).returncode
    return rc == 0, time.monotonic() - t0


def counter(h: Harness, table: str, family: str, comment: str) -> int:
    listing = h.run(["sudo", "nft", "list", "table", family, table]).stdout
    for line in listing.splitlines():
        if f'comment "{comment}"' in line:
            m = re.search(r"packets (\d+)", line)
            if m:
                return int(m.group(1))
    return 0


def load_forward(h: Harness, bridge: str, verdict: str) -> None:
    _quiet(["sudo", "nft", "delete", "table", "inet", TABLE])
    h.run(
        ["sudo", "nft", "-f", "-"],
        stdin=f"""
table inet {TABLE} {{
    chain c_forward {{
        type filter hook forward priority -10; policy accept;
        iifname "{bridge}" ip daddr {DENIED} counter {verdict} comment "deny"
    }}
}}
""",
    )


def main() -> int:
    h = Harness("R10", "Linux rule shape: reject vs drop, and a br_netfilter-free key")
    try:
        cleanup()
        h.require(
            "br_netfilter is NOT loaded",
            "br_netfilter" not in _quiet(["lsmod"]).stdout,
            "part B's whole claim is that it does not need the module",
        )
        h.run(["docker", "network", "create", "--subnet", SUBNET, NET])
        bridge = bridge_of(h)
        for box in (BOX, BOX_B):
            h.run(["docker", "run", "-d", "--name", box, "--network", NET,
                   "alpine", "sleep", "600"])
            h.run(["docker", "exec", box, "apk", "add", "--no-cache", "curl"])
        veth = host_veth_of(h, BOX)

        base_reach, base_time = probe(h, BOX, DENIED)
        h.control("the denied host is reachable with no policy", base_reach,
                  f"in {base_time:.2f}s — the timing baseline for part A")

        # -- PART A: drop vs reject ------------------------------------------
        load_forward(h, bridge, "drop")
        drop_reach, drop_time = probe(h, BOX, DENIED)
        h.control("the drop rule fired", counter(h, TABLE, "inet", "deny") > 0,
                  f"{counter(h, TABLE, 'inet', 'deny')} packets")

        load_forward(h, bridge, "reject")
        rej_reach, rej_time = probe(h, BOX, DENIED)
        h.control("the reject rule fired", counter(h, TABLE, "inet", "deny") > 0,
                  f"{counter(h, TABLE, 'inet', 'deny')} packets")

        h.measure("drop: blocked", not drop_reach, f"took {drop_time:.2f}s")
        blocked_by_reject = h.measure("reject: blocked", not rej_reach,
                                      f"took {rej_time:.2f}s")
        # The comparison that matters: same outcome, different wait. A reject that
        # blocked but took the same timeout would be no improvement at all.
        fast = h.measure(
            "reject fails fast where drop waits out the timeout",
            rej_time < drop_time / 2,
            f"reject {rej_time:.2f}s vs drop {drop_time:.2f}s (curl -m {PROBE_TIMEOUT})",
        )
        h.expect("reject blocks as well as drop does", blocked_by_reject, want=True)
        h.expect("and it turns a black hole into an immediate error", fast, want=True)

        _quiet(["sudo", "nft", "delete", "table", "inet", TABLE])

        # -- PART B: netdev ingress on the sandbox's own veth -----------------
        h.control(
            "with the forward table gone, the denied host is reachable again",
            probe(h, BOX, DENIED)[0],
            "part B must not inherit part A's enforcement",
        )
        # A chain bound to ONE device: the key is the attachment, not a match.
        loaded_netdev = subprocess.run(  # noqa: S603
            ["sudo", "nft", "-f", "-"],
            input=f"""
table netdev {NETDEV_TABLE} {{
    chain c_ingress {{
        type filter hook ingress device "{veth}" priority 0; policy accept;
        ip daddr {DENIED} counter drop comment "nd-deny"
    }}
}}
""",
            capture_output=True,
            text=True,
            check=False,
        )
        h.require(
            "the netdev ingress chain loads at all",
            loaded_netdev.returncode == 0,
            loaded_netdev.stderr.strip() or "loaded",
        )
        nd_reach, _ = probe(h, BOX, DENIED)
        nd_blocked = h.measure("netdev ingress on the veth blocks, with no br_netfilter",
                               not nd_reach)
        h.control(
            "the netdev rule fired",
            counter(h, NETDEV_TABLE, "netdev", "nd-deny") > 0,
            f"{counter(h, NETDEV_TABLE, 'netdev', 'nd-deny')} packets — a blocked probe "
            "against a zero counter would be a free negative",
        )
        h.control(
            "the SECOND sandbox is untouched",
            probe(h, BOX_B, DENIED)[0],
            "the chain is bound to A's veth only, so this is the discrimination control",
        )
        h.expect("a netdev ingress hook keys per-sandbox without br_netfilter",
                 nd_blocked, want=True)

        h.not_tried(
            "whether a netdev chain SURVIVES the veth going away and coming back. A chain "
            "bound to a device is the lifecycle question in a new form and it is untouched",
            "whether the guest can influence it. The guest cannot name the host-side veth "
            "(k1), but a netdev chain is a different object from a filter rule and nothing "
            "here probes it",
            "the reply direction, and egress hook (`hook egress`) as the counterpart",
            "sets, allowlists at size, and any of the throughput questions p2 asked of the "
            "forward hook. This is a single-address deny",
            "reject's ICMP variants and what a rejected UDP flow does — part A is TCP only",
            "whether `reject` is available in a netdev chain at all; part B uses drop",
            "rootless podman, containerd/CNI, and every VM backend",
            "kernel-version sensitivity. netdev ingress needs >= 4.2 and this host runs one "
            "kernel; nothing here says what a supported older target does",
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
