#!/usr/bin/env python3
# ABOUTME: R11 — what happens to a netdev chain when its device goes away, and does a
# ABOUTME: device returning under the same name inherit the departed one's policy? The
# ABOUTME: reaping question, in the form R10's candidate mechanism creates.

"""R11: the lifecycle of a device-bound chain.

R10 found that a netdev ingress chain attached to a sandbox's veth keys per-sandbox
with no `br_netfilter` dependency — attractive enough that it changes the design. So
its lifecycle has to be understood before the brief adopts it, and it is the reaping
question wearing new clothes.

Two outcomes, both plausible, and they lead to opposite designs:

* **The chain dies with the device.** Reaping is largely free, and the risk inverts to
  a sandbox coming back *unenforced* because nothing reinstalled its chain.
* **The chain survives as a stale object.** Then it is exactly the macOS I5 hazard and
  the `k3b` veth-reuse hazard: a device returning under the same name inherits the
  departed sandbox's policy. Rootless podman hands out `veth0` every time.

Deliberately measured on **manually created veth pairs in a netns**, not on containers.
Docker does not recycle veth names, so a container-based test cannot produce the
collision at all — it would measure the absence of a hazard rather than the mechanism.
Constructing the device by hand lets the same name be destroyed and recreated on
demand, which is the only way to ask the question directly.

Every probe is ICMP, test and control alike, with a baseline proving ICMP works on this
path before anything is loaded — three runs in this workstream recorded a reassuring
"blocked" that was free because the protocol never worked there in the first place.

Run it as: `sudo -v && python3 r11_netdev_lifecycle.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v1 import Harness, HarnessError  # noqa: E402

NS = "yb_r11_ns"
HOST_VETH = "yb_r11_v"
PEER = "yb_r11_p"
TABLE = "yb_r11_nd"
HOST_IP = "10.206.0.1"
NS_IP = "10.206.0.2"


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["sudo", "nft", "delete", "table", "netdev", TABLE])
    _quiet(["sudo", "ip", "link", "del", HOST_VETH])
    _quiet(["sudo", "ip", "netns", "del", NS])


def build_link(h: Harness) -> None:
    """Create the veth pair and address both ends. Idempotent by construction: the
    caller deletes first, so this is the 'a sandbox arrives' half of a lifecycle."""
    h.run(["sudo", "ip", "link", "add", HOST_VETH, "type", "veth", "peer", "name", PEER])
    h.run(["sudo", "ip", "link", "set", PEER, "netns", NS])
    h.run(["sudo", "ip", "addr", "add", f"{HOST_IP}/24", "dev", HOST_VETH])
    h.run(["sudo", "ip", "link", "set", HOST_VETH, "up"])
    h.run(["sudo", "ip", "netns", "exec", NS, "ip", "addr", "add", f"{NS_IP}/24", "dev", PEER])
    h.run(["sudo", "ip", "netns", "exec", NS, "ip", "link", "set", PEER, "up"])


def reaches(h: Harness) -> bool:
    """ICMP from the netns to the host end. Same protocol for every arm."""
    return (
        h.run(
            ["sudo", "ip", "netns", "exec", NS, "ping", "-c", "2", "-W", "2", HOST_IP],
            check=False,
        ).returncode
        == 0
    )


def chain_exists(h: Harness) -> bool:
    return h.run(["sudo", "nft", "list", "table", "netdev", TABLE], check=False).returncode == 0


def counter(h: Harness) -> int:
    out = h.run(["sudo", "nft", "list", "table", "netdev", TABLE], check=False).stdout
    m = re.search(r'packets (\d+).*comment "nd-deny"', out)
    return int(m.group(1)) if m else 0


def load_chain(h: Harness) -> None:
    h.run(
        ["sudo", "nft", "-f", "-"],
        stdin=f"""
table netdev {TABLE} {{
    chain c_ingress {{
        type filter hook ingress device "{HOST_VETH}" priority 0; policy accept;
        ip daddr {HOST_IP} counter drop comment "nd-deny"
    }}
}}
""",
    )


def main() -> int:
    h = Harness("R11", "Does a netdev chain outlive its device, and does a returning "
                       "device inherit the departed one's policy?")
    try:
        cleanup()
        h.run(["sudo", "ip", "netns", "add", NS])
        build_link(h)

        h.control("ICMP works on this path before any policy", reaches(h),
                  "without this every 'blocked' below is free")
        load_chain(h)
        blocked = h.measure("the netdev chain blocks while its device is present", not reaches(h))
        h.control("the netdev rule fired", counter(h) > 0, f"{counter(h)} packets")
        h.expect("a device-bound chain enforces", blocked, want=True)

        # -- the device goes away --------------------------------------------
        h.run(["sudo", "ip", "link", "del", HOST_VETH])
        survived_val = chain_exists(h)
        h.measure(
            "the chain still exists after its device is deleted",
            survived_val,
            "nft's own view; whether it is ATTACHED to anything is the next arm",
        )

        # -- a new device arrives under the same name ------------------------
        build_link(h)
        h.control(
            "the recreated device is a genuinely new one",
            _quiet(["sudo", "ip", "-o", "link", "show", HOST_VETH]).returncode == 0,
            "same name, new ifindex — the k3b hazard's precondition",
        )
        before = counter(h)
        inherited_reach = reaches(h)
        after = counter(h)
        # A plain bool, not the Measurement `measure()` hands back. The first cut used
        # the returned object in the conditional below, and a dataclass is always
        # truthy — so the derived label read "stale-and-live" directly underneath its
        # own data saying `inherits=False`. Only `expect()` renders from data; anything
        # you compute yourself can still contradict the numbers beside it.
        inherited_val = (not inherited_reach) and after > before
        h.measure(
            "a NEW device under the old name inherits the departed chain's policy",
            inherited_val,
            f"reachable={inherited_reach}, counter {before} -> {after}. Both conditions "
            "are required: a blocked probe with a frozen counter would mean something "
            "else stopped it",
        )
        # No `expect` on a preferred direction here — BOTH answers are design-relevant,
        # and asserting one would be writing the verdict before the run. The report
        # records which happened; the plan decides what to do about it.
        h.measure(
            "the chain is therefore",
            "stale-and-live" if inherited_val else ("stale-but-inert" if survived_val else "reaped"),
            "stale-and-live is the k3b/I5 hazard and needs the lifecycle rule; stale-but-inert "
            "means no inheritance but silent non-enforcement on return; reaped means reaping "
            "is free",
        )

        h.not_tried(
            "any container backend. This is a hand-built veth pair precisely because docker "
            "does not recycle names and so cannot produce the collision; whether rootless "
            "podman's `veth0` reuse hits this in practice is a separate run",
            "the ifindex. The two devices share a name and nothing here records whether nft "
            "binds by name or by index — which is the mechanism behind whatever was observed",
            "a chain attached while its device is ABSENT, i.e. whether enforcement can be "
            "installed before the sandbox exists (the pre-agent hook's question)",
            "TCP. Every probe is ICMP, consistently, with a baseline — but a netdev chain may "
            "treat protocols differently and nothing here checks",
            "the reply direction and the egress hook",
            "concurrent create/destroy, and more than one device",
            "whether a guest with CAP_NET_ADMIN inside its own netns can influence a chain "
            "bound to the host-side device",
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
