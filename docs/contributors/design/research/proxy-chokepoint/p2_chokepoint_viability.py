#!/usr/bin/env python3
# ABOUTME: P2 — can a sandbox whose only destination is a host-side proxy still do the work?
# ABOUTME: The falsification test for the whole direction, plus the hostname list P1 could not
# ABOUTME: capture, because a proxy is told the name in the clear before any TLS starts.

"""P2: the falsification test.

P1 established that 99.2% of an agent session is HTTP or DNS, which says a proxy *could* serve
it. That is a statement about protocol shape, not about whether anything still works — a
client can speak HTTPS and still fail when its only route is a proxy, because it resolves for
itself, or ignores the proxy variables, or wants a protocol the proxy cannot carry. This asks
the question P1 cannot: **with the guest holding exactly one destination, does the work
complete?**

**Two arms, because they cost very different amounts and answer different things.**

* **Arm `reach`** is cheap and carries the negative self-test. A `curl` from inside the sandbox
  to an ordinary HTTPS destination, with the chokepoint installed. Baselined **with no proxy
  configured**, where it must FAIL — otherwise the chokepoint is not a chokepoint and every
  later success is free. Then sampled with the proxy configured, where it must succeed.
* **Arm `work`** is expensive and is the actual claim: a real agent session, launched by the
  product, doing a task that needs the network — under the same chokepoint.

**Which fork this uses, and why the answer transfers to the other.** The chokepoint here is
shape **(A)**: a normal guest stack with an nftables netdev rule on the sandbox's own veth
permitting one address and rejecting everything else. Shape (B) — no IP stack at all — would
produce the *same* traffic at the proxy, because what reaches the proxy is decided by the
client's proxy configuration, not by how the route was removed. So coverage is fork-independent
and is measured on whichever is cheap to construct. (A) is cheap here because round 2 already
measured the netdev key; (B) needs a socket channel that does not exist yet.

**What the proxy log buys.** P1's `not_tried` names hostnames as its largest gap: a capture
would need a TLS parser to recover them. A proxy does not — `CONNECT api.anthropic.com:443`
arrives in the clear before TLS begins. So this run also produces the destination list, which is
what the closure question in [D137](../../../decisions/working-notes.md) §2 needs and which no
run in this workstream has ever produced.

**The instrument's boundary.** Inside the region: the sandbox's egress, the proxy's log, and the
agent's own exit. Scaffolding, named: `chokepoint_proxy.py`, which allows everything and
validates no hostname — deliberately, so that no result here can be read as having tested an
allowlist. DNS is *not* permitted through the chokepoint, so a guest that resolves for itself
fails; that is intentional and is P6's question answered in passing.

Run it as: `python3 p2_chokepoint_viability.py` (needs passwordless sudo for nft).
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

REPO = Path(__file__).resolve().parents[5]
YOLOAI = REPO / "yoloai"
HERE = Path(__file__).resolve().parent
BOX = "p2choke"
TABLE = "yb_p2"
PROXY_HOST = "172.17.0.1"  # the bridge gateway: the guest's only permitted destination
PROXY_PORT = 8899
PROXY_LOG = Path("/tmp/p2_proxy.log")
PROBE_URL = "https://example.com/"
WORKDIR = os.environ.get("P2_WORKDIR", str(Path.home() / "p2choke-repo"))

TASK = (
    "In this repository, run `npm init -y`, then install the `left-pad` package with npm, "
    "then write index.js that requires it and prints leftPad('x', 5, '-'), run it with node "
    "to check it works, and finally git add and git commit everything. Do not ask questions."
)


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["sudo", "nft", "delete", "table", "netdev", TABLE])
    _quiet(["sudo", "pkill", "-f", "chokepoint_proxy.py"])
    _quiet([str(YOLOAI), "destroy", BOX, "--abandon-unapplied"])


def host_veth(h: Harness) -> str:
    """The host end of the sandbox's veth pair, found the way the design would find it."""
    iflink = h.run(["docker", "exec", f"yoloai-cli-{BOX}",
                    "cat", "/sys/class/net/eth0/iflink"], check=False).stdout.strip()
    if not iflink.isdigit():
        return ""
    out = _quiet(["ip", "-o", "link"]).stdout
    for line in out.splitlines():
        if line.startswith(f"{iflink}:"):
            return line.split(":")[1].strip().split("@")[0]
    return ""


def install_chokepoint(h: Harness, dev: str) -> bool:
    """One destination permitted, everything else rejected. No DNS, deliberately."""
    return h.run(["sudo", "nft", "-f", "-"], check=False, stdin=f"""
table netdev {TABLE} {{
    chain c_ingress {{
        type filter hook ingress device "{dev}" priority 0; policy accept;
        ip daddr {PROXY_HOST} tcp dport {PROXY_PORT} counter accept comment "to-proxy"
        ip daddr 0.0.0.0/0 counter reject comment "chokepoint"
    }}
}}
""").returncode == 0


def reaches(h: Harness, with_proxy: bool) -> bool:
    env = ["env", f"https_proxy=http://{PROXY_HOST}:{PROXY_PORT}",
           f"http_proxy=http://{PROXY_HOST}:{PROXY_PORT}"] if with_proxy else ["env", "-u",
                                                                              "https_proxy"]
    rc = h.run([str(YOLOAI), "exec", BOX, "--", *env,
                "curl", "-s", "-o", "/dev/null", "-m", "20", "-w", "%{http_code}",
                PROBE_URL], check=False)
    return "200" in rc.stdout or "301" in rc.stdout


def counter(h: Harness, comment: str) -> int:
    out = h.run(["sudo", "nft", "list", "table", "netdev", TABLE], check=False).stdout
    m = re.search(rf'packets (\d+).*comment "{comment}"', out)
    return int(m.group(1)) if m else 0


def main() -> int:
    h = Harness("P2", "With one destination and a host-side proxy, does the work still complete?")
    proxy = None
    try:
        cleanup()
        PROXY_LOG.write_text("")
        proxy = subprocess.Popen(
            [sys.executable, str(HERE / "chokepoint_proxy.py"),
             PROXY_HOST, str(PROXY_PORT), str(PROXY_LOG)],
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        )
        time.sleep(1)
        h.control("the proxy stand-in is listening", proxy.poll() is None,
                  f"{PROXY_HOST}:{PROXY_PORT} — if it died, every failure below is free")

        # -- arm `reach`: the cheap arm, and the one that carries the negative --
        env = [f"HTTPS_PROXY=http://{PROXY_HOST}:{PROXY_PORT}",
               f"HTTP_PROXY=http://{PROXY_HOST}:{PROXY_PORT}",
               f"https_proxy=http://{PROXY_HOST}:{PROXY_PORT}",
               f"http_proxy=http://{PROXY_HOST}:{PROXY_PORT}",
               # NOT "localhost,127.0.0.1": `--env` on `new`/`run` comma-splits its value
               # and rejects the fragment (DF195). Working around it, and recorded there.
               "NO_PROXY=localhost"]
        started = h.run([str(YOLOAI), "run", BOX, WORKDIR, "-p", TASK,
                         *sum((["--env", e] for e in env), []), "--tty"], check=False)
        h.control("the sandbox launched via the product's own launch path",
                  started.returncode == 0, f"rc={started.returncode} {started.stderr[-300:]}")

        dev = host_veth(h)
        h.control("the sandbox's host-side veth was found", bool(dev), f"dev={dev!r}")
        h.control("the chokepoint rule loaded", install_chokepoint(h, dev),
                  "one destination permitted; DNS is NOT, so a guest that resolves for itself "
                  "fails — which is P6's question answered in passing")

        # ONE probe, one callable, both answers. The mechanism under test is "the proxy is
        # the route"; the state flag is what makes it absent, so the baseline and the sample
        # come from the same code path rather than from two functions that could differ.
        state = {"proxy": False}
        egress = h.probe("the sandbox reaches an ordinary HTTPS destination",
                         lambda: reaches(h, with_proxy=state["proxy"]))
        egress.baseline(want=False,
                        detail="chokepoint installed, no proxy configured. If this SUCCEEDS "
                               "the chokepoint is not a chokepoint and every result below "
                               "is free")
        h.control("the chokepoint's deny rule actually fired", counter(h, "chokepoint") > 0,
                  f"{counter(h, 'chokepoint')} packets — a baseline that failed for some other "
                  "reason would look identical")

        state["proxy"] = True
        h.expect("configuring the proxy restores reachability through the chokepoint",
                 egress.sample("with the proxy configured"), want=True, arm="reach")
        h.control("traffic to the proxy was permitted by the accept rule",
                  counter(h, "to-proxy") > 0, f"{counter(h, 'to-proxy')} packets", arm="reach")

        # -- arm `work`: the expensive arm, and the actual claim ------------------
        waited = h.run([str(YOLOAI), "wait", BOX], check=False)
        diff = h.run([str(YOLOAI), "diff", BOX], check=False).stdout
        did_work = h.measure("the agent completed the task under the chokepoint",
                             "package.json" in diff and "index.js" in diff,
                             f"agent exit={waited.returncode}; diff mentions "
                             f"package.json={'yes' if 'package.json' in diff else 'NO'}, "
                             f"index.js={'yes' if 'index.js' in diff else 'NO'}",
                             arm="work")
        h.expect("a real agent session completes its work with one destination and a proxy, "
                 "which is what D137's whole direction assumes",
                 did_work, want=True, arm="work",
                 unbaselined="the mechanism-absent state is arm `reach`'s baseline, where the "
                             "same chokepoint refused the same guest; re-running a full agent "
                             "session with no proxy to watch it fail costs an LLM run to "
                             "observe what the cheap arm already showed")

        # -- what P1 could not see: the names, in the clear ---------------------
        log = PROXY_LOG.read_text().splitlines()
        targets = sorted({ln.split(" ", 1)[1] for ln in log
                          if ln.startswith(("CONNECT", "GET", "POST", "HEAD", "PUT"))})
        h.control("the proxy log is non-empty, so the destination list is evidence",
                  bool(targets), f"{len(log)} lines")
        h.control(
            "the agent's OWN LLM traffic went through the proxy",
            any("anthropic" in t for t in targets),
            "this is what makes the work arm a statement about a chokepoint rather than about "
            "a sandbox that finished before the rule landed — the chokepoint is installed "
            "after launch, so without this control a late rule and a working proxy are "
            "indistinguishable",
            arm="work")
        h.control(
            "the package manager's traffic went through the proxy too",
            any("npm" in t or "registry" in t for t in targets),
            "the task's network-dependent step; without it the session could have completed "
            "from cache and proved nothing about coverage",
            arm="work")
        h.measure("distinct destinations the session asked for", len(targets))
        h.measure("the destinations, in the clear", "; ".join(targets[:20]) or "none",
                  "a proxy is TOLD the name in the CONNECT line before TLS starts, which is why "
                  "this run answers what P1's capture structurally could not")
        h.measure("proxy errors", sum(1 for ln in log if ln.startswith("ERROR")),
                  "; ".join(sorted({ln for ln in log if ln.startswith("ERROR")})[:4]) or "none")

        h.not_tried(
            "shape (B). The chokepoint here is an nftables rule over a normal guest stack; a "
            "guest with NO stack reaching a socket would present the same traffic to the proxy, "
            "which is why coverage is measured on whichever is cheap — but that is an argument, "
            "not a measurement",
            "an ALLOWLIST. The proxy stand-in allows everything and validates no hostname, "
            "deliberately, so that nothing here can be read as having tested a policy. The "
            "parser-differential class egress-proxy-build.md names is untouched",
            "UDP of any kind. An HTTP proxy carries TCP streams; P1 measured zero QUIC in this "
            "session, which makes this a fair rig for THIS traffic and not for a client that "
            "would use QUIC",
            "git over SSH, which P1 saw attempted unasked and which no HTTP proxy carries "
            "without a CONNECT tunnel — that is P3",
            "other agents. Claude honours proxy env by documentation; prior art says Codex "
            "disables it inside its own sandbox and Aider needs a flag, which is P8",
            "failure MODES. This measures that the work completes, not what a user sees when it "
            "does not, and a chokepoint's failures are silent in a way a filter's are not",
            "repeat runs, and one task. n=1 on both arms",
        )
        return 0 if h.report() else 1
    finally:
        if proxy is not None:
            proxy.terminate()
        cleanup()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
