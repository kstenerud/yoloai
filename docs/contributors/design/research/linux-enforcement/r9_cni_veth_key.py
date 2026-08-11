#!/usr/bin/env python3
# ABOUTME: R9 — does the veth key discriminate on CNI's own veths, or only on
# ABOUTME: docker's? The reach table calls containerd "mechanism measured on docker's
# ABOUTME: veths, not on CNI's", and containerd is a shipped backend.

"""R9: the reach table's containerd row, measured instead of extrapolated.

`enforcement-state-reaping.md` keys containerd on `veth via physdev` because
containerd puts every sandbox on one shared bridge, so the per-network bridge key
does not apply there. But the row says outright that the mechanism was **measured on
docker's veths, not on CNI's**, and "the mechanism should carry" is the exact form of
reasoning this workstream has had to retract five times.

CNI creates veths through a different code path from docker's, and the plan already
records one consequence: CNI generates `veth%x` from 4 random bytes and does **not**
persist the name, where docker persists its own. A naming difference is not a matching
difference, but nobody has checked.

**This uses nerdctl's CNI bridge, not yoloAI's `yoloai0`.** Same CNI bridge plugin and
same veth creation path, which is what the mechanism question is about; it is not a
test of yoloAI's own conflist. Stated again in the bounds.

Run it as: `sudo -v && python3 r9_cni_veth_key.py`
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v1 import Harness, HarnessError  # noqa: E402

BOX_A = "yb_r9_a"
BOX_B = "yb_r9_b"
CHAIN = "YB_R9"
DENIED = "1.1.1.1"


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup(loaded_br_netfilter: bool) -> None:
    _quiet(["sudo", "iptables", "-D", "FORWARD", "-j", CHAIN])
    _quiet(["sudo", "iptables", "-F", CHAIN])
    _quiet(["sudo", "iptables", "-X", CHAIN])
    for box in (BOX_A, BOX_B):
        _quiet(["sudo", "nerdctl", "rm", "-f", box])
    if loaded_br_netfilter:
        _quiet(["sudo", "sysctl", "-qw", "net.bridge.bridge-nf-call-iptables=0"])
        _quiet(["sudo", "modprobe", "-r", "br_netfilter"])


def host_veth_of(h: Harness, box: str) -> str:
    iflink = h.run(
        ["sudo", "nerdctl", "exec", box, "cat", "/sys/class/net/eth0/iflink"]
    ).stdout.strip()
    for line in h.run(["ip", "-o", "link"]).stdout.splitlines():
        if line.startswith(iflink + ":"):
            return line.split(":")[1].strip().split("@")[0]
    raise HarnessError(f"no host interface with ifindex {iflink} for {box}")


def reaches(h: Harness, box: str, host: str) -> bool:
    return (
        h.run(
            ["sudo", "nerdctl", "exec", box, "curl", "-s", "-m", "5", "-o", "/dev/null",
             f"http://{host}/"],
            check=False,
        ).returncode
        == 0
    )


def drop_counter(h: Harness, veth: str) -> int:
    out = h.run(["sudo", "iptables", "-L", CHAIN, "-v", "-n", "-x"]).stdout
    for line in out.splitlines():
        if veth in line and "DROP" in line:
            return int(line.split()[0])
    return 0


def main() -> int:
    h = Harness("R9", "Does the veth key discriminate on CNI's veths, not just docker's?")
    loaded = False
    try:
        allowed = subprocess.run(
            ["getent", "ahostsv4", "example.com"], capture_output=True, text=True, check=False
        ).stdout.split()
        h.require("example.com resolves", bool(allowed))
        allow_ip = allowed[0]

        cleanup(False)
        for box in (BOX_A, BOX_B):
            h.run(["sudo", "nerdctl", "run", "-d", "--name", box, "alpine", "sleep", "600"])
        # Before any policy exists — `apk add` under denied egress hangs, and the hang
        # reads as a blocked probe.
        for box in (BOX_A, BOX_B):
            h.run(["sudo", "nerdctl", "exec", box, "apk", "add", "--no-cache", "curl"])
            h.require(
                f"curl present in {box}",
                h.run(["sudo", "nerdctl", "exec", box, "sh", "-c", "command -v curl"],
                      check=False).returncode == 0,
            )

        veth_a, veth_b = host_veth_of(h, BOX_A), host_veth_of(h, BOX_B)
        h.require("CNI gave the two sandboxes distinct host veths", veth_a != veth_b,
                  f"A={veth_a} B={veth_b}")
        h.control(
            "both sandboxes sit on the SAME bridge, so the per-network key cannot apply",
            _quiet(["ip", "-o", "link", "show", veth_a]).stdout.split("master")[1].split()[0]
            == _quiet(["ip", "-o", "link", "show", veth_b]).stdout.split("master")[1].split()[0],
            "otherwise this measures the docker shape wearing CNI's name",
        )

        # br_netfilter is what makes physdev match at all (R8), so the key needs it here too.
        if "br_netfilter" not in _quiet(["lsmod"]).stdout:
            h.run(["sudo", "modprobe", "br_netfilter"])
            loaded = True
        h.run(["sudo", "sysctl", "-qw", "net.bridge.bridge-nf-call-iptables=1"])

        h.control("A reaches the denied host before any policy", reaches(h, BOX_A, DENIED))
        h.control("A reaches the allowlisted host before any policy", reaches(h, BOX_A, allow_ip))
        h.control("B reaches the denied host before any policy", reaches(h, BOX_B, DENIED))

        h.run(["sudo", "iptables", "-N", CHAIN])
        h.run(["sudo", "iptables", "-I", "FORWARD", "-j", CHAIN])
        h.run(["sudo", "iptables", "-A", CHAIN, "-m", "physdev", "--physdev-in", veth_a,
               "-d", DENIED, "-j", "DROP"])

        a_denied = h.measure("A -> denied, keyed on A's CNI veth", reaches(h, BOX_A, DENIED))
        a_allowed = h.measure("A -> allowlisted, under the same policy",
                              reaches(h, BOX_A, allow_ip))
        h.control(
            "B is untouched by A's rule",
            reaches(h, BOX_B, DENIED),
            "the discrimination control: if B is also blocked the key is not per-sandbox",
        )
        h.control(
            "the drop rule actually fired",
            drop_counter(h, veth_a) > 0,
            f"{drop_counter(h, veth_a)} packets — a blocked probe against a zero counter "
            "would be a free negative",
        )
        h.expect("a CNI veth is a usable per-sandbox key on a shared bridge",
                 a_denied, want=False)
        h.expect("and it does not cost the sandbox its allowlisted traffic",
                 a_allowed, want=True)

        h.not_tried(
            "yoloAI's OWN CNI conflist. This uses nerdctl's bridge — same CNI bridge plugin "
            "and veth path, which is the mechanism question, but not a test of `yoloai0` or "
            "of the plugin chain yoloAI configures (firewall, portmap, host-local IPAM)",
            "name recycling on CNI under churn. k3 measured 6 distinct names over 6 sequential "
            "cycles; this run creates two sandboxes once and says nothing about reuse",
            "Kata. containerd's VM isolation runs the guest in a VM, and whether the host-side "
            "veth is the same object in that configuration is unexamined",
            "the nft equivalent (`meta ibrname`), which might key on the bridge port without "
            "br_netfilter at all — R8 flagged it and nothing has tried it",
            "IPv6, UDP, and the reply direction",
            "concurrency: two sandboxes, created in sequence, on an idle host",
            "what happens when br_netfilter is absent — R8 measured that separately and the "
            "answer is that this whole key goes inert, silently",
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
