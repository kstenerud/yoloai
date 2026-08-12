#!/usr/bin/env python3
# ABOUTME: Scaffolding for P2/P4 — a minimal host-side HTTP proxy that speaks CONNECT and
# ABOUTME: absolute-URI GET, resolves on behalf of the guest, and logs every requested target.
# ABOUTME: NOT a product component and deliberately not hardened; see the warning below.

"""A stand-in for the host-side proxy, so the round can measure coverage before anything is built.

**This is a research rig, not a prototype.** It exists to answer "can the honest path work when
the guest's only destination is a proxy", and it is disqualified from being the basis of any
implementation for one specific reason: it does **no** validation of the client-supplied
hostname. `egress-proxy-build.md` makes SNI/name hardening a hard requirement, with a live
precedent — Claude Code's own egress proxy was bypassed for ~5.5 months because its allowlist
and its `Dial` disagreed about `evil.com\\x00.anthropic.com`. This rig would fall to exactly
that, on purpose: an allowlist here would create the illusion that the round had tested one.

So it **allows everything and records what was asked for.** The recording is the point. P1 could
not census hostnames — extracting SNI from a capture needs a TLS parser this host has no tooling
for — but a proxy is *told* the name in the clear, in the `CONNECT` line, before any TLS starts.
So the same run that measures whether the work completes also produces the destination list, and
that list is what the closure question in [D137](../../../decisions/working-notes.md) §2 needs.

Two behaviours matter for what the round concludes:

* **The guest never resolves.** `CONNECT api.anthropic.com:443` carries a name, and this process
  resolves it host-side. That is P6's question answered by construction rather than by policy,
  and it is the inversion `prior-art` §1 records Cilium making for the same reason.
* **It is TCP-only and knows nothing about UDP.** A chokepoint served by an HTTP proxy cannot
  carry QUIC, DNS-over-UDP, or anything else that is not a TCP stream. P1 measured zero QUIC, so
  this is a fair rig for that session; it is not a fair rig for a client that would use it.

Usage: `chokepoint_proxy.py <bind-host> <port> <logfile>`
"""

from __future__ import annotations

import socket
import sys
import threading
from pathlib import Path

BUFSIZE = 65536
_log_lock = threading.Lock()


def log(path: Path, line: str) -> None:
    with _log_lock:
        with path.open("a") as fh:
            fh.write(line + "\n")
            fh.flush()


def pipe(a: socket.socket, b: socket.socket) -> None:
    try:
        while True:
            data = a.recv(BUFSIZE)
            if not data:
                break
            b.sendall(data)
    except OSError:
        pass
    finally:
        for s in (a, b):
            try:
                s.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass


def handle(client: socket.socket, logfile: Path) -> None:
    upstream: socket.socket | None = None
    try:
        client.settimeout(30)
        head = b""
        while b"\r\n\r\n" not in head:
            chunk = client.recv(BUFSIZE)
            if not chunk:
                return
            head += chunk
            if len(head) > 65536:
                return
        first = head.split(b"\r\n", 1)[0].decode("latin-1")
        parts = first.split()
        if len(parts) < 2:
            return
        method, target = parts[0], parts[1]

        if method.upper() == "CONNECT":
            host, _, port_s = target.rpartition(":")
            port = int(port_s or 443)
            log(logfile, f"CONNECT {host}:{port}")
            upstream = socket.create_connection((host, port), timeout=15)
            client.sendall(b"HTTP/1.1 200 Connection established\r\n\r\n")
            rest = head.split(b"\r\n\r\n", 1)[1]
            if rest:
                upstream.sendall(rest)
        else:
            # absolute-URI form: GET http://host/path HTTP/1.1
            if "://" not in target:
                return
            after = target.split("://", 1)[1]
            hostport = after.split("/", 1)[0]
            host, _, port_s = hostport.partition(":")
            port = int(port_s or 80)
            log(logfile, f"{method.upper()} {host}:{port}")
            upstream = socket.create_connection((host, port), timeout=15)
            upstream.sendall(head)

        client.settimeout(None)
        upstream.settimeout(None)
        t = threading.Thread(target=pipe, args=(client, upstream), daemon=True)
        t.start()
        pipe(upstream, client)
        t.join(timeout=5)
    except (OSError, ValueError) as exc:
        log(logfile, f"ERROR {exc}")
    finally:
        for s in (client, upstream):
            if s is not None:
                try:
                    s.close()
                except OSError:
                    pass


def main() -> int:
    if len(sys.argv) != 4:
        print(__doc__)
        return 2
    bind, port, logpath = sys.argv[1], int(sys.argv[2]), Path(sys.argv[3])
    logpath.write_text("")
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind((bind, port))
    srv.listen(128)
    print(f"listening on {bind}:{port}", flush=True)
    while True:
        conn, _ = srv.accept()
        threading.Thread(target=handle, args=(conn, logpath), daemon=True).start()


if __name__ == "__main__":
    sys.exit(main())
