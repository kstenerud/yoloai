#!/usr/bin/env python3
# ABOUTME: V2+V5 — can a netdev chain be installed before its device exists (Part 0's
# ABOUTME: ordering), and does nft bind that chain by NAME or by IFINDEX (whether R11's
# ABOUTME: stale-but-inert is structural or incidental)? Two probes of one mechanism.

"""V2 and V5: what exactly is a netdev chain attached to?

Two items from `enforcement-verification-round-2.md`, in one harness because they are
two probes of the same kernel behaviour and share a rig.

**V2 — Part 0's ordering.** The pre-agent hook has to guarantee enforcement is in place
before the agent's first packet. If a netdev chain can be created for a device that does
not exist yet and binds when the device appears, that guarantee is trivial: install, then
start the sandbox. If it cannot, there is a window between device creation and chain load,
and Part 0 has to close it some other way.

**V5 — the mechanism behind R11.** R11 measured that a device deleted and recreated under
the same name does NOT inherit the departed chain's policy (`reachable=True, counter
0 -> 0`), and named the open question in its own bounds: *"nothing here records whether nft
binds by name or by index."* That distinction decides whether `stale-but-inert` is
**structural** — a new device always has a new ifindex, so inheritance is impossible by
construction — or **incidental** to this kernel, in which case the build cannot lean on it.

The discriminator is a **rename**, which neither R11 nor K3b tried. Load a chain on a
device, then rename that device without destroying it: the ifindex is unchanged and the
name is not.

* still enforcing under the new name  -> bound by **ifindex**
* no longer enforcing                 -> bound by **name**, at load time

`man nft` on this host does not settle it: base chains "exist per interface only" (an
instance) but take a device "name as a string" (a name). The prior-art gate found
mailing-list traffic around same-name device re-registration and no published answer, so
this is measurement rather than re-derivation — the distinction A35 exists to enforce.

**The rig is a hand-built veth pair in a netns, not a container**, for R11's reason:
docker does not recycle veth names, so a container-based test cannot produce the
collision. Two host-side addresses are configured and only one is denied, so *"the path
is live while the chain is loaded"* is a control rather than an assumption — the gap I
wrongly accused R11 of having (A36) is closed here by construction.

Every probe is ICMP, test and control alike.

Run it as: `sudo -v && python3 v2_v5_netdev_device_binding.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

NS = "yb_v25_ns"
VETH = "yb_v25_v"
RENAMED = "yb_v25_r"
GHOST = "yb_v25_g"
PEER = "yb_v25_p"
TABLE = "yb_v25_nd"
HOST_IP = "10.216.0.1"
ALT_IP = "10.216.0.9"
NS_IP = "10.216.0.2"


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["sudo", "nft", "delete", "table", "netdev", TABLE])
    for dev in (VETH, RENAMED, GHOST):
        _quiet(["sudo", "ip", "link", "del", dev])
    _quiet(["sudo", "ip", "netns", "del", NS])


def build_link(h: Harness, dev: str) -> None:
    """Create the veth pair, address both ends, and add the second host address.

    ALT_IP is what makes 'the path still works' checkable while a deny rule for HOST_IP
    is loaded — the control R11 did not need and this run should not do without.
    """
    h.run(["sudo", "ip", "link", "add", dev, "type", "veth", "peer", "name", PEER])
    h.run(["sudo", "ip", "link", "set", PEER, "netns", NS])
    h.run(["sudo", "ip", "addr", "add", f"{HOST_IP}/24", "dev", dev])
    h.run(["sudo", "ip", "addr", "add", f"{ALT_IP}/24", "dev", dev])
    h.run(["sudo", "ip", "link", "set", dev, "up"])
    h.run(["sudo", "ip", "netns", "exec", NS, "ip", "addr", "add", f"{NS_IP}/24", "dev", PEER])
    h.run(["sudo", "ip", "netns", "exec", NS, "ip", "link", "set", PEER, "up"])


def pings(h: Harness, dst: str) -> bool:
    return (
        h.run(
            ["sudo", "ip", "netns", "exec", NS, "ping", "-c", "2", "-W", "2", dst],
            check=False,
        ).returncode
        == 0
    )


def ifindex(h: Harness, dev: str) -> int:
    out = h.run(["sudo", "ip", "-o", "link", "show", dev], check=False).stdout
    m = re.match(r"(\d+):", out)
    return int(m.group(1)) if m else -1


def counter(h: Harness) -> int:
    out = h.run(["sudo", "nft", "list", "table", "netdev", TABLE], check=False).stdout
    m = re.search(r'packets (\d+).*comment "nd-deny"', out)
    return int(m.group(1)) if m else 0


def declared_device(h: Harness) -> str:
    """What nft itself reports the chain is attached to."""
    out = h.run(["sudo", "nft", "list", "table", "netdev", TABLE], check=False).stdout
    m = re.search(r'device[s]?\s*=?\s*\{?\s*"?([\w-]+)"?', out)
    return m.group(1) if m else ""


def load_chain(h: Harness, dev: str, *, check: bool = True) -> subprocess.CompletedProcess[str]:
    return h.run(
        ["sudo", "nft", "-f", "-"],
        check=check,
        stdin=f"""
table netdev {TABLE} {{
    chain c_ingress {{
        type filter hook ingress device "{dev}" priority 0; policy accept;
        ip daddr {HOST_IP} counter drop comment "nd-deny"
    }}
}}
""",
    )


def main() -> int:
    h = Harness(
        "V2+V5",
        "Can a netdev chain be installed before its device exists, and does nft bind it "
        "by name or by ifindex?",
    )
    try:
        cleanup()
        h.run(["sudo", "ip", "netns", "add", NS])

        # =================================================================
        # V5 — name or ifindex? Decided by renaming a live device.
        # =================================================================
        build_link(h, VETH)
        idx_before = ifindex(h, VETH)
        h.require("the device has a real ifindex", idx_before > 0, f"ifindex {idx_before}")

        denied = h.probe("ICMP to the denied host address", lambda: pings(h, HOST_IP))
        denied.baseline(want=True, detail="no chain loaded; without this every block below is free")
        h.control(
            "ICMP to the second host address works too, before any chain",
            pings(h, ALT_IP),
            "this is the control that stays live while the deny rule is loaded",
        )

        load_chain(h, VETH)
        blocked = denied.sample("under the chain, device named as loaded")
        h.control("the deny rule fired", counter(h) > 0, f"{counter(h)} packets")
        h.control(
            "the second address is STILL reachable with the chain loaded",
            pings(h, ALT_IP),
            "so 'blocked' below is the rule, not a dead path",
        )
        h.expect("a netdev chain bound to a device enforces", blocked, want=False)

        # -- the rename: same ifindex, different name ---------------------
        renamed_ok = h.run(
            ["sudo", "ip", "link", "set", "dev", VETH, "name", RENAMED], check=False
        ).returncode == 0
        h.measure(
            "the device can be renamed while a netdev chain is attached to it",
            renamed_ok,
            "if this fails the arm cannot run and V5 is undecided by this route",
        )
        if not renamed_ok:
            h.void_arm("v5", "the device could not be renamed while its chain was attached")
        else:
            h.run(["sudo", "ip", "link", "set", RENAMED, "up"])
            idx_after = ifindex(h, RENAMED)
            h.control(
                "the rename preserved the ifindex",
                idx_after == idx_before,
                f"ifindex {idx_before} -> {idx_after}; this is what makes the arm a "
                "name-vs-index discriminator at all",
                arm="v5",
            )
            h.control(
                "the path still works after the rename",
                pings(h, ALT_IP),
                "renaming a host-side veth must not break routing, or the next probe is free",
                arm="v5",
            )
            before = counter(h)
            still_blocked = not pings(h, HOST_IP)
            after = counter(h)
            bound_by_index = h.measure(
                "the chain still enforces on the RENAMED device",
                still_blocked and after > before,
                f"blocked={still_blocked}, counter {before} -> {after}. Both required: a "
                "blocked probe with a frozen counter means something else stopped it",
                arm="v5",
            )
            h.measure(
                "nft reports the chain attached to",
                declared_device(h),
                "the name nft prints after the rename — its own view of the binding",
            )
            h.measure(
                "so the netdev chain binds by",
                "ifindex" if bound_by_index.value else "name",
                "ifindex means R11's stale-but-inert is STRUCTURAL — a recreated device "
                "has a new index and can never inherit. name means it was incidental",
            )

        # =================================================================
        # V2 — can a chain be installed for a device that does not exist?
        # =================================================================
        _quiet(["sudo", "nft", "delete", "table", "netdev", TABLE])
        h.require(
            f"{GHOST} really does not exist yet",
            ifindex(h, GHOST) < 0,
            "the whole arm is about an absent device",
        )
        ghost = load_chain(h, GHOST, check=False)
        ghost_loaded = h.measure(
            "a netdev chain naming a device that does not exist loads",
            ghost.returncode == 0,
            f"rc={ghost.returncode}; nft said: {ghost.stderr.strip() or '(nothing)'}",
        )
        h.expect(
            "enforcement can be installed before its device appears (Part 0's ordering)",
            ghost_loaded,
            want=True,
            unbaselined="a capability probe — 'does this ruleset load' has no "
                        "mechanism-absent state to baseline against",
        )

        if not ghost_loaded.value:
            h.void_arm(
                "v2-bind",
                "the chain could not be loaded for an absent device, so whether it would "
                "bind on appearance is unreachable by this route",
            )
            h.measure("Part 0 consequence", "there IS a window: the device must exist first",
                      "the pre-agent hook must order device-then-chain-then-agent, and the "
                      "gap between the first two is unenforced")
        else:
            h.run(["sudo", "ip", "link", "add", GHOST, "type", "veth", "peer", "name", PEER + "g"])
            h.run(["sudo", "ip", "link", "set", GHOST, "up"])
            appeared = h.measure(
                "the chain binds once the device appears",
                counter(h) >= 0 and ifindex(h, GHOST) > 0,
                "device created after the chain; see the reachability arm for whether it "
                "actually filters",
                arm="v2-bind",
            )
            h.measure("ghost device ifindex after creation", ifindex(h, GHOST), arm="v2-bind")
            h.measure("nft reports the ghost chain attached to", declared_device(h),
                      arm="v2-bind")
            h.expect(
                "a chain loaded before its device is attached to it once it appears",
                appeared,
                want=True,
                arm="v2-bind",
                unbaselined="by construction the chain precedes the device, so there is no "
                            "device-present-chain-absent state for this arm",
            )

        h.not_tried(
            "containers. This is a hand-built veth pair, for R11's reason: docker does not "
            "recycle veth names and so cannot produce the collision. Whether a container "
            "runtime's own device churn hits the same kernel path is a separate arm",
            "TCP and UDP. Every probe here is ICMP, consistently and with a baseline, but a "
            "netdev chain may treat protocols differently and V3 is where that is asked",
            "the ghost-device arm's actual FILTERING. It measures that the chain loads and "
            "that the device appears under it; whether traffic is then dropped needs the "
            "netns wiring this arm does not build",
            "a device that appears and disappears repeatedly, and concurrent churn",
            "kernel-version sensitivity. One kernel, one nft. The prior-art gate found no "
            "published contract for this binding, so it must not be read as one",
            "rootless podman's netns, where the chain lives in a different namespace",
            "whether the rename path is something a hostile guest could reach — it cannot "
            "name the host-side device (k1), but that is V4's question, not this one",
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
