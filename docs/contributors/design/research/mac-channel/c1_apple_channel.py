#!/usr/bin/env python3
# ABOUTME: C1 of the mac-channel round — does the `apple` backend expose a
# ABOUTME: host<->guest data channel we can drive, and does it survive a
# ABOUTME: guest with no IP stack (shape B)?

"""C1: apple's host<->guest channel.

Decides whether `apple` gets D139's shape (B) — a guest with no IP stack reaching a
host-side proxy over a socket — or must fall back to shape (A), a normal stack with a
one-address filter. If (B) is available here, and tart has its own out-of-sandbox
enforcement point (C2), then no backend needs (A) and D139's fallback clause has
nothing to cover.

The instrument's boundary: everything measured here is the `container` CLI's own
behaviour on this host. Nothing goes through yoloAI, because yoloAI does not pass
these flags today — that is C3's subject, and A37 applies to it and not to this run.
"""

import os
import socket
import subprocess
import sys
import threading
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "..", "..", "..", "scripts"))
from research_harness_v2 import Harness  # noqa: E402

CONTAINER = "/usr/local/bin/container"
IMAGE = "c1probe"
# AF_UNIX sun_path is 108 bytes, so the host end of a published socket cannot live
# under a long scratch path. This is a real constraint on where yoloAI may put a
# proxy socket, and it is recorded as a measurement below rather than only here.
SOCKDIR = "/tmp/yc1"
PROBEDIR = os.path.dirname(os.path.abspath(__file__))


def sh(*args, timeout=180):
    return subprocess.run(args, capture_output=True, text=True, timeout=timeout)


def rm_container(name):
    sh(CONTAINER, "rm", "-f", name)


def start_guest_server(name, sockpath, extra_args=()):
    """Start a detached guest listening on a unix socket inside the guest."""
    rm_container(name)
    args = [CONTAINER, "run", "-d", "--name", name]
    args += list(extra_args)
    args += ["-v", f"{PROBEDIR}:/probe", IMAGE, "python3", "/probe/c1_guest_server.py", sockpath]
    return sh(*args)


# The probe below must be ONE callable sampled across every arm, or the baseline and
# the samples are two instruments that merely look alike — the shape that produced
# `pf-no-state` run 5. So the arm's host socket lives here and the arm switches it,
# rather than each sample binding its own path.
ARM = {"hostsock": None, "egress_args": ()}


def host_roundtrip(hostsock, payload=b"PING\n", timeout=8):
    """True only if the host both sent and received bytes over the channel."""
    try:
        c = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        c.settimeout(timeout)
        c.connect(hostsock)
        c.sendall(payload)
        reply = c.recv(4096)
        c.close()
        return b"PONG-FROM-GUEST" in reply
    except Exception:
        return False


def guest_egress_ok(extra_args=()):
    """True if a guest reaches an external address."""
    args = [CONTAINER, "run", "--rm"] + list(extra_args) + [
        IMAGE, "sh", "-c",
        "wget -T3 -q -O- http://1.1.1.1 >/dev/null 2>&1 && echo REACHED || echo BLOCKED",
    ]
    p = sh(*args)
    return "REACHED" in p.stdout


def main():
    h = Harness("C1", "does the apple backend expose a host<->guest data channel, and does it survive a guest with no IP stack?")

    h.require("the container CLI is present", os.path.exists(CONTAINER))
    status = sh(CONTAINER, "system", "status")
    h.require("the container daemon is running", "running" in status.stdout,
              detail=status.stdout.strip().splitlines()[1] if status.stdout else "")
    h.require("the probe image exists", IMAGE in sh(CONTAINER, "image", "list").stdout,
              detail=f"{IMAGE}: alpine:3.22 + python3/socat/iproute2, so no probe depends on the network under test")

    os.makedirs(SOCKDIR, exist_ok=True)

    # ---- what the guest can see of vsock at all -------------------------------
    vs = sh(CONTAINER, "run", "--rm", "-v", f"{PROBEDIR}:/probe", IMAGE,
            "python3", "/probe/c1_guest_vsock.py")
    h.measure("guest AF_VSOCK/CID probe", vs.stdout.strip().replace("\n", " | "),
              detail="the raw guest-side capability read, before any channel is asked for")

    # ---- probe 1: the channel itself -----------------------------------------
    # Baseline with the mechanism ABSENT: a container started WITHOUT
    # --publish-socket. The host socket does not exist, so the round trip must fail.
    bare_sock = os.path.join(SOCKDIR, "bare.sock")
    if os.path.exists(bare_sock):
        os.unlink(bare_sock)
    start_guest_server("c1-bare", "/tmp/g.sock")
    time.sleep(4)
    ARM["hostsock"] = bare_sock
    channel = h.probe("the host completes a round trip with the guest over a published socket",
                      lambda: host_roundtrip(ARM["hostsock"]))
    channel.baseline(want=False,
                     detail="same guest, same listener, but the container was started without "
                            "--publish-socket, so there is no host end to connect to")
    rm_container("c1-bare")

    # Now WITH the mechanism, on the default network.
    net_sock = os.path.join(SOCKDIR, "net.sock")
    if os.path.exists(net_sock):
        os.unlink(net_sock)
    start_guest_server("c1-net", "/tmp/g.sock",
                       extra_args=("--publish-socket", f"{net_sock}:/tmp/g.sock"))
    time.sleep(4)
    ARM["hostsock"] = net_sock
    m_net = channel.sample("with --publish-socket, default network",
                           detail=f"host end {net_sock}")
    rm_container("c1-net")

    # And with the mechanism on a guest that has NO IP STACK. This is the shape (B)
    # question: the channel must survive the thing that makes (B) (B).
    none_sock = os.path.join(SOCKDIR, "none.sock")
    if os.path.exists(none_sock):
        os.unlink(none_sock)
    start_guest_server("c1-none", "/tmp/g.sock",
                       extra_args=("--network", "none", "--publish-socket", f"{none_sock}:/tmp/g.sock"))
    time.sleep(4)
    ifaces = sh(CONTAINER, "exec", "c1-none", "sh", "-c", "ip -o addr | awk '{print $2}' | sort -u | tr '\\n' ' '")
    h.control("the stackless guest really has only lo", ifaces.stdout.strip(),
              detail="if this shows an ethernet device the arm is not measuring a stackless guest")
    ARM["hostsock"] = none_sock
    m_none = channel.sample("with --publish-socket, --network none",
                            detail=f"host end {none_sock}")

    # ---- probe 2: egress, so 'stackless' is a measurement and not a label -----
    ARM["egress_args"] = ()
    egress = h.probe("a guest reaches an external address",
                     lambda: guest_egress_ok(ARM["egress_args"]))
    egress.baseline(want=True, detail="default network, no flags — the positive control A22 requires")
    ARM["egress_args"] = ("--network", "none")
    m_egress_none = egress.sample("under --network none",
                                  detail="same probe, same image, same host, --network none added")

    # ---- what the channel is like, for whoever builds on it ------------------
    # Direction. `container` creates the host end itself and refuses if it exists,
    # so the guest listens and the host connects — the inverse of what an egress
    # proxy wants, and the single most consequential fact for the build.
    clash = sh(CONTAINER, "run", "--rm", "--publish-socket", f"{none_sock}:/tmp/x.sock",
               IMAGE, "true")
    h.measure("host end pre-existing", (clash.stderr + clash.stdout).strip()[:160],
              detail="container creates the host end; it refuses to reuse one, which is what "
                     "establishes the direction as guest-listens / host-connects")

    # Concurrency: a proxy needs more than one connection at a time.
    conc = sum(1 for _ in range(5) if host_roundtrip(none_sock, b"PING\n"))
    h.measure("sequential round trips over one published socket (of 5)", conc)
    parallel_ok = []

    def worker():
        parallel_ok.append(host_roundtrip(none_sock, b"PING\n"))
    ts = [threading.Thread(target=worker) for _ in range(5)]
    [t.start() for t in ts]
    [t.join() for t in ts]
    h.measure("concurrent round trips over one published socket (of 5)", sum(parallel_ok),
              detail="a chokepoint proxy needs many in flight; one accept-loop served them")

    # Can the guest INITIATE, which is the direction a proxy client wants?
    gi = sh(CONTAINER, "exec", "c1-none", "python3", "/probe/c1_guest_initiate.py")
    h.measure("guest-initiated vsock to host CID 2", gi.stdout.strip().replace("\n", " | "),
              detail="whether the guest can open its own channel out, rather than being connected to")

    h.measure("AF_UNIX sun_path budget for the host end", len(SOCKDIR) + len("/xxxxxxxx.sock"),
              detail="108-byte limit; a host end under ~/.yoloai/sandboxes/<name>/ can exceed it, "
                     "which bit this run before it was noticed")

    rm_container("c1-none")

    # ---- claims --------------------------------------------------------------
    h.expect("apple exposes a host<->guest channel that carries arbitrary bidirectional data",
             m_net)
    h.expect("that channel survives a guest with no IP stack, which is shape (B)",
             m_none)
    h.expect("--network none actually removes the guest's egress",
             m_egress_none, want=False)

    h.not_tried(
        "**the guest-initiated direction, beyond the vsock probe above.** `--publish-socket` is "
        "guest-listens/host-connects. An egress proxy wants the opposite, so a build on this needs "
        "either a guest-side shim that multiplexes local connections over the channel, or a host "
        "proxy that connects in and speaks a reverse protocol. Which of those is cheaper is a "
        "design question this run does not open",
        "whether yoloAI can pass these flags at all. `runtime/apple` passes neither --network nor "
        "--publish-socket today; what it costs to thread them through is a code question (and the "
        "--network half is C3's defect)",
        "the channel under the real image. This runs alpine+python3, not yoloai-base, whose "
        "entrypoint.py takes over PID 1 — so nothing here says the shim can be started before the "
        "agent, only that the host end exists from container start",
        "throughput and latency. Every payload here is a few bytes; whether this channel carries a "
        "real HTTP session at usable speed is unmeasured and is P2's question one layer down",
        "restart and lifecycle. Whether the host end survives a container stop/start, a daemon "
        "restart, or a reboot — the whole subject of enforcement-state-reaping.md — is untouched",
        "SOCK_DGRAM, and anything other than a stream socket",
        "n. One host, one macOS (26.5.1), container CLI 1.0.0, one boot per arm",
    )

    ok = h.report()
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
