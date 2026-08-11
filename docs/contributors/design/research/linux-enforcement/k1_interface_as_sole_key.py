#!/usr/bin/env python3
# ABOUTME: K1 ported onto research_harness_v1 — is a per-sandbox INTERFACE a usable
# ABOUTME: enforcement key on Linux, with no address anywhere in the rule? The bash
# ABOUTME: original stays alongside as the record of what was actually run in 2026-08.

"""K1, rewritten against the harness (D134).

This is one of the migrations chosen to exercise `research_harness_v1.py` against a
real experiment rather than against its own unit tests. K1 was picked because it is
the load-bearing measurement of the whole enforcement design — decision 1, "key on
the host-side interface" — and because its bash original already had the shape the
harness formalises: preconditions that abort, a discrimination control, a negative
control, and a bounding section.

**The bash original is not deleted.** It is what produced
`results/k1-interface-as-sole-key.txt`, and a rewrite is not evidence about what a
past run did. Standing procedure (D134): a script gets migrated when someone next
needs to change it, not on a schedule.

Run it as: `sudo -v && python3 k1_interface_as_sole_key.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v1 import Harness, HarnessError  # noqa: E402

NET_A = "yb_k1_neta"
NET_B = "yb_k1_netb"
BOX_A = "yb_k1_a"
BOX_B = "yb_k1_b"
TABLE = "yb_k1"
RECYCLE_NETS = [f"yb_k1_r{i}" for i in range(1, 5)]
DENIED = "1.1.1.1"


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    """Best-effort teardown: every one of these fails when the object is absent."""
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["sudo", "nft", "delete", "table", "inet", TABLE])
    _quiet(["docker", "rm", "-f", BOX_A, BOX_B])
    _quiet(["docker", "network", "rm", NET_A, NET_B, *RECYCLE_NETS])


def bridge_of(h: Harness, network: str) -> str:
    net_id = h.run(["docker", "network", "inspect", network, "--format", "{{.Id}}"]).stdout.strip()
    return "br-" + net_id[:12]


def reaches(h: Harness, box: str, host: str, source: str | None = None) -> bool:
    """One probe. TCP for test and control alike — three runs in this workstream
    recorded a reassuring 'blocked' that was free because they probed with ICMP."""
    argv = ["docker", "exec", box, "curl", "-s", "-m", "5", "-o", "/dev/null"]
    if source:
        argv += ["--interface", source]
    return h.run([*argv, f"http://{host}/"], check=False).returncode == 0


def sandbox_identifying_address_rules(h: Harness) -> int:
    """Rules that identify the SANDBOX by address. K1's premise is that this is zero.

    Deliberately `saddr` only. A first cut counted `daddr` too and the control fired
    on `ip daddr @allowed` — which is the allowlist, i.e. the policy's content, and is
    unavoidable in any egress allowlist. The claim under test is that nothing
    identifies *the sandbox* by its address, not that no address appears anywhere.
    """
    listing = h.run(["sudo", "nft", "list", "table", "inet", TABLE]).stdout
    return len(re.findall(r"\bip6?\s+saddr\b", listing))


def main() -> int:
    h = Harness(
        "K1",
        "Is a per-sandbox interface a usable key on Linux, with no address in the rule?",
    )
    allowed = subprocess.run(
        ["getent", "ahostsv4", "example.com"], capture_output=True, text=True, check=False
    ).stdout.split()
    h.require("example.com resolves to an IPv4 address", bool(allowed))
    allow_ip = allowed[0]

    cleanup()
    try:
        # -- part 1: one network per sandbox --------------------------------
        h.run(["docker", "network", "create", "--subnet", "10.201.0.0/24", NET_A])
        h.run(["docker", "network", "create", "--subnet", "10.202.0.0/24", NET_B])
        bridge_a, bridge_b = bridge_of(h, NET_A), bridge_of(h, NET_B)
        for box, net in ((BOX_A, NET_A), (BOX_B, NET_B)):
            h.run(
                ["docker", "run", "-d", "--name", box, "--cap-add", "NET_ADMIN",
                 "--network", net, "alpine", "sleep", "500"]
            )
        # Install curl BEFORE any policy exists. Doing it after cost three runs in
        # this workstream: `apk add` inside a container whose egress is already
        # denied hangs, and the hang reads as a blocked probe.
        for box in (BOX_A, BOX_B):
            h.run(["docker", "exec", box, "apk", "add", "--no-cache", "curl"])
            h.require(
                f"curl present in {box}",
                h.run(["docker", "exec", box, "sh", "-c", "command -v curl"], check=False).returncode
                == 0,
            )
        h.require(
            f"bridge {bridge_a} exists on the host",
            _quiet(["ip", "-br", "link", "show", bridge_a]).returncode == 0,
            "the key is untestable if the interface is not there",
        )

        h.control("A reaches the allowlisted host before any policy", reaches(h, BOX_A, allow_ip))
        h.control("A reaches the denied host before any policy", reaches(h, BOX_A, DENIED))
        h.control("B reaches the denied host before any policy", reaches(h, BOX_B, DENIED))

        h.run(
            ["sudo", "nft", "-f", "-"],
            stdin=f"""
table inet {TABLE} {{
    set allowed {{ type ipv4_addr; elements = {{ {allow_ip} }} }}
    chain c_forward {{
        type filter hook forward priority -10; policy accept;
        iifname "{bridge_a}" ct state established,related accept
        iifname "{bridge_a}" udp dport 53 accept
        iifname "{bridge_a}" ip daddr @allowed counter accept comment "A-allowlisted"
        iifname "{bridge_a}" counter drop comment "A-denied"
    }}
}}
""",
        )
        h.control(
            "no rule identifies the sandbox by address",
            sandbox_identifying_address_rules(h) == 0,
            "otherwise the run measures address keying wearing an interface's name",
        )

        a_allow = h.measure("A -> allowlisted, under policy", reaches(h, BOX_A, allow_ip))
        a_deny = h.measure("A -> denied, under policy", reaches(h, BOX_A, DENIED))
        h.control(
            "B is untouched by A's policy",
            reaches(h, BOX_B, DENIED),
            "the discrimination control: if B is also blocked the key is not per-sandbox",
        )
        h.expect("an interface-keyed allow still reaches its destination", a_allow, want=True)
        h.expect("an interface-keyed deny blocks its destination", a_deny, want=False)

        # -- part 2: does the bridge name recycle? --------------------------
        names = []
        for i, net in enumerate(RECYCLE_NETS, start=1):
            h.run(["docker", "network", "create", "--subnet", f"10.21{i}.0.0/24", net])
            names.append(bridge_of(h, net))
            h.run(["docker", "network", "rm", net])
        distinct = h.measure(
            "distinct bridge names over sequential create/destroy",
            len(set(names)) == len(names),
            f"{len(set(names))} distinct out of {len(names)}: {' '.join(names)}",
        )
        h.expect("docker bridge names do not recycle across sequential cycles", distinct)

        # -- part 3: can the guest escape the key? --------------------------
        h.run(
            ["docker", "exec", "--user", "root", BOX_A,
             "ip", "addr", "add", "10.201.0.99/24", "dev", "eth0"],
            check=False,
        )
        h.control(
            "A actually holds the spoofed address",
            "10.201.0.99"
            in h.run(["docker", "exec", BOX_A, "ip", "-4", "addr", "show", "eth0"]).stdout,
            "without this the next measurement is free",
        )
        spoofed = h.measure(
            "A -> denied, sourced from an address the policy never names",
            reaches(h, BOX_A, DENIED, source="10.201.0.99"),
        )
        h.expect("adding an address does not defeat an interface-keyed rule", spoofed, want=False)

        h.run(
            ["docker", "exec", "--user", "root", BOX_A, "sh", "-c",
             "ip link set dev eth0 down; ip link set dev eth0 address 02:11:22:33:44:55; "
             "ip link set dev eth0 up"],
            check=False,
        )
        mac = h.run(["docker", "exec", BOX_A, "cat", "/sys/class/net/eth0/address"]).stdout.strip()
        h.control("A's MAC actually changed", mac == "02:11:22:33:44:55", f"MAC is now {mac}")
        remac = h.measure("A -> denied, after changing its MAC", reaches(h, BOX_A, DENIED))
        h.expect("changing the MAC does not defeat an interface-keyed rule", remac, want=False)

        h.measure(
            "host-side devices visible to A",
            h.run(["docker", "exec", BOX_A, "ls", "/sys/class/net"]).stdout.split(),
            "it cannot name the bridge or the veth peer, so it cannot move off the key",
        )

        h.not_tried(
            "veth-level keying on a SHARED bridge (physdev / nft bridge family) — that is k2",
            "ct mark / meta mark at claim time: circular unless the mark is itself applied by "
            "interface or cgroup, so it is downstream of this result",
            "eBPF cgroup_skb/egress (systemd's IPAddressAllow mechanism) — different toolchain, "
            "and it cannot cover the VM backends",
            "whether bridge names collide across a docker daemon restart, or across hosts",
            "concurrent create/destroy: part 2 is sequential, which bounds rather than proves",
            "podman and containerd — docker only. containerd shares one CNI bridge, so a "
            "per-sandbox bridge key does not exist there as configured",
            "any claim about macOS: vmnet recycles bridge indices and this run says nothing "
            "about it",
            "IPv6 and UDP throughout, other than the DNS accept the policy needs to function",
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
