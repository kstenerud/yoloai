#!/usr/bin/env python3
# ABOUTME: V1 — does the nftables netlink event group deliver an ATTRIBUTABLE event for
# ABOUTME: every way our chain can stop enforcing? Decides whether Part 2's detector is
# ABOUTME: subscription-first with a poll backstop, or poll-first.

"""V1: what the subscription covers, and what it cannot.

The prior-art gate opened this round with a correction to the brief.
[`prior-art-egress-enforcement.md`](../prior-art-egress-enforcement.md) §3 records that
enforcement liveness is a known CVE-class bug (moby GHSA-x4rx-4gw3-53p4) and that the
industry remedy is **a subscription, not a poll** — Docker reacts to firewalld's reload
signal (moby PR #49443) rather than health-probing. The brief's Part 2 was written the
other way round, with a counter-versus-`rx_packets` comparison as the primary detector and
netlink events as an addition.

If the subscription covers our fault set, that inverts: events are primary, and the poll is
a backstop for the remainder. **The remainder is the interesting part**, because at least
one fault cannot possibly emit a ruleset event — V5 established that a netdev chain binds
by ifindex, so when the device disappears the chain goes inert with **nothing in the
ruleset changing at all**. There is no event to send. That fault is reachable both by
ordinary lifecycle (R11) and by a hostile guest destroying its own interface (V4).

So this measures, for each way enforcement can stop:

* does an event arrive at all, and
* can it be **attributed** to our table — a flush that says nothing about who it hit is a
  signal to re-check everything, which is a different and much more expensive design.

**`nft monitor` is the probe**, because it is the userspace face of the same netlink group
a daemon would subscribe to, and using it keeps this run about kernel behaviour rather than
about socket plumbing. What a Go implementation sees may differ in detail; that bound is
stated rather than glossed.

**The watcher proves itself alive before anything is concluded from its silence.**
`pf-change-signal.txt`'s S1a is the specimen: its notify half posted to itself to prove the
watcher was live and its log half had no equivalent, so one half's silence was evidence and
the other's was an unknown. Here the monitor must first be *seen* reporting a deliberate
change, and that same probe is what every later silence is measured against.

Run it as: `sudo -v && python3 v1_netlink_event_coverage.py`
"""

from __future__ import annotations

import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import IO

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

NS = "yb_v1_ns"
VETH = "yb_v1_v"
PEER = "yb_v1_p"
TABLE = "yb_v1_nd"
DECOY = "yb_v1_decoy"
HOST_IP = "10.219.0.1"
NS_IP = "10.219.0.2"
MONITOR_LOG = Path(tempfile.gettempdir()) / "yb_v1_monitor.log"
SETTLE = 1.2


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup(*, repair_docker: bool = False) -> None:
    """Tear down, and optionally repair the collateral damage this run causes.

    `nft flush ruleset` is host-wide and takes **docker's own chains** with it -- the next
    `docker network create` fails with "No chain/target/match by that name" for
    DOCKER-FORWARD. That is the L3d shape (sharing a manager's table is destroyed) arriving
    from the other direction, and it is a fact about the fault this run induces rather than
    a defect in the run. Restarting docker rebuilds them; leaving the host broken for the
    next experiment is not acceptable, and it cost one void run in this round.
    """
    _quiet(["sudo", "pkill", "-f", "nft monitor"])
    for t in (TABLE, DECOY):
        _quiet(["sudo", "nft", "delete", "table", "netdev", t])
        _quiet(["sudo", "nft", "delete", "table", "inet", t])
    _quiet(["sudo", "ip", "link", "del", VETH])
    _quiet(["sudo", "ip", "netns", "del", NS])
    _quiet(["sudo", "rm", "-f", str(MONITOR_LOG)])
    if repair_docker:
        _quiet(["sudo", "systemctl", "restart", "docker"])
        time.sleep(8)


class Monitor:
    """A live `nft monitor`, plus a mark-and-read cursor over its output.

    Reading from a cursor rather than the whole file is what lets each fault be judged on
    the events *it* produced. Without it a later fault inherits an earlier one's events and
    every arm reports success.
    """

    def __init__(self) -> None:
        self.proc: subprocess.Popen[bytes] | None = None
        self.sink: IO[bytes] | None = None
        self.cursor = 0

    def start(self) -> None:
        """Start `nft monitor` writing to a file THIS process opened.

        Not `sudo sh -c 'nft monitor > file'`: the shell redirect is performed by the
        elevated shell and failed with EACCES on this host, so the monitor never ran and
        every silence below was free -- which the alive-check duly caught. Opening the
        sink here and letting root inherit the descriptor avoids the question entirely.

        `stdbuf -oL` is load-bearing. Block buffering on a non-tty would hold events in
        libc until the buffer filled, and a late event is indistinguishable from an absent
        one at this timescale.
        """
        sink = MONITOR_LOG.open("wb")
        self.sink = sink
        self.proc = subprocess.Popen(  # noqa: S603
            ["sudo", "stdbuf", "-oL", "nft", "monitor"],
            stdout=sink,
            stderr=subprocess.STDOUT,
        )
        time.sleep(SETTLE)

    def mark(self) -> None:
        time.sleep(SETTLE)
        self.cursor = len(self._read())

    def since_mark(self) -> str:
        time.sleep(SETTLE)
        return self._read()[self.cursor:]

    def _read(self) -> str:
        try:
            return MONITOR_LOG.read_text(errors="replace")
        except OSError:
            return ""

    def stop(self) -> None:
        _quiet(["sudo", "pkill", "-f", "nft monitor"])
        if self.proc is not None:
            self.proc.wait(timeout=10)
        if self.sink is not None:
            self.sink.close()


def build_link(h: Harness) -> None:
    h.run(["sudo", "ip", "link", "add", VETH, "type", "veth", "peer", "name", PEER])
    h.run(["sudo", "ip", "link", "set", PEER, "netns", NS])
    h.run(["sudo", "ip", "addr", "add", f"{HOST_IP}/24", "dev", VETH])
    h.run(["sudo", "ip", "link", "set", VETH, "up"])
    h.run(["sudo", "ip", "netns", "exec", NS, "ip", "addr", "add", f"{NS_IP}/24", "dev", PEER])
    h.run(["sudo", "ip", "netns", "exec", NS, "ip", "link", "set", PEER, "up"])


def load_chain(h: Harness) -> None:
    h.run(
        ["sudo", "nft", "-f", "-"],
        stdin=f"""
table netdev {TABLE} {{
    chain c_ingress {{
        type filter hook ingress device "{VETH}" priority 0; policy accept;
        ip daddr {HOST_IP} counter drop comment "nd-deny"
    }}
}}
""",
    )


def enforcing(h: Harness) -> bool:
    """Is the chain actually stopping traffic right now? The ground truth every event
    claim is checked against -- an event is only useful if it corresponds to reality."""
    return (
        h.run(
            ["sudo", "ip", "netns", "exec", NS, "ping", "-c", "2", "-W", "2", HOST_IP],
            check=False,
        ).returncode
        != 0
    )


def main() -> int:
    h = Harness("V1", "Does the nftables netlink group report every way our chain stops "
                      "enforcing, attributably?")
    mon = Monitor()
    try:
        cleanup()
        h.run(["sudo", "ip", "netns", "add", NS])
        build_link(h)
        mon.start()

        # -- the watcher must be seen alive before its silence means anything ----
        saw_event = h.probe(
            "an event naming our table appears in the monitor",
            lambda: TABLE in mon.since_mark(),
        )
        mon.mark()
        saw_event.baseline(
            want=False,
            detail="nothing has touched our table yet, so silence here is the floor",
        )
        mon.mark()
        load_chain(h)
        h.expect(
            "the monitor reports our table being created, so it is alive and attributable",
            saw_event.sample("after loading our own chain"),
            want=True,
        )
        h.require(
            "the monitor is demonstrably alive",
            TABLE in MONITOR_LOG.read_text(errors="replace"),
            "if it is not, every 'no event arrived' below is free — and free in the "
            "reassuring direction, which is the 29:8 asymmetry D136 counted",
        )
        h.control("the chain is actually enforcing", enforcing(h),
                  "ground truth; an event about a chain that does not enforce is noise")

        # -- fault 1: our own table deleted ---------------------------------
        mon.mark()
        h.run(["sudo", "nft", "delete", "table", "netdev", TABLE])
        f1 = mon.since_mark()
        h.measure("FAULT 'delete our table': an event arrived", bool(f1.strip()))
        h.measure("  ... and it names our table", TABLE in f1)
        h.control("enforcement really did stop", not enforcing(h))

        # -- fault 2: host-wide flush ruleset --------------------------------
        load_chain(h)
        h.control("re-armed for fault 2", enforcing(h))
        mon.mark()
        h.run(["sudo", "nft", "flush", "ruleset"])
        f2 = mon.since_mark()
        h.measure("FAULT 'flush ruleset': an event arrived", bool(f2.strip()))
        h.measure("  ... and it names our table", TABLE in f2,
                  "if not, the signal is 'something happened' and the detector must "
                  "re-check every sandbox rather than one")
        h.control("enforcement really did stop after the flush", not enforcing(h))
        h.measure(
            "the host-wide flush also destroyed docker's own chains",
            _quiet(["docker", "network", "create", "--subnet", "10.231.0.0/24",
                    "yb_v1_probe"]).returncode != 0,
            "L3d's finding from the other direction: a manager's flush takes every "
            "table with it, ours and docker's alike. Part 3's reconcile runs on a host "
            "where the runtime itself may be broken",
        )
        _quiet(["docker", "network", "rm", "yb_v1_probe"])

        # -- fault 3: a config reload that begins with flush ruleset ---------
        load_chain(h)
        h.control("re-armed for fault 3", enforcing(h))
        mon.mark()
        h.run(["sudo", "nft", "-f", "-"], stdin=f"""
flush ruleset
table inet {DECOY} {{
    chain c {{ type filter hook forward priority 0; policy accept; }}
}}
""")
        f3 = mon.since_mark()
        h.measure("FAULT 'reload with flush ruleset': an event arrived", bool(f3.strip()))
        h.measure("  ... and it names our table", TABLE in f3,
                  "the systemctl-restart-nftables shape the prior art says emits no "
                  "firewalld signal — but this is nft's own group, not firewalld's")
        h.control("enforcement really did stop after the reload", not enforcing(h))

        # -- fault 4: the device disappears. NOTHING in the ruleset changes. --
        _quiet(["sudo", "nft", "delete", "table", "inet", DECOY])
        load_chain(h)
        h.control("re-armed for fault 4", enforcing(h))
        h.control(
            "the table still exists right before the device is removed",
            h.run(["sudo", "nft", "list", "table", "netdev", TABLE],
                  check=False).returncode == 0,
        )
        mon.mark()
        h.run(["sudo", "ip", "link", "del", VETH])
        f4 = mon.since_mark()
        silent = h.measure(
            "FAULT 'device deleted': the monitor was SILENT",
            not f4.strip(),
            "the fault V5 predicts is unreportable: the chain binds by ifindex, the device "
            "is gone, and no ruleset object changed — so there is nothing to send an event "
            "about",
        )
        h.measure("  ... the table is still listed as present",
                  h.run(["sudo", "nft", "list", "table", "netdev", TABLE],
                        check=False).returncode == 0,
                  "which is exactly why an inventory check cannot see this either")
        h.expect(
            "at least one way of losing enforcement emits no event at all, so a "
            "subscription cannot be the whole detector",
            silent,
            want=True,
            unbaselined="a silence, measured against a watcher this run already showed "
                        "reporting our table; there is no mechanism to remove for a "
                        "before/after on the absence itself",
        )

        h.not_tried(
            "a Go netlink subscriber. `nft monitor` is the userspace face of the same "
            "group, which keeps this about the kernel — but what NFNLGRP_NFTABLES delivers "
            "to a raw socket may differ in detail, and the implementation must re-check",
            "iptables-nft traffic from docker. Docker writes iptables rules that land in "
            "nft tables on this host, so its churn may be a substantial event source; "
            "nothing here measures the noise floor a real subscriber would face",
            "firewalld's D-Bus reload signal, which is the prior art's actual mechanism and "
            "a different bus entirely. firewalld is not installed on this host",
            "event LATENCY and loss. Each arm sleeps and then reads; a subscriber under "
            "buffer pressure can lose events, and NFNLGRP overruns are a documented class",
            "whether an event can be attributed to a SANDBOX rather than a table, which is "
            "what the response policy actually needs",
            "concurrency: two sandboxes' tables changing at once",
            "the device REAPPEARING, which is R11 and V5's ground",
        )
        return 0 if h.report() else 1
    finally:
        mon.stop()
        cleanup(repair_docker=True)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
