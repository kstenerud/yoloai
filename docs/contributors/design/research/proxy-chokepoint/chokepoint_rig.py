#!/usr/bin/env python3
# ABOUTME: Shared scaffolding for P3/P4/P8 — stand up a sandbox whose only permitted destination
# ABOUTME: is a host-side proxy, and tear it down. Extracted so three harnesses measure the same
# ABOUTME: chokepoint rather than three hand-built ones that could quietly differ.

"""The rig P2 built, factored out so later items cannot accidentally test something else.

P2 established the shape: an nftables netdev ingress chain on the sandbox's own veth permitting
one address and one port, rejecting all other IPv4, with the proxy environment set at launch.
Three later items need exactly that and differ only in what they run inside it, so the setup
lives here once. A rig copied three times is three rigs.

**What this deliberately does not do:** permit DNS. P2 showed the guest never needs it, because
`CONNECT` carries a name the proxy resolves. Anything here that fails for want of resolution is
reporting a real property of a chokepoint, not a defect in the rig.

**Its own negative self-test is the caller's job.** This module can stand a chokepoint up; it
cannot know what the caller intends to claim, so every harness using it must still baseline its
own probe with the mechanism absent. `assert_choked()` exists to make the cheap half of that
easy: it returns whether an ordinary HTTPS destination is refused with the proxy unset.
"""

from __future__ import annotations

import re
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness  # noqa: E402

REPO = Path(__file__).resolve().parents[5]
YOLOAI = REPO / "yoloai"
HERE = Path(__file__).resolve().parent
PROXY_HOST = "172.17.0.1"
PROXY_PORT = 8899
PROXY_LOG = Path("/tmp/chokepoint_proxy.log")

PROXY_ENV = [
    f"HTTPS_PROXY=http://{PROXY_HOST}:{PROXY_PORT}",
    f"HTTP_PROXY=http://{PROXY_HOST}:{PROXY_PORT}",
    f"https_proxy=http://{PROXY_HOST}:{PROXY_PORT}",
    f"http_proxy=http://{PROXY_HOST}:{PROXY_PORT}",
    "NO_PROXY=localhost",  # no comma: DF195
]


def quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def teardown(box: str, table: str) -> None:
    quiet(["sudo", "nft", "delete", "table", "netdev", table])
    quiet(["sudo", "pkill", "-f", "chokepoint_proxy.py"])
    quiet([str(YOLOAI), "destroy", box, "--abandon-unapplied"])


def start_proxy() -> subprocess.Popen[bytes]:
    PROXY_LOG.write_text("")
    proc = subprocess.Popen(
        [sys.executable, str(HERE / "chokepoint_proxy.py"),
         PROXY_HOST, str(PROXY_PORT), str(PROXY_LOG)],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    time.sleep(1)
    return proc


def host_veth(h: Harness, box: str) -> str:
    iflink = h.run(["docker", "exec", f"yoloai-cli-{box}",
                    "cat", "/sys/class/net/eth0/iflink"], check=False).stdout.strip()
    if not iflink.isdigit():
        return ""
    for line in quiet(["ip", "-o", "link"]).stdout.splitlines():
        if line.startswith(f"{iflink}:"):
            return line.split(":")[1].strip().split("@")[0]
    return ""


def install_chokepoint(h: Harness, dev: str, table: str) -> bool:
    return h.run(["sudo", "nft", "-f", "-"], check=False, stdin=f"""
table netdev {table} {{
    chain c_ingress {{
        type filter hook ingress device "{dev}" priority 0; policy accept;
        ip daddr {PROXY_HOST} tcp dport {PROXY_PORT} counter accept comment "to-proxy"
        ip daddr 0.0.0.0/0 counter reject comment "chokepoint"
    }}
}}
""").returncode == 0


def counter(h: Harness, table: str, comment: str) -> int:
    out = h.run(["sudo", "nft", "list", "table", "netdev", table], check=False).stdout
    m = re.search(rf'packets (\d+).*comment "{comment}"', out)
    return int(m.group(1)) if m else 0


PROXY_PREFIX = (
    f"export HTTPS_PROXY=http://{PROXY_HOST}:{PROXY_PORT} "
    f"HTTP_PROXY=http://{PROXY_HOST}:{PROXY_PORT} "
    f"https_proxy=http://{PROXY_HOST}:{PROXY_PORT} "
    f"http_proxy=http://{PROXY_HOST}:{PROXY_PORT} NO_PROXY=localhost; "
)


def sh_proxied(h: Harness, box: str, script: str, timeout: int = 180
               ) -> subprocess.CompletedProcess[str]:
    """`sh` with the proxy exported explicitly.

    Measured, not assumed: `--env` is delivered to the AGENT'S process tree only. PID 1 does
    not carry it and neither does `yoloai exec`, so a command run through exec has no proxy
    and no resolver, and every tool would fail for want of configuration rather than for want
    of capability. That is a real property worth its own note in the results; here it is
    simply the reason this helper exists.
    """
    return sh(h, box, PROXY_PREFIX + script, timeout)


def sh(h: Harness, box: str, script: str, timeout: int = 180) -> subprocess.CompletedProcess[str]:
    """Run a shell snippet inside the sandbox, with a wall clock on it.

    Not `Harness.run`, which has no timeout: a package manager that hangs waiting for a
    resolver it will never reach would stall the whole round, and "it hung" is itself a
    result worth recording rather than a reason to lose the run.
    """
    argv = [str(YOLOAI), "exec", box, "--", "sh", "-lc", script]
    try:
        return subprocess.run(argv, capture_output=True, text=True,
                              check=False, timeout=timeout)
    except subprocess.TimeoutExpired:
        return subprocess.CompletedProcess(argv, 124, "TIMEOUT", "TIMEOUT")


def assert_choked(h: Harness, box: str) -> bool:
    """True when an ordinary HTTPS destination is refused with the proxy unset."""
    out = sh(h, box,
             "env -u https_proxy -u HTTPS_PROXY -u http_proxy -u HTTP_PROXY "
             "curl -s -o /dev/null -m 15 -w '%{http_code}' https://example.com/ || true",
             timeout=60)
    return "200" not in out.stdout and "301" not in out.stdout


def proxy_lines() -> int:
    """Request COUNT, not the set of names.

    A set delta cannot see a repeat request to a host already in it, so a control that asks
    "did anything new reach the proxy" reports False when the answer is yes-but-again. That
    voided P8 run 1, whose positive control re-fetched a host the sandbox's own agent had
    already contacted at startup.
    """
    if not PROXY_LOG.exists():
        return 0
    return sum(1 for ln in PROXY_LOG.read_text().splitlines()
               if ln.split(" ", 1)[0] in {"CONNECT", "GET", "POST", "HEAD", "PUT"})


def targets_seen() -> set[str]:
    if not PROXY_LOG.exists():
        return set()
    return {ln.split(" ", 1)[1] for ln in PROXY_LOG.read_text().splitlines()
            if ln.split(" ", 1)[0] in {"CONNECT", "GET", "POST", "HEAD", "PUT"}}
