#!/usr/bin/env python3
# ABOUTME: R15 — can yoloAI install host-netns enforcement WITHOUT the user elevating
# ABOUTME: it, by borrowing the container runtime's own privilege? Every other Linux
# ABOUTME: run in this corpus used `sudo nft` directly and so cannot answer it.

"""R15: where does the privilege actually come from on Linux?

The plan's tier story assumes Linux reaches host-side enforcement without the user
starting yoloAI elevated, because the container runtime is already privileged. **That
is an inference, and every Linux run behind it used `sudo nft` directly.** R13 is the
one exception and it covers rootless podman, where the user owns the namespace
outright.

It matters because it decides where D135's degradation warning fires. If docker needs
elevation too, then "run yoloAI normally and get host-side enforcement" is false on the
primary Linux backend, and the tier a default install lands on is one lower than the
brief claims.

The mechanism under test is the one `tamper-resistant-network-isolation.md` already
ships for the in-guest layer: a helper container that borrows the daemon's privilege.
Here it runs in the **host** network namespace rather than the sandbox's, which is what
host-side enforcement needs.

**This run asserts it is not root.** A test of "can we do this unelevated" that happens
to run elevated would pass for the wrong reason, and that is the single most common
shape of invalid run in this corpus.

Run it as: `python3 r15_unelevated_install.py` — deliberately NOT under sudo.
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v1 import Harness, HarnessError  # noqa: E402

NET = "yb_r15_net"
BOX_A = "yb_r15_a"
BOX_B = "yb_r15_b"
HELPER = "yb_r15_helper"
TABLE = "yb_r15"
SUBNET = "10.210.0.0/24"
DENIED = "1.1.1.1"


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    # Teardown goes through the helper too, because if it did not, cleanup would need
    # the privilege the run is trying to prove it does not need.
    _quiet(["docker", "exec", HELPER, "nft", "delete", "table", "netdev", TABLE])
    for box in (BOX_A, BOX_B, HELPER):
        _quiet(["docker", "rm", "-f", box])
    _quiet(["docker", "network", "rm", NET])


def host_veth_of(h: Harness, box: str) -> str:
    iflink = h.run(["docker", "exec", box, "cat", "/sys/class/net/eth0/iflink"]).stdout.strip()
    for line in h.run(["ip", "-o", "link"]).stdout.splitlines():
        if line.startswith(iflink + ":"):
            return line.split(":")[1].strip().split("@")[0]
    raise HarnessError(f"no host interface with ifindex {iflink} for {box}")


def reaches(h: Harness, box: str, host: str) -> bool:
    return (
        h.run(["docker", "exec", box, "curl", "-s", "-m", "5", "-o", "/dev/null",
               f"http://{host}/"], check=False).returncode
        == 0
    )


def nft_via_helper(h: Harness, args: list[str], *, stdin: str | None = None,
                   check: bool = True) -> subprocess.CompletedProcess[str]:
    argv = ["docker", "exec", "-i", HELPER, "nft", *args]
    return h.run(argv, stdin=stdin, check=check)


def counter(h: Harness) -> int:
    out = nft_via_helper(h, ["list", "table", "netdev", TABLE], check=False).stdout
    m = re.search(r'packets (\d+).*comment "nd-deny"', out)
    return int(m.group(1)) if m else 0


def start_helper(h: Harness, caps: list[str]) -> bool:
    """Start the privileged helper in the HOST netns with the given caps.

    Returns whether it came up and can see the host's interfaces — which is the real
    question, not whether `docker run` exited 0.
    """
    _quiet(["docker", "rm", "-f", HELPER])
    run = _quiet([
        "docker", "run", "-d", "--name", HELPER, "--network", "host",
        *[a for c in caps for a in ("--cap-add", c)],
        "alpine", "sleep", "600",
    ])
    if run.returncode != 0:
        return False
    if _quiet(["docker", "exec", HELPER, "apk", "add", "--no-cache", "nftables"]).returncode != 0:
        return False
    # Listing the ruleset is the cheapest thing that requires the capability to be real.
    return _quiet(["docker", "exec", HELPER, "nft", "list", "ruleset"]).returncode == 0


def main() -> int:
    h = Harness("R15", "Can host-netns enforcement be installed by borrowing the container "
                       "runtime's privilege, with yoloAI unelevated?")
    try:
        cleanup()
        h.require(
            "this process is NOT root",
            os.geteuid() != 0,
            f"euid={os.geteuid()} — running elevated would pass this test for the wrong reason",
        )
        h.require(
            "docker is reachable without elevation",
            _quiet(["docker", "info"]).returncode == 0,
            "the whole mechanism is borrowing the daemon's privilege, so this is the premise",
        )
        h.control(
            "plain nft is refused to this user, so the helper is doing real work",
            _quiet(["nft", "list", "ruleset"]).returncode != 0,
            "if an unelevated nft already worked, the helper would prove nothing",
        )

        h.run(["docker", "network", "create", "--subnet", SUBNET, NET])
        for box in (BOX_A, BOX_B):
            h.run(["docker", "run", "-d", "--name", box, "--network", NET,
                   "alpine", "sleep", "600"])
            h.run(["docker", "exec", box, "apk", "add", "--no-cache", "curl"])
        veth_a = host_veth_of(h, BOX_A)

        # Minimum privilege is a design input, not a detail: NET_ADMIN alone is a much
        # smaller thing to hand a helper than --privileged.
        net_admin_only = h.measure("NET_ADMIN alone is enough for the helper",
                                   start_helper(h, ["NET_ADMIN"]))
        if not net_admin_only.value:
            h.measure("--cap-add NET_ADMIN,SYS_ADMIN is enough",
                      start_helper(h, ["NET_ADMIN", "SYS_ADMIN"]))
        h.require("the helper came up and can read the host ruleset",
                  _quiet(["docker", "exec", HELPER, "nft", "list", "ruleset"]).returncode == 0,
                  "nothing below is measurable otherwise")

        h.control("A reaches the denied host before any policy", reaches(h, BOX_A, DENIED))
        h.control("B reaches it too", reaches(h, BOX_B, DENIED))

        nft_via_helper(
            h, ["-f", "-"],
            stdin=f"""
table netdev {TABLE} {{
    chain c_ingress {{
        type filter hook ingress device "{veth_a}" priority 0; policy accept;
        ip daddr {DENIED} counter drop comment "nd-deny"
    }}
}}
""",
        )
        blocked = h.measure("A is blocked by a chain installed without elevating yoloAI",
                            not reaches(h, BOX_A, DENIED))
        h.control("the rule fired", counter(h) > 0, f"{counter(h)} packets")
        h.control("B is untouched", reaches(h, BOX_B, DENIED),
                  "the discrimination control")
        h.expect("host-side enforcement is reachable on docker with no user elevation",
                 blocked, want=True)

        # The chain lives in the HOST netns, not the helper's — otherwise the helper
        # dying would take enforcement with it, which is a different design entirely.
        h.run(["docker", "rm", "-f", HELPER])
        h.control(
            "enforcement survives the helper being destroyed",
            not reaches(h, BOX_A, DENIED),
            "proves the chain is in the host namespace rather than the helper's own",
        )

        h.not_tried(
            "whether being in the `docker` group is acceptable as the privilege source. It is "
            "already root-equivalent on any host — this run measures that the mechanism works, "
            "not that it is a good idea, and the tier story should say which privilege it is "
            "borrowing rather than implying none is involved",
            "rootless docker, where the daemon is NOT root and this whole route disappears",
            "containerd, which already requires yoloAI to run as root for CNI, so the question "
            "does not arise there in the same form",
            "SELinux and AppArmor hosts, which may refuse `--network host` + NET_ADMIN",
            "the helper's own lifecycle: it is created and destroyed here by hand, and nothing "
            "measures what happens if it dies mid-install",
            "whether a hostile agent in ANOTHER sandbox could reach the helper or the docker "
            "socket — out of scope here, but it is the obvious next question",
            "macOS, where Docker Desktop's daemon runs in a VM and `--network host` does not "
            "mean the same thing at all",
            "IPv6, UDP, allowlist sets — all covered on the sudo path and not re-run here",
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
