#!/usr/bin/env python3
# ABOUTME: P1b ported onto research_harness_v1 — does removing an allowlist entry stop
# ABOUTME: an in-flight transfer, and does the conntrack fast-path prevent it? Sampled
# ABOUTME: across a 30s window, because a TCP transfer stalls gradually.

"""P1b, rewritten against the harness (D134).

The second of the migrations chosen to exercise the harness against real hardware.
K1 was picked for being load-bearing; this one was picked for having a *different
shape*: two arms where one must survive, a per-arm precondition that voids the run
rather than reporting a free zero, and a verdict that depends on a rule counter
rather than on a probe's exit code. It is the only thing so far to use
`counter_moved()`.

**Ported faithfully, including its address keying.** The rules say `ip saddr <client>`
because that is what the original ran, and a port whose rules differ cannot tell you
whether a difference in result came from the harness or from the experiment. The
fast-path question is independent of the key: `ct state established,related accept`
short-circuits evaluation whatever the rule beneath it matches on. Re-running this
under interface keying is a *new* experiment, not this one.

Run it as: `sudo -v && python3 p1b_revocation_decay.py`
"""

from __future__ import annotations

import re
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v1 import Harness, HarnessError, counter_moved  # noqa: E402

TABLE = "yb_p1b"
SERVER = "yb_p1b_srv"
CLIENT = "yb_p1b_cli"
SAMPLE_SECONDS = 5
SAMPLES = 6


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup(restore_br_netfilter: bool) -> None:
    _quiet(["sudo", "nft", "delete", "table", "inet", TABLE])
    _quiet(["docker", "rm", "-f", SERVER, CLIENT])
    if restore_br_netfilter:
        _quiet(["sudo", "sysctl", "-qw", "net.bridge.bridge-nf-call-iptables=0"])
        _quiet(["sudo", "modprobe", "-r", "br_netfilter"])


def address_of(h: Harness, box: str) -> str:
    out = h.run(
        ["docker", "inspect", "-f",
         "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", box]
    ).stdout.strip()
    return out


def load_policy(h: Harness, *, fastpath: bool, client: str, server: str) -> None:
    _quiet(["sudo", "nft", "delete", "table", "inet", TABLE])
    fast = (
        f'ip saddr {client} ct state established,related counter accept comment "fastpath"'
        if fastpath
        else ""
    )
    h.run(
        ["sudo", "nft", "-f", "-"],
        stdin=f"""
table inet {TABLE} {{
    set allowed {{ type ipv4_addr; elements = {{ {server} }} }}
    chain c_forward {{
        type filter hook forward priority -10; policy accept;
        {fast}
        ip saddr {client} ip daddr @allowed counter accept comment "allowlisted"
        ip saddr {client} counter drop comment "post-revoke-drops"
    }}
}}
""",
    )


def downloaded_bytes(h: Harness) -> int:
    out = h.run(
        ["docker", "exec", CLIENT, "sh", "-c", "wc -c < /tmp/dl 2>/dev/null || echo 0"],
        check=False,
    ).stdout.strip()
    return int(out or 0)


def drop_counter(h: Harness) -> int:
    listing = h.run(["sudo", "nft", "list", "table", "inet", TABLE]).stdout
    for line in listing.splitlines():
        if "post-revoke-drops" in line:
            m = re.search(r"packets (\d+)", line)
            if m:
                return int(m.group(1))
    return 0


def run_arm(h: Harness, *, fastpath: bool, client: str, server: str) -> tuple[list[int], int]:
    """One arm. Returns (per-sample KB/s, drop-counter delta across the window)."""
    name = "fastpath" if fastpath else "no-fastpath"
    load_policy(h, fastpath=fastpath, client=client, server=server)
    _quiet(["docker", "exec", CLIENT, "sh", "-c", "rm -f /tmp/dl"])
    h.run(
        ["docker", "exec", "-d", CLIENT, "sh", "-c",
         f"curl -s --limit-rate 300k -m 120 -o /tmp/dl http://{server}/big"]
    )
    time.sleep(6)
    pre = downloaded_bytes(h)
    # Not a control: a control that fails voids the whole run, and an arm that never
    # started should void that arm's *conclusion*. v1 has no such distinction, so this
    # is a precondition -- deliberately fatal, because reporting a zero rate for a
    # transfer that never began is exactly the free negative this file exists to avoid.
    h.require(
        f"[{name}] the transfer is running before revocation",
        pre > 100_000,
        f"only {pre} bytes after 6s",
    )
    counter_before = drop_counter(h)
    h.run(["sudo", "nft", "delete", "element", "inet", TABLE, "allowed", f"{{ {server} }}"])

    rates, last = [], pre
    for _ in range(SAMPLES):
        time.sleep(SAMPLE_SECONDS)
        now = downloaded_bytes(h)
        rates.append((now - last) // SAMPLE_SECONDS // 1024)
        last = now
    counter_after = drop_counter(h)
    _quiet(["docker", "exec", CLIENT, "sh", "-c", "pkill curl"])
    return rates, counter_after - counter_before


def main() -> int:
    h = Harness(
        "P1b",
        "Does removing an allowlist entry stop an in-flight transfer, and does the "
        "conntrack fast-path prevent that?",
    )
    loaded_br_netfilter = False
    try:
        # Container-to-container traffic on one bridge is bridged, not routed, so it
        # reaches the ip forward hook only with br_netfilter on. Without this the
        # rules load and never fire, and every arm reports a free "survives".
        if _quiet(["lsmod"]).stdout.find("br_netfilter") < 0:
            h.run(["sudo", "modprobe", "br_netfilter"])
            loaded_br_netfilter = True
        h.run(["sudo", "sysctl", "-qw", "net.bridge.bridge-nf-call-iptables=1"])

        h.run(["docker", "run", "-d", "--name", SERVER, "nginx:alpine"])
        h.run(
            ["docker", "exec", SERVER, "sh", "-c",
             "dd if=/dev/urandom of=/usr/share/nginx/html/big bs=1M count=200 2>/dev/null"]
        )
        h.run(["docker", "run", "-d", "--name", CLIENT, "alpine", "sleep", "900"])
        # Before any policy exists: `apk add` under a denied egress hangs, and the hang
        # reads as a blocked probe. Three runs in this workstream lost to that.
        h.run(["docker", "exec", CLIENT, "apk", "add", "--no-cache", "curl"])
        h.require(
            "curl present in the client",
            h.run(["docker", "exec", CLIENT, "sh", "-c", "command -v curl"], check=False).returncode
            == 0,
        )
        time.sleep(2)
        server_ip, client_ip = address_of(h, SERVER), address_of(h, CLIENT)
        h.require("both containers have addresses", bool(server_ip) and bool(client_ip))

        fast_rates, fast_drops = run_arm(h, fastpath=True, client=client_ip, server=server_ip)
        slow_rates, slow_drops = run_arm(h, fastpath=False, client=client_ip, server=server_ip)

        # The fast-path arm is the control: it must SURVIVE. Without it the run shows a
        # transfer stopping, not the fast-path's absence stopping it.
        h.control(
            "with the fast-path, the revoked transfer keeps flowing",
            fast_rates[-1] > 0,
            f"KB/s across the window: {fast_rates}",
        )
        h.control(
            "with the fast-path, no packet ever reaches the deny rule",
            not counter_moved(0, fast_drops),
            f"drop counter moved by {fast_drops}",
        )

        stopped = h.measure(
            "without the fast-path, the transfer stops",
            slow_rates[-1] == 0,
            f"KB/s across the window: {slow_rates}",
        )
        fired = h.measure(
            "without the fast-path, the deny rule actually fired",
            counter_moved(0, slow_drops),
            f"drop counter moved by {slow_drops}",
        )
        h.expect("revocation stops an in-flight transfer once the fast-path is gone", stopped)
        h.expect("the stop is caused by the deny rule, not by the transfer ending", fired)

        h.not_tried(
            "whether the client surfaces an error or hangs — curl is given -m 120, and what a "
            "real agent's HTTP stack does with a stalled socket is untested",
            "UDP",
            "the reply direction: bulk data flows server->client, matches no rule, and falls "
            "through an accept policy. macOS measured this direction to be load-bearing there "
            "(pf-no-state.txt); nothing here measures it on Linux",
            "interface keying. These rules say `ip saddr` because the original did, so the two "
            "runs are comparable. The fast-path result is independent of the key, but that is "
            "an argument, not a measurement",
            "a destination with more than one address — P1 run 1 was invalidated by exactly "
            "that, and this uses a local nginx to avoid it rather than to settle it",
            "concurrency: one client, one flow, one server",
        )
        return 0 if h.report() else 1
    finally:
        cleanup(loaded_br_netfilter)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
