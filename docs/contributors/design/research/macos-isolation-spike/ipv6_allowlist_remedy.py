#!/usr/bin/env python3
# ABOUTME: ipv6-sidestep.txt showed the allowlist does not constrain IPv6 at all. This is the
# ABOUTME: chosen remedy measured end to end: mirror the allowlist into ip6tables plus an
# ABOUTME: inet6 ipset, and check it denies, permits, and does not take the v4 half down.

"""Does mirroring the allowlist into IPv6 actually close the hole?

**Post-round, owner-prompted.** Round 2 is closed; this is not one of its items.

[`ipv6-sidestep.txt`](results/ipv6-sidestep.txt) measured the hole: with a v4 default-deny
installed and a peer refused over IPv4 in the same second, the guest reaches it over IPv6,
because `firewall.py` installs v4 rules only and `ip6tables`' OUTPUT policy is `ACCEPT`. Of
the candidate remedies the owner chose this one — mirror the allowlist into the second family
rather than switch the family off — because it is the option that survives a user who
legitimately needs IPv6.

Three things have to hold before that is a plan rather than a preference, and none of them is
an argument:

1. **The machinery exists on the blessed image.** An `inet6` ipset, and `ip6tables` able to
   match it — which needs `xt_set` for the v6 family, not just the v4 one. `firewall.py`
   already carries a fallback for hosts whose iptables lacks `xt_set` at all, so this is not
   guaranteed.
2. **It denies, and it permits.** A default-deny that blocks everything is not an allowlist;
   the same peer must become reachable when its address is added.
3. **It does not take the v4 half down with it.** That is [DF134](../../findings-unresolved.md)'s
   whole story: a v6 address fed to `iptables -d` aborted the install and destroyed the
   *correct* v4 rules, so a single v6 nameserver used to disable isolation entirely. The
   families must be populated independently, and this run checks that the v4 chain is
   untouched by everything the v6 half does.

**The baseline is the hole itself**, taken on this rig rather than cited: with no v6 policy
the peer answers over IPv6, which is what makes the denial afterwards mean something.

Instrument boundary: nothing timed, no host privilege, no pf rule loaded.

Run it as: `python3 ipv6_allowlist_remedy.py`
"""

from __future__ import annotations

import json
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

IMAGE = "yoloai-base:latest"
A, B = "yb6ra", "yb6rb"
PORT = 8080
STATE: dict[str, object] = {}


def _q(argv: list[str], timeout: int = 240) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def guest(box: str, script: str) -> str:
    return _q(["container", "exec", box, "sh", "-c", script]).stdout.strip()


def cleanup() -> None:
    for b in (A, B):
        _q(["container", "stop", b])
        _q(["container", "rm", b])


def reach(box: str, url: str) -> str:
    return guest(box, f"curl -s -o /dev/null -m 6 -w '%{{http_code}}' '{url}' 2>/dev/null; echo")


def main() -> int:
    h = Harness("IPV6FIX", "Does mirroring the allowlist into ip6tables close the v6 hole?")
    try:
        cleanup()
        for b in (B, A):
            _q(["container", "run", "-d", "--name", b, "--network", "default",
                "--cap-add", "NET_ADMIN", IMAGE, "sleep", "600"])
        for b in (A, B):
            for _ in range(60):
                if guest(b, "echo ok") == "ok":
                    break
                time.sleep(1)
        h.require("both guests are up",
                  guest(A, "echo ok") == "ok" and guest(B, "echo ok") == "ok")

        b4 = b6 = ""
        for _ in range(30):
            n = json.loads(_q(["container", "inspect", B]).stdout)[0]["status"]["networks"][0]
            b4, b6 = n["ipv4Address"].split("/")[0], n.get("ipv6Address", "").split("/")[0]
            if b6 and b6 in guest(B, "ip -6 addr show eth0"):
                break
            time.sleep(1)
        h.require("the peer configured its ULA", bool(b6) and b6 in
                  guest(B, "ip -6 addr show eth0"))
        h.measure("peer", f"{B} v4={b4} v6={b6}")
        guest(B, f"nohup python3 -m http.server {PORT} --bind :: >/dev/null 2>&1 & sleep 1")
        time.sleep(2)

        def v6_reaches_peer() -> bool:
            code = reach(A, f"http://[{b6}]:{PORT}/")
            STATE["v6"] = code
            return code not in ("", "000")

        probe = h.probe("the guest reaches its peer over IPv6", v6_reaches_peer)

        # -- BASELINE: the hole, reproduced on this rig -------------------------------
        guest(A, "iptables -F OUTPUT; iptables -P OUTPUT ACCEPT; "
                 "ip6tables -F OUTPUT; ip6tables -P OUTPUT ACCEPT")
        probe.baseline(want=True)
        h.measure("baseline v6 read", STATE.get("v6"),
                  "no v6 policy — this is ipv6-sidestep.txt's finding, re-taken here so the "
                  "denial below is measured against it rather than cited")

        # -- 1. the machinery ---------------------------------------------------------
        setup = guest(A, """
ipset create -exist allowed6 hash:net family inet6 && echo SET=ok || echo SET=fail
ip6tables -F OUTPUT
ip6tables -A OUTPUT -o lo -j ACCEPT
ip6tables -A OUTPUT -m set --match-set allowed6 dst -j ACCEPT && echo MATCH=ok || echo MATCH=fail
ip6tables -P OUTPUT DROP && echo POLICY=ok || echo POLICY=fail
""")
        h.control("an inet6 ipset exists and ip6tables can match it",
                  "SET=ok" in setup and "MATCH=ok" in setup and "POLICY=ok" in setup,
                  setup.replace("\n", " ") + " — xt_set for the v6 family, which "
                  "firewall.py's own fallback shows is not guaranteed")

        # -- the v4 half must be installed and must stay working ----------------------
        guest(A, "ipset create -exist allowed4 hash:net; ipset flush allowed4; "
                 f"ipset add -exist allowed4 {b4}; "
                 "iptables -F OUTPUT; iptables -A OUTPUT -o lo -j ACCEPT; "
                 "iptables -A OUTPUT -m set --match-set allowed4 dst -j ACCEPT; "
                 "iptables -P OUTPUT DROP")
        v4_rules_before = guest(A, "iptables -S OUTPUT | wc -l")

        # -- 2a. it DENIES -------------------------------------------------------------
        blocked = probe.sample("v6 default-deny, peer not in allowed6")
        h.measure("v6 read under the mirrored allowlist", STATE.get("v6"))
        h.control("the v4 half still permits its own allowlisted peer",
                  reach(A, f"http://{b4}:{PORT}/") not in ("", "000"),
                  "so the v6 policy did not take v4 down with it — DF134's failure mode")

        # -- 2b. it PERMITS ------------------------------------------------------------
        guest(A, f"ipset add -exist allowed6 {b6}")
        permitted = reach(A, f"http://[{b6}]:{PORT}/")
        h.control("adding the peer to the v6 set makes it reachable again",
                  permitted not in ("", "000"),
                  f"v6 -> {permitted}. A default-deny that blocks everything is not an "
                  "allowlist, so this is what distinguishes the two")

        # -- 3. DF134's hazard, still live and now bounded ----------------------------
        df134 = guest(A, f"iptables -A OUTPUT -d {b6} -j ACCEPT 2>&1 | head -1")
        v4_rules_after = guest(A, "iptables -S OUTPUT | wc -l")
        h.measure("feeding a v6 address to the v4 tool", df134 or "(accepted!)",
                  "DF134's mechanism, confirmed still live — the families must be populated "
                  "independently and a v6 address must never reach `iptables`")
        h.control("that failure did not damage the v4 chain",
                  v4_rules_before == v4_rules_after,
                  f"{v4_rules_before} rules before, {v4_rules_after} after — the command "
                  "fails cleanly, so the hazard is in the CALLER aborting an install "
                  "part-way, not in iptables corrupting itself")

        h.expect(
            "mirroring the allowlist into ip6tables closes the IPv6 hole",
            blocked, want=False,
        )

        h.not_tried(
            "AAAA resolution, which is the real cost of this option. The addresses here are "
            "link-scope peers typed in by the harness; the product would have to resolve "
            "each allowlisted domain in BOTH families, doubling the host/guest divergence "
            "and one-shot-decay surface `dns-gaps.txt` and `dns-split-horizon-sim.txt` "
            "already measured for v4",
            "a domain that resolves to different sets per family, which is the v6 form of "
            "split horizon and is not a hypothetical for CDN-fronted hosts",
            "the DNS rules themselves. `firewall.py` also allows the resolver; nothing here "
            "installs a v6 nameserver rule, and DF134 exists precisely because v6 "
            "nameservers were what broke the v4 install",
            "link-local traffic. `fe80::/10` is not covered by a `hash:net` set of global "
            "addresses and every guest has a link-local address permanently; whether the "
            "default-deny catches it is unmeasured",
            "ICMPv6. A v6 default-deny that drops Neighbour Discovery breaks the network in "
            "ways a v4 default-deny does not, because NDP is not optional the way ARP "
            "handling is — this run's policy blocks it and nothing checked what that costs "
            "over a longer life than the probe's",
            "the fallback path for hosts whose iptables lacks `xt_set`. `firewall.py` has "
            "one for v4; the v6 half would need its own and this run does not exercise it",
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
