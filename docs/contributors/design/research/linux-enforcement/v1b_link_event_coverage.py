#!/usr/bin/env python3
# ABOUTME: V1b — the one fault the nftables netlink group cannot report is the device
# ABOUTME: vanishing. That is a LINK event, not a ruleset event. If the link group covers
# ABOUTME: it, Part 2's detector is two subscriptions and no polling at all.

"""V1b: the complement of V1, added mid-round because V1's result asked for it.

V1 measured that the nftables netlink group reports every *ruleset* fault attributably —
our table deleted, a host-wide `flush ruleset`, a reload beginning with a flush — and is
**completely silent** for one: the chain's device disappearing. V5 explains why. The chain
binds by ifindex; when the device goes, nothing in the ruleset changes, so there is no
event to send and the table still lists as present.

The brief's Part 2 answers that with a counter-versus-`rx_packets` poll, whose known cost
is the idle-sandbox ambiguity (V6): a rule at 0 packets on a device at 0 packets is
indistinguishable from a healthy sandbox that is simply not talking, and reporting *healthy*
there is the free negative the macOS canary shipped once.

**But a device disappearing is exactly what `RTNLGRP_LINK` exists to announce.** If the link
group reports it, the detector is two subscriptions covering disjoint fault classes, with no
inference from traffic anywhere — and the idle-sandbox problem does not arise, because
nothing is ever inferred from silence.

Three ways the device can go, and the middle one is the security-relevant case:

1. the sandbox is stopped normally — the runtime tears the veth down;
2. **a guest with `CAP_NET_ADMIN` destroys its own end** (V4 measured that it can, and that
   a veth pair dies together), which is a guest choosing to be unenforced;
3. the host side is deleted directly.

`ip monitor link` is the probe, for V1's reason: it is the userspace face of the same group
a daemon subscribes to, which keeps the run about kernel behaviour rather than socket
plumbing. The same bound applies and is repeated in the bounding section rather than assumed
to carry over.

**The watcher proves itself alive first** — V1's lesson, and `pf-change-signal.txt`'s S1a
before it. A silent monitor makes every "no event" free, in the reassuring direction.

Run it as: `sudo -v && python3 v1b_link_event_coverage.py`
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

NET = "yb_v1b_net"
BOX_A = "yb_v1b_a"
BOX_B = "yb_v1b_b"
SUBNET = "10.220.0.0/24"
MONITOR_LOG = Path(tempfile.gettempdir()) / "yb_v1b_monitor.log"
SETTLE = 1.5


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["sudo", "pkill", "-f", "ip monitor"])
    for box in (BOX_A, BOX_B):
        _quiet(["docker", "rm", "-f", box])
    _quiet(["docker", "network", "rm", NET])
    _quiet(["rm", "-f", str(MONITOR_LOG)])


class LinkMonitor:
    """`ip monitor link`, with a mark-and-read cursor so each fault is judged on its own
    events rather than inheriting the previous arm's."""

    def __init__(self) -> None:
        self.proc: subprocess.Popen[bytes] | None = None
        self.sink: IO[bytes] | None = None
        self.cursor = 0

    def start(self) -> None:
        sink = MONITOR_LOG.open("wb")
        self.sink = sink
        self.proc = subprocess.Popen(  # noqa: S603
            ["sudo", "stdbuf", "-oL", "ip", "monitor", "link"],
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
        _quiet(["sudo", "pkill", "-f", "ip monitor"])
        if self.proc is not None:
            self.proc.wait(timeout=10)
        if self.sink is not None:
            self.sink.close()


def host_veth_of(h: Harness, box: str) -> str:
    iflink = h.run(["docker", "exec", box, "cat", "/sys/class/net/eth0/iflink"]).stdout.strip()
    for line in h.run(["ip", "-o", "link"]).stdout.splitlines():
        if line.startswith(iflink + ":"):
            return line.split(":")[1].strip().split("@")[0]
    raise HarnessError(f"no host interface with ifindex {iflink} for {box}")


def dev_exists(h: Harness, dev: str) -> bool:
    return h.run(["ip", "-o", "link", "show", dev], check=False).returncode == 0


def main() -> int:
    h = Harness("V1b", "Does the link netlink group report the device faults the nftables "
                       "group cannot?")
    mon = LinkMonitor()
    try:
        cleanup()
        h.run(["docker", "network", "create", "--subnet", SUBNET, NET])
        mon.start()

        # -- the watcher must be seen alive before any silence is evidence ----
        seen = h.probe(
            "the monitor reports a device event in this window",
            lambda: bool(mon.since_mark().strip()),
        )
        mon.mark()
        seen.baseline(want=False, detail="nothing has touched a device yet; the floor")

        mon.mark()
        h.run(["docker", "run", "-d", "--name", BOX_A, "--network", NET,
               "alpine", "sleep", "900"])
        h.expect(
            "a sandbox starting is announced on the link group, so the watcher is alive",
            seen.sample("after starting a sandbox"),
            want=True,
        )
        veth_a = host_veth_of(h, BOX_A)
        h.control("the sandbox has a host-side veth", dev_exists(h, veth_a), f"{veth_a}")

        # -- fault 1: a hostile guest destroys its own end -------------------
        # V4's attack, which is the case that turns a lifecycle question into a security
        # one: the guest can choose to be unenforced, and the host must notice.
        h.run(["docker", "run", "-d", "--name", BOX_B, "--network", NET,
               "--cap-add", "NET_ADMIN", "alpine", "sleep", "900"])
        h.run(["docker", "exec", BOX_B, "apk", "add", "--no-cache", "iproute2"])
        veth_b = host_veth_of(h, BOX_B)
        h.control("the hostile sandbox has a host-side veth too", dev_exists(h, veth_b),
                  f"{veth_b}")
        mon.mark()
        killed = h.run(["docker", "exec", BOX_B, "ip", "link", "del", "eth0"],
                       check=False).returncode == 0
        h.control("the guest really did destroy its interface", killed)
        hostile = mon.since_mark()
        h.measure("FAULT 'guest destroys its own end': an event arrived",
                  bool(hostile.strip()))
        names_b = h.measure(
            "  ... and it names the host-side veth",
            veth_b in hostile,
            "attribution is the whole question: an event that does not say WHICH device "
            "forces a re-check of every sandbox",
        )
        h.control("the host-side veth really is gone", not dev_exists(h, veth_b),
                  "ground truth — the event must correspond to reality")
        h.control("the OTHER sandbox's veth is untouched", dev_exists(h, veth_a),
                  "the discrimination control")
        h.expect(
            "a guest unbinding its own enforcement is announced, attributably",
            names_b,
            want=True,
            unbaselined="the baseline for this probe is the empty window above; this is a "
                        "different measurement (name attribution) over the same event",
        )

        # -- fault 2: an ordinary stop ---------------------------------------
        mon.mark()
        h.run(["docker", "rm", "-f", BOX_A])
        ordinary = mon.since_mark()
        h.measure("FAULT 'sandbox stopped normally': an event arrived", bool(ordinary.strip()))
        h.measure("  ... and it names the host-side veth", veth_a in ordinary)
        h.control("that veth is gone too", not dev_exists(h, veth_a))

        h.not_tried(
            "a Go rtnetlink subscriber. `ip monitor link` is the userspace face of the same "
            "group; what a raw RTNLGRP_LINK socket delivers may differ in detail and the "
            "implementation must re-check. Same bound as V1",
            "event loss under pressure. rtnetlink sockets can overrun and drop, which is "
            "precisely when a fleet of sandboxes is churning — and a dropped delete is a "
            "silently unenforced sandbox. Nothing here measures the buffer",
            "ORDERING against the nftables group. Two subscriptions mean two streams, and "
            "whether a device-delete can be observed before or after a related ruleset "
            "event is not established",
            "the device REAPPEARING and whether the reinstall is racing it — that is Part "
            "0's window, which V2 established exists",
            "rootless podman and containerd/CNI, where device churn happens in another "
            "namespace and the host-side monitor may not see it at all",
            "a guest that renames rather than destroys, which V4 measured is inert for "
            "enforcement but would also emit a link event and could be used as noise",
            "how many events a busy host emits, i.e. the noise floor a real subscriber "
            "filters against",
        )
        return 0 if h.report() else 1
    finally:
        mon.stop()
        cleanup()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
