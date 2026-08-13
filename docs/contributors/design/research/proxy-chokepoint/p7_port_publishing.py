#!/usr/bin/env python3
# ABOUTME: P7 — does `--port` survive a guest with no IP stack? The shipped feature that decides
# ABOUTME: whether shape (B) costs one channel per backend or two, measured against the closest
# ABOUTME: analogue the product already has: --network-none.

"""P7: the other falsification test for the fork.

Shape (B) — a guest with no IP stack, reaching a host-side proxy over a socket — wins on
containment because it rests solely on the `CAP_NET_ADMIN` bounding-set drop, the one vector
`agent-privilege-reality.txt` measured surviving `sudo`. It has exactly one known cost, and it
is not exotic: **`--port` is a shipped, documented feature** (`--port host:container`,
`GUIDE.md`, `parsePortBindings` in `launch.go`) that composes with `--network-isolated` today.
"Run the dev server and open it in my browser" is an ordinary thing to want.

If (B) needs a *second* per-backend channel to carry ingress back in, its cost roughly doubles on
every backend, and shape (A) — one filter rule over a normal stack — becomes the better answer on
the majority backends too. So this is a fork question, not a detail.

**What is actually measurable here.** Shape (B) does not exist yet, so the honest proxy for it is
`--network-none`, which the product already ships and which removes the interface rather than
filtering it — the same structural position a socket-only guest would be in. Three things get
asked, in order of how much they decide:

1. Does `--port` work at all today? Without this the rest is unanchored, and it is the
   probe's negative self-test: a port that is *not* published must not be reachable.
2. Does `--port` compose with `--network-none`?
3. If it does not, **how does it fail** — refused at the CLI, or silently accepted and
   quietly inert? A flag that is accepted and does nothing is worse than one that is refused,
   and it is the outcome that would make (B) expensive *and* confusing.

**Instrument boundary.** Inside the region: whether a listener inside the sandbox is reachable
from the host on the published port. Scaffolding: the listener itself, started with `exec`, and
a fixed host port. Not measured: whether a *reverse* forwarder over a socket could restore this
under (B) — that is a design question about something unbuilt, and answering it by argument here
would be the inference-overreach class.

Run it as: `python3 p7_port_publishing.py`
"""

from __future__ import annotations

import os
import socket
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from chokepoint_rig import YOLOAI, quiet, sh  # noqa: E402
from research_harness_v2 import Harness, HarnessError  # noqa: E402

BOX_OPEN = "p7open"
BOX_NONE = "p7none"
HOST_PORT = 18099
GUEST_PORT = 8099
WORKDIR = os.environ.get("P7_WORKDIR", str(Path.home() / "p7-repo"))
PROMPT = "Do nothing at all. Exit immediately without using any tools."


def cleanup() -> None:
    for b in (BOX_OPEN, BOX_NONE):
        quiet([str(YOLOAI), "destroy", b, "--abandon-unapplied"])


def serve(h: Harness, box: str) -> None:
    """Start a listener inside the sandbox, detached, and give it a moment."""
    sh(h, box,
       f"nohup python3 -m http.server {GUEST_PORT} --bind 0.0.0.0 "
       f">/tmp/httpd.log 2>&1 & echo started", timeout=30)
    time.sleep(2)


def host_reaches(port: int) -> bool:
    """An HTTP round trip, not a TCP connect.

    docker publishes a host port by binding it at CREATE time, so `connect()` succeeds
    whether or not anything is listening inside — the proxy accepts and then fails to
    forward. A TCP-level probe therefore reports True with an empty sandbox behind it,
    which is the free positive this run was voided for on its first attempt.
    """
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=4) as s:
            s.sendall(b"GET / HTTP/1.0\r\nHost: localhost\r\n\r\n")
            s.settimeout(4)
            return b"HTTP/" in s.recv(4096)
    except OSError:
        return False


def listening_inside(h: Harness, box: str) -> bool:
    out = sh(h, box, f"python3 -c \"import socket;s=socket.create_connection"
                     f"(('127.0.0.1',{GUEST_PORT}),3);print('INSIDE_OK')\" 2>&1", timeout=30)
    return "INSIDE_OK" in out.stdout


def main() -> int:
    h = Harness("P7", "Does `--port` survive a guest with no IP stack?")
    try:
        cleanup()

        # -- 1. the feature works today, and the probe can report both answers --
        opened = h.run([str(YOLOAI), "run", BOX_OPEN, WORKDIR, "-p", PROMPT,
                        "--port", f"{HOST_PORT}:{GUEST_PORT}", "--tty"], check=False)
        h.control("a sandbox with --port launched", opened.returncode == 0,
                  f"rc={opened.returncode} {opened.stderr[-200:]}")

        reachable = h.probe("the host reaches a listener inside the sandbox",
                            lambda: host_reaches(HOST_PORT))
        reachable.baseline(want=False,
                           detail="published, but nothing is listening inside yet. A TCP connect "
                                  "SUCCEEDS here because docker binds the host port at create "
                                  "time, so the probe must complete an HTTP round trip or it "
                                  "reports reachability for an empty sandbox")
        serve(h, BOX_OPEN)
        h.control("something really is listening inside the sandbox",
                  listening_inside(h, BOX_OPEN),
                  "otherwise an unreachable host port says nothing about publishing")
        h.expect("`--port` publishes a guest listener to the host, which is the shipped "
                 "behaviour shape (B) would have to preserve",
                 reachable.sample("with a listener running inside"), want=True, arm="today")

        # -- 2. and now with the interface removed -----------------------------
        none = h.run([str(YOLOAI), "run", BOX_NONE, WORKDIR, "-p", PROMPT,
                      "--port", f"{HOST_PORT + 1}:{GUEST_PORT}", "--network-none", "--tty"],
                     check=False)
        accepted = h.measure(
            "the CLI accepts --port together with --network-none",
            none.returncode == 0,
            f"rc={none.returncode}; stderr={none.stderr.strip()[-300:]!r}",
            arm="none")

        if accepted.value:
            serve(h, BOX_NONE)
            inside = h.measure("a listener does start inside the network-none sandbox",
                               listening_inside(h, BOX_NONE),
                               "loopback exists even with no external interface, so the "
                               "listener is expected to work; the question is reachability",
                               arm="none")
            got = h.measure("the host reaches the published port of a network-none sandbox",
                            host_reaches(HOST_PORT + 1),
                            "if this is False while the CLI accepted the flag, the flag is "
                            "accepted and inert — the worst of the three outcomes, because "
                            "nothing tells the user their port will never work",
                            arm="none")
            h.measure("so `--port` under a stack-less guest is",
                      "SILENTLY INERT" if (inside.value and not got.value)
                      else "working" if got.value else "inconclusive",
                      "this is the shape shape (B) would inherit unless a reverse channel is "
                      "built, and it is why P7 is a fork question rather than a detail",
                      arm="none")
        else:
            h.measure("so `--port` under a stack-less guest is",
                      "REFUSED AT THE CLI",
                      "the better of the two failure modes: the user is told, at the moment "
                      "they ask, rather than discovering it when nothing connects",
                      arm="none")

        h.not_tried(
            "shape (B) itself. `--network-none` removes the interface, which is the same "
            "structural position a socket-only guest is in, but it is an ANALOGUE and not the "
            "thing — nothing here measures a real socket channel because none exists yet",
            "a reverse forwarder. Whether ingress could be restored over the same socket that "
            "carries egress is the obvious next question and is a design question about "
            "something unbuilt; answering it by argument here would be inference-overreach",
            "the other backends. docker only. apple, tart and containerd each publish ports "
            "their own way and none is examined",
            "whether anyone USES --port with isolation. The feature composes today and is "
            "documented; how often that combination is reached is not knowable from here",
            "UDP ports, port ranges, and binding to a specific host interface, all of which "
            "`parsePortBindings` may or may not accept",
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
