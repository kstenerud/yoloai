#!/usr/bin/env python3
# ABOUTME: W8 — tart's Softnet is the only enforcement surface that backend has, and this
# ABOUTME: corpus asserts it from `--help` text alone. Does it actually deny a destination,
# ABOUTME: and is `--net-softnet-allow` the allowlist its name suggests?

"""W8: the surface the corpus described without running.

`tart-net-key.txt` established that pf cannot key on tart's per-VM interfaces at all — a
rule `on <member>` blocks nothing while the same rule on the shared bridge blocks every VM,
and OpenBSD's `received-on` is a syntax error here. Its stated consequence is that tart's
own `--net-softnet-allow`/`-block` are the only enforcement surface that backend has, and
the results README is blunt about the evidence:

> ***Softnet's allowlist is read from `--help`, never run.*** *No VM was started with a
> blocked CIDR and no destination was confirmed unreachable. If that becomes the tart answer
> it needs the same treatment every pf rule in this directory got.*

The prior-art gate then raised its importance: `cirruslabs/softnet` is a purpose-built
userspace packet filter sitting between the VM and `vmnet.framework`, doing per-VM source
MAC and IP pinning, destination CIDR allow/block with longest-prefix matching and block
precedence, and — the part that matters most — dynamic policy updates over a Unix-socket
JSON-RPC channel that clear the flow table on change. That last is `pf-no-state.txt`'s
entire five-run investigation, already solved, in a dependency this project already ships.

**And the first thing this run found is that the corpus has the flag backwards.** Booting
with `--net-softnet-allow=1.1.1.1/32` — read as an allowlist naming one permitted
destination — leaves a *different* address reachable. `--help` says why: `--allow`
*"allows you to bypass the private IPv4 address space restrictions imposed by
`--net-softnet`"*. It **widens** the default policy; it does not narrow it. The allowlist
form is `--net-softnet-block=0.0.0.0/0` relaxed by `--net-softnet-allow=<permitted>`, with
longest prefix winning and block taking precedence on a tie.

So the baseline is a VM whose destination restrictions are switched off entirely
(`--net-softnet-allow=0.0.0.0/0`, which the help says disables destination-based
restrictions), and the claim is measured against the default-deny form.

Instrument boundary: nothing is timed. Each arm is a separate VM boot, and `tart exec`
reaches the guest through the Guest Agent rather than over the network under test — which
matters, because a sampler that rode the filtered path would read a successful block as a
dead VM.

Run it as: `python3 w8_softnet_enforcement.py`
"""

from __future__ import annotations

import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

VM = "yoloai-base"
ALLOW = "1.1.1.1"
DENY = "1.0.0.1"

MODE: dict[str, list[str]] = {"flags": []}
STATE: dict[str, object] = {}


def _q(argv: list[str], timeout: int = 300) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def stop_vm() -> None:
    _q(["tart", "stop", VM, "--timeout", "20"])
    for _ in range(30):
        if "running" not in _q(["tart", "list", "--source", "local"]).stdout:
            return
        time.sleep(1)


def boot(flags: list[str]) -> bool:
    """Boot the VM with a Softnet policy and wait for the Guest Agent."""
    stop_vm()
    subprocess.Popen(
        ["tart", "run", VM, "--no-graphics", "--net-softnet", *flags],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    for _ in range(120):
        if _q(["tart", "exec", VM, "/bin/sh", "-c", "echo ok"]).stdout.strip() == "ok":
            return True
        time.sleep(2)
    return False


def guest(script: str) -> str:
    return _q(["tart", "exec", VM, "/bin/sh", "-c", script]).stdout.strip()


def probe(addr: str) -> str:
    return guest(f"/usr/bin/curl -s -o /dev/null -m 8 -w '%{{http_code}}' http://{addr}/ "
                 "2>/dev/null; echo")


def denied_reachable() -> bool:
    """THE probe. Same callable, same guest, same command; only the boot policy varies."""
    code = probe(DENY)
    STATE["denied"] = code
    return code not in ("", "000")


def main() -> int:
    h = Harness("W8", "Does tart's Softnet actually deny a destination, and is "
                      "`--net-softnet-allow` an allowlist?")
    try:
        h.require("softnet is installed", Path("/opt/homebrew/bin/softnet").exists())
        h.require("the tart VM exists", VM in _q(["tart", "list"]).stdout)

        # -- the flag-semantics finding, measured before anything rests on it ----------
        h.require("the VM boots with a naive 'allowlist'",
                  boot([f"--net-softnet-allow={ALLOW}/32"]),
                  "no Guest Agent answer within the limit")
        naive_allow = probe(ALLOW)
        naive_deny = probe(DENY)
        h.measure("`--net-softnet-allow=1.1.1.1/32` alone: allowed / other",
                  f"{naive_allow} / {naive_deny}",
                  "read as an allowlist this should refuse the second address; `--help` "
                  "says --allow WIDENS the default private-address restriction rather than "
                  "narrowing it, and the measurement agrees")
        h.control("the guest has a network at all under Softnet",
                  naive_allow not in ("", "000"),
                  f"{ALLOW} -> {naive_allow}")

        p = h.probe("the guest reaches a destination outside its Softnet policy",
                    denied_reachable)

        # -- BASELINE: destination restrictions switched off entirely ------------------
        h.require("the VM boots with restrictions disabled",
                  boot(["--net-softnet-allow=0.0.0.0/0"]))
        p.baseline(want=True,
                   detail="--net-softnet-allow=0.0.0.0/0, which `--help` says disables "
                          "destination-based restrictions")
        h.measure("baseline read", STATE.get("denied"))

        # -- SAMPLE: the real allowlist form -------------------------------------------
        h.require("the VM boots with default-deny plus one permitted address",
                  boot([f"--net-softnet-block=0.0.0.0/0",
                        f"--net-softnet-allow={ALLOW}/32"]))
        blocked = p.sample("--net-softnet-block=0.0.0.0/0 --net-softnet-allow=1.1.1.1/32")
        allowed_code = probe(ALLOW)
        h.measure("under default-deny: permitted / denied",
                  f"{allowed_code} / {STATE.get('denied')}")
        h.control("the permitted address still answers, so this is an allowlist and not an "
                  "outage", allowed_code not in ("", "000"),
                  f"{ALLOW} -> {allowed_code}")
        h.control("`tart exec` still answers, so the sampler is not riding the filtered path",
                  guest("echo ok") == "ok",
                  "the Guest Agent is a vsock channel, not the network under test")

        h.expect(
            "Softnet's default-deny form actually denies a destination on tart",
            blocked, want=False,
        )

        h.not_tried(
            "**the dynamic policy channel, which is the interesting half.** Softnet's "
            "README describes newline-delimited JSON-RPC over a Unix socket with flow-table "
            "clearing on change — which is live revocation, the thing `pf-no-state.txt` "
            "needed five runs to reach on pf. `tart run` exposes only the boot-time flags, "
            "and nothing here finds or drives that socket. Whether yoloAI could is the "
            "question that decides if tart gets revocation at all",
            "source pinning. Softnet also enforces the VM's own MAC and DHCP-assigned "
            "address, which would structurally close `pf-spoof.txt`'s S4 escape. Not "
            "exercised: no attempt was made to spoof from inside the guest",
            "the `@host` identifier the README names for gateway traffic, which is W5's "
            "problem on the tart side and is not probed",
            "UDP and DNS. Both probes are TCP/80. The guest resolves through the Softnet "
            "gateway and whether a default-deny policy costs it its resolver is exactly "
            "W1's question, unasked here",
            "more than one destination per class, and any address inside the private "
            "ranges Softnet restricts by default",
            "n. One boot per arm, one host, one tart and softnet version "
            "(tart 2.32.1, softnet 0.19.0)",
            "whether this composes with yoloAI at all. `runtime/tart` does not pass these "
            "flags today, and what it would cost to thread an allowlist through is a code "
            "question this run does not open",
        )
        return 0 if h.report() else 1
    finally:
        stop_vm()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
