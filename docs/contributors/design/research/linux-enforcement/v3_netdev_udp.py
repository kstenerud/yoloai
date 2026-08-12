#!/usr/bin/env python3
# ABOUTME: V3 — does the netdev chain's allowlist cover UDP, or is the corpus's
# ABOUTME: TCP-only evidence hiding an exfiltration path? DNS rides on UDP, so this
# ABOUTME: also asks what the policy costs resolution.

"""V3: the protocol the whole corpus skipped.

`enforcement-build.md` § *Known-unmeasured* lists **UDP, on both platforms, throughout.
DNS rides on it.** That is not a gap in coverage, it is a gap in a containment claim:
every rule and every probe behind the Linux design is TCP, and an allowlist that only
constrains TCP is an allowlist an errant agent walks around by using a UDP client.

Three things measured, in one rig:

1. **Is a denied UDP destination actually denied?** `ip daddr` is protocol-agnostic in
   principle, so the expected answer is yes — which is exactly why it needs measuring
   rather than asserting. R10's `reject` result and R12's allowlist result are both
   TCP-only, and this design is about to ship on them.
2. **Does an allowlisted UDP destination still work?** The half that makes a block
   meaningful rather than a dead path.
3. **What does the sandbox experience?** R10 found `reject` turns a 5.09s black hole into
   a 0.06s error for TCP. UDP's `reject` is an ICMP port-unreachable and may behave
   differently; an agent hanging on every denied UDP flow is a usability defect even when
   containment is correct.

**The probe is `dig @<address>`**, which is real UDP with an unambiguous exit code, and it
sidesteps docker's embedded resolver at `127.0.0.11` — that lives inside the sandbox's own
netns and never crosses the veth the chain is attached to, so it cannot answer anything
about host-side enforcement. Two public resolvers are used as destinations: one
allowlisted, one denied. `+tcp` re-runs the identical query over TCP so the two protocols
are compared on the same rig in the same run, rather than against R10's numbers from a
different one.

**Tooling is installed before any policy loads.** Three runs in this corpus recorded a free
"blocked" because the package manager could not reach its repositories under a policy the
run had already applied (L5d, L10c, R2).

Run it as: `sudo -v && python3 v3_netdev_udp.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

NET = "yb_v3_net"
BOX = "yb_v3_a"
TABLE = "yb_v3_nd"
SUBNET = "10.217.0.0/24"
GATEWAY = "10.217.0.1"
ALLOWED = "8.8.8.8"
DENIED = "1.1.1.1"


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["sudo", "nft", "delete", "table", "netdev", TABLE])
    _quiet(["docker", "rm", "-f", BOX])
    _quiet(["docker", "network", "rm", NET])


def host_veth_of(h: Harness, box: str) -> str:
    iflink = h.run(["docker", "exec", box, "cat", "/sys/class/net/eth0/iflink"]).stdout.strip()
    for line in h.run(["ip", "-o", "link"]).stdout.splitlines():
        if line.startswith(iflink + ":"):
            return line.split(":")[1].strip().split("@")[0]
    raise HarnessError(f"no host interface with ifindex {iflink} for {box}")


def dig(h: Harness, server: str, *, tcp: bool = False) -> tuple[bool, float]:
    """One DNS query at a named server. Returns (succeeded, seconds)."""
    argv = ["docker", "exec", BOX, "dig", f"@{server}", "+time=3", "+tries=1"]
    if tcp:
        argv.append("+tcp")
    argv.append("example.com")
    start = time.monotonic()
    rc = h.run(argv, check=False).returncode
    elapsed = time.monotonic() - start
    return rc == 0, elapsed


def counter(h: Harness, comment: str) -> int:
    out = h.run(["sudo", "nft", "list", "table", "netdev", TABLE], check=False).stdout
    m = re.search(rf'packets (\d+).*comment "{comment}"', out)
    return int(m.group(1)) if m else 0


def load_chain(h: Harness, veth: str, verdict: str) -> None:
    """The rule shape Part 1 proposes: a named set as the allowlist, everything else denied."""
    _quiet(["sudo", "nft", "delete", "table", "netdev", TABLE])
    h.run(
        ["sudo", "nft", "-f", "-"],
        stdin=f"""
table netdev {TABLE} {{
    set allowed {{
        type ipv4_addr
        elements = {{ {ALLOWED}, {GATEWAY} }}
    }}
    chain c_ingress {{
        type filter hook ingress device "{veth}" priority 0; policy accept;
        ip daddr @allowed counter accept comment "nd-allow"
        ip daddr 0.0.0.0/0 counter {verdict} comment "nd-deny"
    }}
}}
""",
    )


def main() -> int:
    h = Harness("V3", "Does a netdev allowlist cover UDP, and what does it cost DNS?")
    try:
        cleanup()
        h.run(["docker", "network", "create", "--subnet", SUBNET, NET])
        h.run(["docker", "run", "-d", "--name", BOX, "--network", NET, "alpine", "sleep", "900"])
        # Before any policy. L5d/L10c/R2 all recorded a free negative from getting this
        # order wrong -- the package manager could not reach its repos under the policy
        # the run had already applied, so the tool under test was never installed.
        h.run(["docker", "exec", BOX, "apk", "add", "--no-cache", "bind-tools"])
        veth = host_veth_of(h, BOX)

        udp_denied = h.probe("UDP/53 to the denied resolver", lambda: dig(h, DENIED)[0])
        tcp_denied = h.probe("TCP/53 to the denied resolver", lambda: dig(h, DENIED, tcp=True)[0])

        udp_denied.baseline(want=True, detail="no policy loaded; without this every block is free")
        tcp_denied.baseline(want=True, detail="the same query over TCP, for the comparison")
        # The allowlisted resolver is a CONTROL, not a claim. It reads the same before and
        # after the policy loads, so it cannot discriminate and v2 refuses to let an
        # expectation rest on it -- correctly. What it establishes is that a block measured
        # on the other probe is the rule rather than a dead path.
        h.control(
            "the allowlisted resolver answers before any policy",
            dig(h, ALLOWED)[0],
            "the positive half, so a denial below means something",
        )

        # -- ARM 1: reject -------------------------------------------------
        load_chain(h, veth, "reject")
        before_deny = counter(h, "nd-deny")
        u_blocked = udp_denied.sample("under a reject-shaped netdev allowlist")
        _, udp_reject_time = dig(h, DENIED)
        t_blocked = tcp_denied.sample("under the same policy, over TCP")
        h.control(
            "the allowlisted resolver STILL answers with the policy loaded",
            dig(h, ALLOWED)[0],
            "an allowlist that blocks everything is not an allowlist",
        )

        h.control(
            "the deny rule fired",
            counter(h, "nd-deny") > before_deny,
            f"{before_deny} -> {counter(h, 'nd-deny')} packets",
        )
        h.control(
            "the allow rule matched, so the set is doing work",
            counter(h, "nd-allow") > 0,
            f"{counter(h, 'nd-allow')} packets",
        )
        h.expect("a netdev allowlist denies UDP, not only TCP", u_blocked, want=False)
        h.expect("TCP is denied by the same rule on the same rig", t_blocked, want=False)

        # -- ARM 2: drop, for the latency comparison -----------------------
        load_chain(h, veth, "drop")
        _, udp_drop_time = dig(h, DENIED)
        drop_blocked = h.measure(
            "drop blocks UDP too, so the timing comparison is like-for-like",
            not dig(h, DENIED)[0],
        )
        h.control("the drop rule fired", counter(h, "nd-deny") > 0,
                  f"{counter(h, 'nd-deny')} packets")

        faster = h.measure(
            "reject answers a denied UDP query faster than drop",
            udp_reject_time < udp_drop_time,
            f"reject {udp_reject_time:.2f}s vs drop {udp_drop_time:.2f}s",
        )
        h.expect(
            "reject spares the sandbox a timeout on denied UDP as it does on TCP",
            faster,
            want=True,
            unbaselined="a latency comparison between two rule shapes, not a "
                        "before/after on one mechanism",
        )
        h.measure("drop_blocked was", drop_blocked.value)

        h.not_tried(
            "UDP that is not DNS. `dig` is the probe because its exit code is unambiguous; "
            "QUIC, WireGuard, and plain datagram exfiltration are the same `ip daddr` match "
            "in principle and are not run here",
            "docker's EMBEDDED resolver at 127.0.0.11. It lives in the sandbox's own netns "
            "and never crosses the veth, so host-side enforcement cannot see it at all — "
            "which also means a sandbox using it is unaffected by this policy, and nothing "
            "here measures what that resolver will forward to",
            "the reply direction. Every rule is `ip daddr` on ingress; a UDP response "
            "arriving for an allowlisted flow is not separately checked",
            "IPv6, which this policy deliberately lets past the chain policy",
            "fragmented UDP, and datagrams larger than one MTU",
            "rootless podman and containerd/CNI. docker only",
            "a hostile guest, which is V4",
            "whether ICMP port-unreachable from `reject` leaks anything about the host",
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
