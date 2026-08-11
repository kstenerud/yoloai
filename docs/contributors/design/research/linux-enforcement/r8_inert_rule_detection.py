#!/usr/bin/env python3
# ABOUTME: R8 — can a host-side counter comparison tell "our rules are in the packet
# ABOUTME: path" from "our rules are loaded and inert", with no probe and no guest
# ABOUTME: participation? And what does br_netfilter actually gate?

"""R8: the detector DF192 proposes, measured rather than asserted.

DF192 argues that a security control's detector must sit on the same side of the
trust boundary as the control, and claims Linux already can: nftables counters are
kernel state a guest cannot write, so comparing a rule's counter against the veth's
host-side `rx_packets` separates *in the path* from *inert*. **I wrote that into the
plan during a design discussion and never ran it.** This runs it.

It needs a real inert case rather than a contrived one, and the corpus supplies it.
Two keys behave differently without `br_netfilter`:

* `iifname <bridge>` in the forward hook — measured working in `k1`, on this host,
  today, with `br_netfilter` **not loaded**.
* `physdev --physdev-in <veth>` — measured working in `k2` **with it loaded**, and
  the plan flags the module as host-wide, unowned, and enabled by docker only for
  `icc=false`.

So loading both keys at once, against the same traffic, gives a working rule and an
inert rule under identical conditions — which is what makes the counter comparison
interpretable. Both rules are non-disruptive (a counting accept and a counting
RETURN); this run measures detection, not enforcement.

It therefore also answers the plan's outstanding `br_netfilter` preflight question:
what exactly stops working when it is absent.

Run it as: `sudo -v && python3 r8_inert_rule_detection.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v1 import Harness, HarnessError  # noqa: E402

NET = "yb_r8_net"
BOX = "yb_r8_box"
TABLE = "yb_r8"
CHAIN = "YB_R8"
SUBNET = "10.204.0.0/24"


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup(loaded_br_netfilter: bool) -> None:
    _quiet(["sudo", "nft", "delete", "table", "inet", TABLE])
    _quiet(["sudo", "iptables", "-D", "FORWARD", "-j", CHAIN])
    _quiet(["sudo", "iptables", "-F", CHAIN])
    _quiet(["sudo", "iptables", "-X", CHAIN])
    _quiet(["docker", "rm", "-f", BOX])
    _quiet(["docker", "network", "rm", NET])
    if loaded_br_netfilter:
        _quiet(["sudo", "sysctl", "-qw", "net.bridge.bridge-nf-call-iptables=0"])
        _quiet(["sudo", "modprobe", "-r", "br_netfilter"])


def host_veth_of(h: Harness) -> str:
    """The host-side half of the sandbox's veth pair.

    Found via the guest's `iflink`, which is the peer's host ifindex. Hardcoding a
    name cost this workstream a run once already — a rule was written for `podman1`
    while the network was on `podman2`, and rule 0b's counter read 0 and looked
    exactly like the fix failing.
    """
    iflink = h.run(["docker", "exec", BOX, "cat", "/sys/class/net/eth0/iflink"]).stdout.strip()
    links = h.run(["ip", "-o", "link"]).stdout
    for line in links.splitlines():
        if line.startswith(iflink + ":"):
            return line.split(":")[1].strip().split("@")[0]
    raise HarnessError(f"no host interface with ifindex {iflink}")


def bridge_of(h: Harness) -> str:
    net_id = h.run(["docker", "network", "inspect", NET, "--format", "{{.Id}}"]).stdout.strip()
    return "br-" + net_id[:12]


def veth_rx_packets(h: Harness, veth: str) -> int:
    """Packets the host received FROM the sandbox. Host-side kernel state."""
    return int(
        h.run(["cat", f"/sys/class/net/{veth}/statistics/rx_packets"]).stdout.strip() or 0
    )


def nft_counter(h: Harness, comment: str) -> int:
    listing = h.run(["sudo", "nft", "list", "table", "inet", TABLE]).stdout
    for line in listing.splitlines():
        if f'comment "{comment}"' in line:
            m = re.search(r"packets (\d+)", line)
            if m:
                return int(m.group(1))
    return 0


def iptables_counter(h: Harness, veth: str) -> int:
    out = h.run(["sudo", "iptables", "-L", CHAIN, "-v", "-n", "-x"]).stdout
    for line in out.splitlines():
        if veth in line:
            return int(line.split()[0])
    return 0


def generate_traffic(h: Harness) -> None:
    """Routed traffic out of the sandbox. Deliberately not container-to-container:
    the product's case is egress, and egress is routed rather than bridged."""
    for _ in range(3):
        h.run(
            ["docker", "exec", BOX, "curl", "-s", "-m", "4", "-o", "/dev/null",
             "http://1.1.1.1/"],
            check=False,
        )


def main() -> int:
    h = Harness(
        "R8",
        "Does a host-side counter comparison detect an inert rule, and what does "
        "br_netfilter gate?",
    )
    loaded = False
    try:
        h.require(
            "br_netfilter starts unloaded",
            "br_netfilter" not in _quiet(["lsmod"]).stdout,
            "this run's first arm depends on the module being absent",
        )
        h.run(["docker", "network", "create", "--subnet", SUBNET, NET])
        bridge = bridge_of(h)
        h.run(["docker", "run", "-d", "--name", BOX, "--network", NET,
               "alpine", "sleep", "600"])
        h.run(["docker", "exec", BOX, "apk", "add", "--no-cache", "curl"])
        veth = host_veth_of(h)

        # Two keys, same traffic, both non-disruptive. The bridge key is the working
        # control: without it, "our rule counted nothing" cannot be told from "the
        # chain was never traversed at all".
        h.run(
            ["sudo", "nft", "-f", "-"],
            stdin=f"""
table inet {TABLE} {{
    chain c_forward {{
        type filter hook forward priority -10; policy accept;
        iifname "{bridge}" counter accept comment "bridge-key"
    }}
}}
""",
        )
        h.run(["sudo", "iptables", "-N", CHAIN])
        h.run(["sudo", "iptables", "-I", "FORWARD", "-j", CHAIN])
        h.run(["sudo", "iptables", "-A", CHAIN, "-m", "physdev",
               "--physdev-in", veth, "-j", "RETURN"])

        # -- ARM 1: br_netfilter absent --------------------------------------
        rx_before = veth_rx_packets(h, veth)
        generate_traffic(h)
        rx_after = veth_rx_packets(h, veth)
        a1_bridge = nft_counter(h, "bridge-key")
        a1_physdev = iptables_counter(h, veth)

        h.control(
            "the sandbox actually transmitted",
            rx_after > rx_before,
            f"veth {veth} rx_packets {rx_before} -> {rx_after}; without this every zero "
            "counter below is free",
        )
        h.control(
            "the bridge-keyed rule counted, so the forward chain WAS traversed",
            a1_bridge > 0,
            f"{a1_bridge} packets — this is what separates 'our rule did not match' from "
            "'nothing reached the hook'",
        )
        inert = h.measure(
            "ARM 1: the physdev-keyed rule is inert without br_netfilter",
            a1_physdev == 0,
            f"physdev counter {a1_physdev} while the bridge key counted {a1_bridge}",
        )
        h.expect("a loaded, correct-looking physdev rule never fires without br_netfilter",
                 inert, want=True)

        # -- ARM 2: br_netfilter loaded --------------------------------------
        h.run(["sudo", "modprobe", "br_netfilter"])
        loaded = True
        h.run(["sudo", "sysctl", "-qw", "net.bridge.bridge-nf-call-iptables=1"])
        rx_before2 = veth_rx_packets(h, veth)
        physdev_before = iptables_counter(h, veth)
        generate_traffic(h)
        rx_after2 = veth_rx_packets(h, veth)
        a2_physdev = iptables_counter(h, veth) - physdev_before

        h.control(
            "the sandbox transmitted in arm 2 as well",
            rx_after2 > rx_before2,
            f"veth rx_packets {rx_before2} -> {rx_after2}",
        )
        revived = h.measure(
            "ARM 2: the same physdev rule counts once br_netfilter is loaded",
            a2_physdev > 0,
            f"physdev counter advanced by {a2_physdev}",
        )
        h.expect("br_netfilter is exactly what gates the physdev key", revived, want=True)

        detector = h.measure(
            "the counter comparison distinguishes the two states",
            (a1_physdev == 0) and (a2_physdev > 0) and (rx_after > rx_before),
            "inert: rule 0 while veth rx climbs; healthy: both climb. No probe, no "
            "traffic injected, no guest participation",
        )
        h.expect(
            "a host-side counter comparison detects an inert rule (DF192's Linux claim)",
            detector,
            want=True,
        )

        h.not_tried(
            "macOS. The pf equivalent reads Evaluations from `pfctl -a <anchor> -vvs rules`, "
            "which D132's grant currently refuses — that is the candidate in "
            "macos-pf-privileged-path.md and it is untested",
            "the shadowed-anchor fault this detector is meant to catch on macOS. Arm 1's inert "
            "case is a missing kernel module, not a rule being shadowed; whether the two produce "
            "the same counter signature is an assumption",
            "detection LATENCY. Counters are read after traffic here; how long an inert rule goes "
            "unnoticed depends on a polling interval nobody has chosen",
            "a sandbox that is legitimately idle. `rule 0 + veth rx 0` is indistinguishable from "
            "a healthy sandbox sending nothing, so this detector reports UNKNOWN there rather "
            "than healthy — untested, and it is the free-negative this design must not repeat",
            "a hostile guest trying to defeat the detector by shaping its own traffic, e.g. "
            "keeping rx_packets low",
            "nft's own `meta ibrname`, which may key on the bridge port without br_netfilter at "
            "all and would remove this dependency entirely. Not attempted",
            "rootless podman and containerd/CNI veths",
            "whether loading br_netfilter host-wide changed behaviour for OTHER bridges on this "
            "machine — the side effect the plan calls unowned. Not observed, only restored",
        )
        return 0 if h.report() else 1
    finally:
        cleanup(loaded)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
