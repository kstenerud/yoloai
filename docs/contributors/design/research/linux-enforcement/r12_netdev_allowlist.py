#!/usr/bin/env python3
# ABOUTME: R12 — can a netdev chain express the policy this design actually needs: a
# ABOUTME: named set as an allowlist, a default deny, live revocation by set update,
# ABOUTME: and the gateway carve-out credential injection depends on?

"""R12: does the candidate mechanism express the real policy?

R10 found that a netdev ingress chain bound to a sandbox's veth keys per-sandbox with
no `br_netfilter` dependency, and R11 measured its lifecycle. On the strength of those
the plan now prefers it — **on the evidence of a single-address `drop`.** The policy
this design actually needs is an allowlist: a named set of permitted destinations, a
default deny for everything else, live revocation by removing a set element, and a
carve-out for the gateway that credential injection rides on.

None of that is implied by "a one-line drop worked". A netdev chain is a different
object from a filter chain, it sits at a different hook, and R10's own bounds flagged
two open questions this run has to answer: whether sets work there at all, and whether
`reject` is even available (R10 used `drop` because it did not know).

**If any of this fails the recommendation reverses**, and `physdev` plus a
`br_netfilter` preflight comes back as the Linux mechanism. That is the point of
running it before the brief rather than after.

Run it as: `sudo -v && python3 r12_netdev_allowlist.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v1 import Harness, HarnessError  # noqa: E402

NET = "yb_r12_net"
BOX = "yb_r12_box"
TABLE = "yb_r12"
SUBNET = "10.207.0.0/24"
GATEWAY = "10.207.0.1"
DENIED = "1.1.1.1"


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["sudo", "nft", "delete", "table", "netdev", TABLE])
    _quiet(["docker", "rm", "-f", BOX])
    _quiet(["docker", "network", "rm", NET])


def host_veth_of(h: Harness) -> str:
    iflink = h.run(["docker", "exec", BOX, "cat", "/sys/class/net/eth0/iflink"]).stdout.strip()
    for line in h.run(["ip", "-o", "link"]).stdout.splitlines():
        if line.startswith(iflink + ":"):
            return line.split(":")[1].strip().split("@")[0]
    raise HarnessError(f"no host interface with ifindex {iflink}")


def reaches(h: Harness, host: str) -> bool:
    return (
        h.run(
            ["docker", "exec", BOX, "curl", "-s", "-m", "5", "-o", "/dev/null", f"http://{host}/"],
            check=False,
        ).returncode
        == 0
    )


def resolves(h: Harness) -> bool:
    """DNS is UDP and rides the same chain. A policy that silently kills name
    resolution looks like a network failure, not a policy decision."""
    return (
        h.run(["docker", "exec", BOX, "nslookup", "example.com"], check=False).returncode == 0
    )


def counter(h: Harness, comment: str) -> int:
    out = h.run(["sudo", "nft", "list", "table", "netdev", TABLE], check=False).stdout
    for line in out.splitlines():
        if f'comment "{comment}"' in line:
            m = re.search(r"packets (\d+)", line)
            if m:
                return int(m.group(1))
    return 0


def main() -> int:
    h = Harness("R12", "Can a netdev chain hold a set-based allowlist, revoke live, and "
                       "keep the gateway carve-out?")
    try:
        cleanup()
        h.require("br_netfilter is NOT loaded",
                  "br_netfilter" not in _quiet(["lsmod"]).stdout,
                  "the whole appeal of this mechanism is not needing it")
        allowed = subprocess.run(
            ["getent", "ahostsv4", "example.com"], capture_output=True, text=True, check=False
        ).stdout.split()
        h.require("example.com resolves on the host", bool(allowed))
        allow_ip = allowed[0]

        h.run(["docker", "network", "create", "--subnet", SUBNET, NET])
        h.run(["docker", "run", "-d", "--name", BOX, "--network", NET, "alpine", "sleep", "600"])
        h.run(["docker", "exec", BOX, "apk", "add", "--no-cache", "curl"])
        veth = host_veth_of(h)

        h.control("allowlisted host reachable with no policy", reaches(h, allow_ip))
        h.control("denied host reachable with no policy", reaches(h, DENIED))
        h.control("DNS resolves with no policy", resolves(h))

        # Is `reject` usable in a netdev chain? R10 used `drop` and did not know.
        #
        # Run 1 answered "no" and was WRONG: the probe was a one-line table definition
        # nft cannot parse, so it failed on a brace and the verdict keyword was never
        # reached. A free negative, in the run whose whole subject is free negatives.
        # The control below is what makes the answer mean anything -- the identical
        # rule with `drop` must load, or "reject fails" is a statement about syntax.
        def loads(verdict: str) -> bool:
            _quiet(["sudo", "nft", "delete", "table", "netdev", f"{TABLE}_p"])
            done = subprocess.run(  # noqa: S603
                ["sudo", "nft", "-f", "-"],
                input=f'table netdev {TABLE}_p {{\n  chain c {{\n'
                      f'    type filter hook ingress device "{veth}" priority 0; policy accept;\n'
                      f"    ip daddr {DENIED} {verdict}\n  }}\n}}\n",
                capture_output=True, text=True, check=False,
            )
            _quiet(["sudo", "nft", "delete", "table", "netdev", f"{TABLE}_p"])
            return done.returncode == 0

        h.control("the identical rule with `drop` loads", loads("drop"),
                  "without this, a `reject` failure is a statement about my syntax")
        reject_loads = h.measure("`reject` loads in a netdev chain", loads("reject"))
        h.expect("a netdev chain accepts reject", reject_loads, want=True)
        verdict = "drop"  # the set policy below is built with drop; reject is timed after

        # The real shape: a named set, a gateway carve-out, DNS, then default deny.
        # Only IPv4 is judged — ARP and IPv6 fall through to the chain policy, because
        # dropping ARP at veth ingress costs the sandbox its network entirely and would
        # look like a policy that works by breaking everything.
        h.run(
            ["sudo", "nft", "-f", "-"],
            stdin=f"""
table netdev {TABLE} {{
    set allowed {{ type ipv4_addr; flags interval; elements = {{ {allow_ip} }} }}
    chain c_ingress {{
        type filter hook ingress device "{veth}" priority 0; policy accept;
        meta protocol ip ip daddr @allowed counter accept comment "allow"
        meta protocol ip ip daddr {GATEWAY} counter accept comment "gateway"
        meta protocol ip udp dport 53 counter accept comment "dns"
        meta protocol ip counter {verdict} comment "deny"
    }}
}}
""",
        )
        h.control("the set loaded with its element",
                  allow_ip in h.run(["sudo", "nft", "list", "set", "netdev", TABLE,
                                     "allowed"]).stdout)

        a_allow = h.measure("allowlisted host reachable under the set policy",
                            reaches(h, allow_ip))
        a_deny = h.measure("denied host blocked under the set policy", reaches(h, DENIED))
        a_dns = h.measure("DNS still resolves under the set policy", resolves(h))
        h.control("the deny rule fired", counter(h, "deny") > 0, f"{counter(h, 'deny')} packets")
        h.control("the allow rule matched", counter(h, "allow") > 0,
                  f"{counter(h, 'allow')} packets — proves the reach was decided by our rule")
        h.expect("a named set works as an allowlist in a netdev chain", a_allow, want=True)
        h.expect("and everything outside it is denied", a_deny, want=False)
        h.expect("without collaterally killing DNS", a_dns, want=True)

        # -- live revocation by set update, no reload -------------------------
        deny_before = counter(h, "deny")
        h.run(["sudo", "nft", "delete", "element", "netdev", TABLE, "allowed",
               f"{{ {allow_ip} }}"])
        revoked = h.measure("the formerly-allowed host is blocked after a set delete",
                            reaches(h, allow_ip))
        h.control("the deny rule fired again after revocation",
                  counter(h, "deny") > deny_before,
                  f"{deny_before} -> {counter(h, 'deny')}")
        h.expect("revocation is a live set update, with no chain reload", revoked, want=False)

        h.run(["sudo", "nft", "add", "element", "netdev", TABLE, "allowed", f"{{ {allow_ip} }}"])
        restored = h.measure("re-adding the element restores reach", reaches(h, allow_ip))
        h.expect("and it is reversible", restored, want=True)

        # -- does `reject` DO anything here, or merely load? -------------------
        # Loading is not enforcing -- that is this workstream's most expensive lesson,
        # and a netdev ingress hook has no routing context to build an ICMP or RST
        # from, so the keyword loading says nothing about what the sender sees.
        # R10 measured the forward hook at drop 5.06s vs reject 0.10s; the same
        # comparison here is the only thing that answers it.
        import time as _time

        def timed_denied_probe() -> tuple[bool, float]:
            t0 = _time.monotonic()
            ok = reaches(h, DENIED)
            return ok, _time.monotonic() - t0

        drop_blocked, drop_secs = timed_denied_probe()
        h.run(["sudo", "nft", "delete", "table", "netdev", TABLE])
        h.run(
            ["sudo", "nft", "-f", "-"],
            stdin=f"""
table netdev {TABLE} {{
    set allowed {{ type ipv4_addr; flags interval; elements = {{ {allow_ip} }} }}
    chain c_ingress {{
        type filter hook ingress device "{veth}" priority 0; policy accept;
        meta protocol ip ip daddr @allowed counter accept comment "allow"
        meta protocol ip ip daddr {GATEWAY} counter accept comment "gateway"
        meta protocol ip udp dport 53 counter accept comment "dns"
        meta protocol ip counter reject comment "deny"
    }}
}}
""",
        )
        rej_blocked, rej_secs = timed_denied_probe()
        h.control("the reject-shaped policy still blocks", not rej_blocked,
                  f"took {rej_secs:.2f}s")
        h.control("the reject rule fired", counter(h, "deny") > 0, f"{counter(h, 'deny')} packets")
        fast = h.measure(
            "reject fails FAST in a netdev chain, as it does in the forward hook",
            rej_secs < drop_secs / 2,
            f"reject {rej_secs:.2f}s vs drop {drop_secs:.2f}s. If these are equal, the "
            "keyword loads and the sender still sees a black hole — netdev ingress has no "
            "routing context to build a response from",
        )
        h.expect("a netdev reject gives the sandbox an immediate error, not a hang",
                 fast, want=True)
        h.measure("drop blocked too, so the comparison is like-for-like", drop_blocked is False,
                  f"drop took {drop_secs:.2f}s")

        h.not_tried(
            "allowlist SIZE. One element. p2 measured the forward hook at 1/1000/10000 and found "
            "run-to-run variance larger than any difference between configurations; nothing here "
            "prices a netdev chain at any size, and the hook is per-packet on every device",
            "an in-flight transfer. Revocation here is measured on a NEW connection; p1b's "
            "sustained-transfer shape has not been run against a netdev chain, and that is the "
            "arm where the conntrack fast-path mattered",
            "IPv6 and ARP, which this policy deliberately lets past the chain policy rather than "
            "judging. A v6-capable host would need the v6 half written and measured",
            "the gateway carve-out against the REAL broker. `10.207.0.1` is allowlisted by address "
            "here; r7 measured the broker path on the forward/input hooks, not this one",
            "the reply direction and `hook egress`",
            "whether a set can be shared across sandboxes' chains, which is what the reaping "
            "design's per-sandbox records would want",
            "rootless podman and containerd/CNI. docker only",
            "a hostile guest attempting to influence the chain or its set",
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
