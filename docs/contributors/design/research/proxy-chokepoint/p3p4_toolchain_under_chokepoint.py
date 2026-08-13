#!/usr/bin/env python3
# ABOUTME: P3 and P4 — does the ordinary developer toolchain work when the only route out is an
# ABOUTME: HTTP proxy? Package managers (P4) and git, including git over SSH (P3), which is the
# ABOUTME: one thing P1 saw a session reach for that no HTTP proxy carries without a tunnel.

"""P3 + P4: the toolchain, measured together because they share a rig and differ only in verb.

Two queue items, one run. P4 asks whether package managers work through a forced proxy — the
*coverage* half only, since [`agent-proxy-support.md`](../agent-proxy-support.md) already settled
that proxy environment variables are never a containment boundary. P3 asks what git over SSH
needs, and whether `CONNECT`-tunnelling it is a hole worth refusing.

**Why they belong in one run.** Both are "an ordinary tool, inside a chokepoint, with the proxy
configured". Splitting them would mean two sandboxes, two chokepoints, and two chances for the
rigs to differ; the queue defines items, not files, so they are measured together and reported
separately.

**The SSH question is sharper than it looks, and P1 is why.** A session that asked only for `npm`
and a local commit reached for `github.com:22` unbidden, twice, across two captures. So this is
not a question about power users. Three outcomes are possible and they lead to different designs:

* **git over SSH works through `CONNECT`** — then a chokepoint carries it, and the cost is that
  the proxy is now tunnelling an opaque stream it cannot inspect. That is a *policy* hole: the
  tunnel carries anything, so `CONNECT host:22` is indistinguishable from `CONNECT host:22` used
  to reach something else entirely.
* **It needs explicit configuration** (`ProxyCommand`) — then it works only for users who set it
  up, and the product would have to ship that configuration.
* **It cannot work** — then a chokepoint changes what the product can do, and that belongs in
  release notes rather than in a design document.

**The negative self-test.** Every claim here rests on one probe — an ordinary HTTPS fetch —
baselined with the proxy unset, where the chokepoint must refuse it. Without that, "npm worked"
is compatible with there being no chokepoint at all, which is precisely how P1 run 1 read 100%
proxyable from a docker build.

**Instrument boundary.** Inside the region: what each tool does with the proxy configured, and
what the proxy log records. Scaffolding: the rig, and `chokepoint_proxy.py`, which validates no
hostname and allows everything — so nothing here tests a policy, only a path.

Run it as: `python3 p3p4_toolchain_under_chokepoint.py`
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))
sys.path.insert(0, str(Path(__file__).resolve().parent))

from chokepoint_rig import (  # noqa: E402
    PROXY_ENV, PROXY_HOST, PROXY_PORT, YOLOAI, assert_choked, counter, host_veth,
    install_chokepoint, quiet, sh, sh_proxied, start_proxy, targets_seen, teardown,
)
from research_harness_v2 import Harness, HarnessError  # noqa: E402

BOX = "p34tool"
TABLE = "yb_p34"
WORKDIR = os.environ.get("P34_WORKDIR", str(Path.home() / "p34-repo"))


def main() -> int:
    h = Harness("P3+P4", "Does the ordinary developer toolchain work behind a forced proxy?")
    proxy = None
    try:
        teardown(BOX, TABLE)
        proxy = start_proxy()
        h.control("the proxy stand-in is listening", proxy.poll() is None,
                  f"{PROXY_HOST}:{PROXY_PORT}")

        started = h.run([str(YOLOAI), "run", BOX, WORKDIR,
                         "-p", "Do nothing at all. Exit immediately without using any tools.",
                         *sum((["--env", e] for e in PROXY_ENV), []), "--tty"], check=False)
        h.control("the sandbox launched via the product's own launch path",
                  started.returncode == 0, f"rc={started.returncode}")

        dev = host_veth(h, BOX)
        h.control("the sandbox's host-side veth was found", bool(dev), f"dev={dev!r}")
        h.control("the chokepoint rule loaded", install_chokepoint(h, dev, TABLE),
                  "one destination permitted, DNS deliberately not")

        choked = h.probe("the chokepoint refuses an ordinary HTTPS destination",
                         lambda: assert_choked(h, BOX))
        choked.baseline(want=True,
                        detail="proxy unset. If this reports False the chokepoint is not "
                               "installed and every success below is free")
        h.control("the deny rule actually fired", counter(h, TABLE, "chokepoint") > 0,
                  f"{counter(h, TABLE, 'chokepoint')} packets")

        # ---------------------------------------------------------------- P4
        pkg = {
            "npm": "npm install --no-audit --no-fund left-pad >/dev/null 2>&1 && "
                   "test -d node_modules/left-pad && echo YES || echo NO",
            "pip": "pip3 download --no-deps --dest /tmp/pipdl six >/dev/null 2>&1 && "
                   "echo YES || echo NO",
            "apt": "sudo apt-get update -qq >/dev/null 2>&1 && echo YES || echo NO",
            "go": "GOPROXY=https://proxy.golang.org GOFLAGS=-mod=mod GONOSUMDB='*' "
                  "GONOSUMCHECK=1 GOSUMDB=off "
                  "sh -c 'cd /tmp && rm -rf gm && mkdir gm && cd gm && "
                  "go mod init x >/dev/null 2>&1 && go get rsc.io/quote >/dev/null 2>&1' "
                  "&& echo YES || echo NO",
            "curl": "curl -s -o /dev/null -m 20 -w '%{http_code}' https://example.com/",
        }
        results: dict[str, str] = {}
        for name, script in pkg.items():
            out = sh_proxied(h, BOX, script, timeout=300).stdout.strip().splitlines()
            results[name] = out[-1] if out else "(no output)"
            h.measure(f"{name} through the proxy", results[name], arm="p4")

        worked = [k for k, v in results.items()
                  if v == "YES" or v.startswith(("200", "301"))]
        all_pkg = h.measure("every package manager tried worked through the proxy",
                            len(worked) == len(pkg),
                            f"worked: {sorted(worked)}; results: {results}", arm="p4")
        h.expect("the honest path's package managers need no coaxing beyond the standard "
                 "proxy environment, which is what a chokepoint depends on",
                 all_pkg, want=True, arm="p4",
                 unbaselined="the mechanism-absent state is the chokepoint baseline above, "
                             "where the same guest could not reach anything; re-running each "
                             "package manager with the proxy unset measures the rig, not them")

        # ---------------------------------------------------------------- P3
        h.measure("git over HTTPS through the proxy",
                  sh_proxied(h, BOX, "cd /tmp && rm -rf ghttps && "
                             "git clone -q --depth 1 https://github.com/git/git ghttps "
                             ">/dev/null 2>&1 && echo YES || echo NO",
                     timeout=300).stdout.strip().splitlines()[-1], arm="p3")

        h.measure(
            "a CONNECT-capable tool ships in the image",
            sh(h, BOX, "command -v socat ncat nc netcat corkscrew >/dev/null "
                       "&& echo YES || echo NO").stdout.strip().splitlines()[-1],
            "nc/socat/corkscrew are how every ssh-through-a-proxy recipe is written; if none "
            "is present then supporting SSH under a chokepoint means adding a package to the "
            "image, which is a product decision and not only a proxy capability",
            arm="p3")

        ssh_plain = sh_proxied(h, BOX,
                       "ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -T "
                       "git@github.com 2>&1 | head -1; echo rc=$?", timeout=90).stdout
        h.measure("plain ssh to github, no ProxyCommand", ssh_plain.strip().replace("\n", " | "),
                  "a chokepoint permits one TCP destination, so this is expected to fail; what "
                  "matters is HOW, because the failure is what a user would see", arm="p3")

        # `nc -X connect -x proxy` is the ProxyCommand form every SSH-through-a-proxy
        # recipe uses; if it works the tunnel is available, with the cost that follows.
        connect_py = (
            "import socket,sys,threading\n"
            "h,p=sys.argv[1],int(sys.argv[2])\n"
            f"s=socket.create_connection(('{PROXY_HOST}',{PROXY_PORT}),15)\n"
            "s.sendall(('CONNECT %s:%s HTTP/1.1\\r\\nHost: %s\\r\\n\\r\\n'"
            "%(h,p,h)).encode())\n"
            "b=b''\n"
            "while b'\\r\\n\\r\\n' not in b: b+=s.recv(65536)\n"
            "def up():\n"
            "    while True:\n"
            "        d=sys.stdin.buffer.read1(65536)\n"
            "        if not d: break\n"
            "        s.sendall(d)\n"
            "threading.Thread(target=up,daemon=True).start()\n"
            "while True:\n"
            "    d=s.recv(65536)\n"
            "    if not d: break\n"
            "    sys.stdout.buffer.write(d); sys.stdout.buffer.flush()\n"
        )
        sh(h, BOX, "cat > /tmp/connect.py <<'PYEOF'\n" + connect_py + "PYEOF\necho written")
        ssh_tunnel = sh_proxied(h, BOX,
                        "ssh -o StrictHostKeyChecking=no -o ConnectTimeout=15 "
                        "-o 'ProxyCommand=python3 /tmp/connect.py %h %p' "
                        "-T git@github.com 2>&1 | head -3", timeout=120).stdout
        tunnelled = h.measure(
            "ssh reaches github through a CONNECT tunnel",
            "successfully authenticated" in ssh_tunnel.lower()
            or "does not provide shell access" in ssh_tunnel.lower()
            or "permission denied" in ssh_tunnel.lower(),
            f"{ssh_tunnel.strip()[:200]!r} — 'permission denied' still proves the TCP stream "
            "reached github's sshd, which is the question; authentication is not",
            arm="p3")
        h.measure("what that costs, if it worked",
                  "the proxy is tunnelling an opaque stream" if tunnelled.value else "n/a",
                  "CONNECT host:22 is indistinguishable from CONNECT host:22 used for anything "
                  "else — the tunnel carries whatever the client puts in it, so allowing it "
                  "trades the chokepoint's closure for compatibility", arm="p3")

        seen = targets_seen()
        h.measure("distinct destinations the toolchain asked the proxy for", len(seen))
        h.measure("the destinations, in the clear", "; ".join(sorted(seen)[:20]) or "none")

        h.not_tried(
            "an allowlist. The proxy allows everything and validates no hostname; nothing here "
            "tests a policy, only a path",
            "authentication for SSH. No key is present, so 'permission denied' is the best "
            "outcome available and it is a statement about REACHABILITY, not about whether a "
            "user's clone would succeed",
            "git push, submodules, LFS, and credential helpers, all of which are ordinary git "
            "over the network and none of which is exercised",
            "private registries, .npmrc auth, pip index URLs, GOPRIVATE — every package manager "
            "here fetched a public artefact anonymously",
            "what a FAILURE looks like to a user. Each cell is YES or NO; the diagnostics a "
            "developer would actually get when a chokepoint refuses something are not captured",
            "proxy authentication, TLS interception, and custom CAs, which every corporate "
            "deployment of this shape has and this rig has none of",
            "repeat runs. n=1 per cell",
        )
        return 0 if h.report() else 1
    finally:
        if proxy is not None:
            proxy.terminate()
        teardown(BOX, TABLE)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
