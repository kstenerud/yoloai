#!/usr/bin/env python3
# ABOUTME: V4 — can a guest holding CAP_NET_ADMIN influence a netdev chain bound to its
# ABOUTME: host-side veth? D135's tier-3 row claims host-side enforcement is "measured
# ABOUTME: not defeasible", and that measurement was taken against a filter rule, not this.

"""V4: the hostile-guest arm, against the object the design actually ships.

[D135](../../../decisions/working-notes.md)'s tier table claims of host-side enforcement:
*"Measured not defeasible by a guest holding `CAP_NET_ADMIN`: it added a second address and
stayed blocked, changed its MAC and stayed blocked, and cannot name the host-side interface
at all."* Every one of those measurements is K1's, and **K1 tested an interface-keyed
`filter` rule in the forward hook.** The design has since moved to a **netdev ingress chain**
(R10), which is a different kernel object with a different lifecycle — R10's own bounds say
so: *"whether the guest can influence it. The guest cannot name the host-side veth (k1), but
a netdev chain is a different object from a filter rule and nothing here probes it."*

So this re-asks the security claim against the shipped mechanism, and adds the attacks a
netdev chain specifically invites, which a filter rule does not:

* **Interface teardown.** V5 established the chain binds by **ifindex**. A guest with
  `CAP_NET_ADMIN` can `ip link del eth0`, which destroys the whole veth pair including the
  host side. If that unbinds the chain, the guest can choose to be unenforced.
* **Carrier flap.** `ip link set eth0 down; up` does not destroy the device. The chain should
  survive by V5's finding, which makes this a check on that finding as much as on the guest.
* **Rename.** V5 showed a *host-side* rename keeps enforcement. A guest renaming its **own**
  end is a different operation and should be inert; measured rather than assumed.
* **Fragmentation.** `ip daddr` reads the IP header, which every fragment carries — but an
  allowlist that only matches first fragments would be a real evasion, and no probe in this
  corpus has ever sent one.

**The teardown arm cannot use reachability as its verdict.** After deleting `eth0` the guest
has no network, so "blocked" is free — the single most common invalid shape in this corpus.
That arm therefore measures *structure*: does the host-side device still exist, does the
chain still exist, and is it still attached. Reachability is only claimed where a positive
control exists in the same state.

Run it as: `sudo -v && python3 v4_hostile_guest_vs_netdev.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

NET = "yb_v4_net"
BOX = "yb_v4_a"
TABLE = "yb_v4_nd"
SUBNET = "10.218.0.0/24"
GATEWAY = "10.218.0.1"
ONLINK_DENIED = "10.218.0.50"
DENIED = "1.1.1.1"


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["sudo", "nft", "delete", "table", "netdev", TABLE])
    _quiet(["docker", "rm", "-f", BOX])
    for br in _quiet(["sh", "-c", "ip -o -4 addr show | awk '/10.218.0.50/ {print $2}'"]).stdout.split():
        _quiet(["sudo", "ip", "addr", "del", f"{ONLINK_DENIED}/24", "dev", br])
    _quiet(["docker", "network", "rm", NET])


def guest(h: Harness, *args: str, check: bool = False) -> subprocess.CompletedProcess[str]:
    return h.run(["docker", "exec", BOX, *args], check=check)


def host_veth_of(h: Harness) -> str:
    iflink = guest(h, "cat", "/sys/class/net/eth0/iflink", check=True).stdout.strip()
    for line in h.run(["ip", "-o", "link"]).stdout.splitlines():
        if line.startswith(iflink + ":"):
            return line.split(":")[1].strip().split("@")[0]
    raise HarnessError(f"no host interface with ifindex {iflink}")


def reaches(h: Harness, dst: str) -> bool:
    return guest(h, "curl", "-s", "-m", "5", "-o", "/dev/null", f"http://{dst}/").returncode == 0


def restore_guest_routing(h: Harness, dev: str = "eth0") -> None:
    """Re-add the default route a down/up cycle removed.

    Bringing an interface down deletes the routes through it, and bringing it back up
    restores only the connected route -- the default route docker installed is gone. That
    is incidental to every attack here: a guest doing this deliberately would restore its
    own routing, and without this the run measures a self-inflicted outage instead of the
    chain. It cost two void runs to notice.
    """
    h.run(["docker", "exec", BOX, "ip", "route", "add", "default", "via", GATEWAY,
           "dev", dev], check=False)


def host_dev_exists(h: Harness, dev: str) -> bool:
    return h.run(["ip", "-o", "link", "show", dev], check=False).returncode == 0


def chain_attached_to(h: Harness) -> str:
    out = h.run(["sudo", "nft", "list", "table", "netdev", TABLE], check=False).stdout
    m = re.search(r'device[s]?\s*=?\s*\{?\s*"?([\w-]+)"?', out)
    return m.group(1) if m else ""


def counter(h: Harness) -> int:
    out = h.run(["sudo", "nft", "list", "table", "netdev", TABLE], check=False).stdout
    m = re.search(r'packets (\d+).*comment "nd-deny"', out)
    return int(m.group(1)) if m else 0


def resolve_one(h: Harness, name: str) -> str:
    out = h.run(["getent", "ahostsv4", name], check=False).stdout
    m = re.match(r"(\d+\.\d+\.\d+\.\d+)", out)
    return m.group(1) if m else ""


def bridge_of(h: Harness) -> str:
    net_id = h.run(["docker", "network", "inspect", NET, "-f", "{{.Id}}"]).stdout.strip()
    return f"br-{net_id[:12]}"


def load_chain(h: Harness, veth: str, allow_ip: str) -> None:
    h.run(
        ["sudo", "nft", "-f", "-"],
        stdin=f"""
table netdev {TABLE} {{
    set allowed {{
        type ipv4_addr
        elements = {{ {allow_ip}, {GATEWAY} }}
    }}
    chain c_ingress {{
        type filter hook ingress device "{veth}" priority 0; policy accept;
        ip daddr @allowed counter accept comment "nd-allow"
        ip daddr 0.0.0.0/0 counter reject comment "nd-deny"
    }}
}}
""",
    )


def main() -> int:
    h = Harness("V4", "Can a guest with CAP_NET_ADMIN defeat a netdev chain on its host veth?")
    try:
        cleanup()
        h.run(["docker", "network", "create", "--subnet", SUBNET, NET])
        h.run(["docker", "run", "-d", "--name", BOX, "--network", NET,
               "--cap-add", "NET_ADMIN", "alpine", "sleep", "900"])
        h.run(["docker", "exec", BOX, "apk", "add", "--no-cache", "curl", "iproute2"])

        caps = guest(h, "sh", "-c", "grep CapEff /proc/self/status").stdout.strip()
        h.require(
            "the guest can actually reconfigure its interfaces",
            guest(h, "ip", "addr", "add", "10.218.0.222/24", "dev", "eth0").returncode == 0,
            f"{caps}. Without a REAL capability every 'still blocked' below is free — this "
            "is pf-spoof run 2's defect, where the sandbox lacked the capability under test "
            "and the run reported 7 PASS for the inverse of the truth",
        )
        guest(h, "ip", "addr", "del", "10.218.0.222/24", "dev", "eth0")
        veth = host_veth_of(h)

        # An allowlisted destination that actually serves HTTP, handed to the guest as an
        # ADDRESS. The first cut allowlisted 8.8.8.8 and curled it -- a DNS server, which
        # serves no HTTP, so the positive control failed and every "still blocked" in the
        # run was free. Resolving on the host and passing the address also keeps DNS out of
        # the path entirely, which is P1 run 1's rebuilt rig.
        allow_ip = resolve_one(h, "example.com")
        h.require("example.com resolves to an IPv4 address", bool(allow_ip), f"got {allow_ip!r}")
        # A denied destination ON-LINK, so the fragmentation arm never depends on the
        # internet carrying oversized datagrams.
        h.run(["sudo", "ip", "addr", "add", f"{ONLINK_DENIED}/24", "dev", bridge_of(h)])

        denied = h.probe("guest reaches the denied host", lambda: reaches(h, DENIED))
        denied.baseline(want=True, detail="no chain loaded")
        h.control("the allowlisted host answers before any chain", reaches(h, allow_ip),
                  f"HTTP to {allow_ip}")
        h.control("oversized ICMP to the gateway works before any chain",
                  guest(h, "ping", "-c", "2", "-W", "2", "-s", "3000", GATEWAY).returncode == 0,
                  "the fragmentation arm's positive path, on-link so the internet is not in it")
        h.control("oversized ICMP to the on-link denied address works before any chain",
                  guest(h, "ping", "-c", "2", "-W", "2", "-s", "3000",
                        ONLINK_DENIED).returncode == 0,
                  "and its negative path, before the rule that should stop it")

        load_chain(h, veth, allow_ip)
        h.expect("the netdev chain blocks before any tampering",
                 denied.sample("chain loaded, guest quiescent"), want=False)
        h.control("the deny rule fired", counter(h) > 0, f"{counter(h)} packets")
        h.control("the allowlisted host still answers with the chain loaded", reaches(h, allow_ip),
                  "the positive control that makes every block below meaningful")

        # -- attack 1: a second address (K1's, re-run against a netdev chain) --
        guest(h, "ip", "addr", "add", "10.218.0.222/24", "dev", "eth0", check=True)
        h.expect("adding an address does not defeat it",
                 denied.sample("after the guest added a second address"), want=False)

        # -- attack 2: a new MAC -------------------------------------------
        guest(h, "ip", "link", "set", "eth0", "down")
        guest(h, "ip", "link", "set", "eth0", "address", "02:aa:bb:cc:dd:ee")
        guest(h, "ip", "link", "set", "eth0", "up")
        restore_guest_routing(h)
        mac = guest(h, "cat", "/sys/class/net/eth0/address").stdout.strip()
        h.control("the guest's MAC actually changed", mac == "02:aa:bb:cc:dd:ee", f"now {mac}")
        h.control("the guest still has a working path after the flap", reaches(h, allow_ip),
                  "down/up must not cost it the network, or the next probe is free")
        h.expect("changing the MAC does not defeat it",
                 denied.sample("after the guest changed its MAC"), want=False)

        # -- attack 3: the carrier flap itself, which V5 predicts is inert --
        h.expect("a down/up flap does not unbind the chain",
                 denied.sample("after the guest flapped its interface"), want=False)
        h.measure("the chain is still attached to", chain_attached_to(h))

        # -- attack 4: the guest renames its own end -----------------------
        renamed = guest(h, "ip", "link", "set", "eth0", "down").returncode == 0 and \
            guest(h, "ip", "link", "set", "eth0", "name", "evil0").returncode == 0
        h.measure("the guest can rename its own interface", renamed)
        if renamed:
            guest(h, "ip", "link", "set", "evil0", "up")
            restore_guest_routing(h, "evil0")
            h.control("the guest still reaches the allowlisted host after renaming",
                      reaches(h, allow_ip), arm="rename")
            h.expect("renaming its own end does not defeat it",
                     denied.sample("after the guest renamed its own interface"),
                     want=False, arm="rename")
            guest(h, "ip", "link", "set", "evil0", "down")
            guest(h, "ip", "link", "set", "evil0", "name", "eth0")
            guest(h, "ip", "link", "set", "eth0", "up")
            restore_guest_routing(h)
        else:
            h.void_arm("rename", "the guest could not rename its own interface")

        # -- attack 5: fragmentation ---------------------------------------
        frag = guest(h, "ping", "-c", "2", "-W", "2", "-s", "3000", ONLINK_DENIED)
        h.control("oversized ICMP to the GATEWAY still works under the chain",
                  guest(h, "ping", "-c", "2", "-W", "2", "-s", "3000", GATEWAY).returncode == 0,
                  "the gateway is allowlisted, so a fragmented denial below is the rule "
                  "rather than fragmentation being broken on this path")
        h.measure(
            "fragmented ICMP to the denied on-link address is refused",
            frag.returncode != 0,
            "every fragment carries the IP header, so `ip daddr` should still match — an "
            "allowlist matching only first fragments would be a real evasion",
        )

        # -- attack 6: destroy the interface -------------------------------
        # NOT a reachability claim. After this the guest has no network, so "blocked"
        # would be free -- the corpus's single most common invalid shape. Structure only.
        deleted = guest(h, "ip", "link", "del", "eth0").returncode == 0
        h.measure("the guest can destroy its own interface", deleted)
        h.measure("the host-side veth survives the guest deleting its end",
                  host_dev_exists(h, veth),
                  "a veth pair dies together, so this is expected to be False — the "
                  "question is what happens to the chain")
        h.measure("the chain still exists after the device is destroyed",
                  h.run(["sudo", "nft", "list", "table", "netdev", TABLE],
                        check=False).returncode == 0,
                  "R11's stale-but-inert, reached by a hostile route rather than a "
                  "lifecycle one")
        h.measure("and nft now reports it attached to", chain_attached_to(h))
        h.measure(
            "SO: a guest can make itself unenforced by destroying its own interface",
            deleted and not host_dev_exists(h, veth),
            "it costs the guest its network at the same instant, so it is a denial of "
            "service against itself rather than an exfiltration path -- unless something "
            "restores the interface WITHOUT restoring the chain, which is Part 0's job",
        )

        h.not_tried(
            "whether anything RESTORES the interface after the guest destroys it. That is "
            "the case that turns this from self-harm into an escape, and it depends on the "
            "backend's restart behaviour — untested here and squarely Part 0's question",
            "CAP_SYS_ADMIN. R2 measured that the agent holds NET_ADMIN but not SYS_ADMIN, so "
            "it cannot enter another namespace; this run does not re-test that and inherits it",
            "a guest attempting to reach the nft socket, the docker socket, or the netlink "
            "group the host detector subscribes to",
            "IPv6 and UDP under tampering. The attacks are re-probed over TCP (curl) and "
            "ICMP (fragmentation) only",
            "traffic shaping to evade a counter-based detector — deliberately out of this "
            "round, and named in the round file as rogue-agent work",
            "rootless podman, where the chain lives in a namespace the guest cannot reach at "
            "all, and containerd",
            "concurrency: one guest, attacking alone, on an idle host",
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
