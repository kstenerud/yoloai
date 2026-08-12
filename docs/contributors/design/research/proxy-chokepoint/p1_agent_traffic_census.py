#!/usr/bin/env python3
# ABOUTME: P1 — what a real agent session actually sends, by protocol and port, captured on the
# ABOUTME: docker bridge while the product's own launch path runs a real task. Sizes every other
# ABOUTME: item in the round: a proxy can only be the sole egress if it can serve what is here.

"""P1: the census, and everything downstream is sized by it.

[D137](../../../decisions/working-notes.md) trades an open-ended *policy* problem — enumerate
every destination an adversarial agent might reach — for a closed *coverage* problem: give the
guest one destination and make the proxy serve the honest path. That trade is only worth
making if the coverage is achievable, and **nobody has measured what the honest path emits.**
The corpus this workstream built is entirely about enforcement mechanism; not one run in it
asked what traffic there is to enforce on.

The classification that matters is three-way, because each bucket implies different work:

* **Proxyable as-is** — TCP 443/80. An HTTP proxy serves these with `CONNECT` or plain
  forwarding, and this is the bucket the whole direction assumes is nearly everything.
* **Served by the proxy rather than forwarded** — DNS. Under a chokepoint there is no route for
  it to travel, so the proxy resolves and the guest never does. `prior-art` §1 (Cilium) records
  the same inversion; what is unmeasured is how *much* there is.
* **The remainder** — everything else, including **QUIC**, which is UDP/443 and is *not* served
  by an HTTP `CONNECT` proxy however much it looks like HTTPS. This is the bucket that decides
  whether the direction is cheap or expensive, and no document in this repo estimates it.

**Classification is by BPF, not by reading tcpdump's decoded text.** Run 2 was voided for
exactly that: it inferred the protocol from whether the token `UDP` appeared in the decode, so
every DNS frame was labelled TCP — harmless in itself, since both land in the DNS bucket — but
the same predicate would have labelled a QUIC flow `tcp/443` and counted it as proxyable. The
one finding that would falsify the direction was the one the predicate could not express. So
each bucket is now a kernel filter applied to a `.pcap`, and the buckets are asserted to sum to
the attributable total, which is what makes a gap or an overlap visible.

**The instrument's boundary** (D136 §3). Inside the measured region: every frame on `docker0`
whose source or destination is the sandbox's own address, timestamped after the sandbox was
created. Scaffolding, excluded and named: the canary below, and any frame predating the
sandbox — run 1 attributed a *docker image build* to the sandbox because the build container
had held the same bridge address, and read 100% HTTP from it. Captured by protocol and port
only, **not by hostname**: extracting SNI needs a TLS parser this host has no tooling for, and
a census that guesses names is worse than one that says it did not look.

**The free negative this is built to avoid.** A capture that silently is not capturing produces
a clean, quiet, entirely wrong census — every "we saw no X" free, and the emptier the result the
more the direction looks confirmed. So the probe is the capture itself: a canary to a TEST-NET
address nothing else on this host would contact, baselined **absent** before the sandbox exists
and sampled **present** from inside it while the agent is mid-session.

Run it as: `python3 p1_agent_traffic_census.py` (needs passwordless sudo for tcpdump).
"""

from __future__ import annotations

import collections
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
BOX = "p1census"
BRIDGE = "docker0"
PCAP = Path("/tmp/p1_census.pcap")
CANARY = "192.0.2.123"  # RFC 5737 TEST-NET-1: routable nowhere, contacted by nothing
CANARY_PORT = 9

# NOT under /tmp/claude-<uid>: yoloAI re-creates the workdir's absolute path inside the
# container and docker makes the parents root-owned, so a workdir there leaves the agent
# refusing its own temp directory. Run 1 died that way in 2 seconds and censused a build.
WORKDIR = os.environ.get("P1_WORKDIR", str(Path.home() / "p1census-repo"))

TASK = (
    "Set up a tiny Node project in this repository: run `npm init -y`, install the "
    "`left-pad` package with npm, write index.js that requires it and prints "
    "leftPad('x', 5, '-'), run it with node to confirm it works, then git add and "
    "git commit everything. Keep it minimal and do not ask me questions."
)

HTTP_PORTS = "(port 443 or port 80 or port 8080 or port 8443)"
PORTLINE = re.compile(r"^\d+\.\d+ IP6? [0-9a-f.:]+\.(\d+) > ([0-9a-f.:]+)\.(\d+):")


def _quiet(argv: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False)


def cleanup() -> None:
    _quiet(["sudo", "pkill", "-f", f"tcpdump -i {BRIDGE}"])
    _quiet([str(YOLOAI), "destroy", BOX, "--abandon-unapplied"])


def start_capture() -> subprocess.Popen[bytes]:
    """`-U` so frames reach the file as they arrive; a buffered capture reads as silence."""
    _quiet(["sudo", "rm", "-f", str(PCAP)])
    return subprocess.Popen(
        ["sudo", "tcpdump", "-i", BRIDGE, "-nn", "-U", "-s", "0", "-w", str(PCAP)],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )


def read_pcap(expr: str) -> list[str]:
    out = _quiet(["sudo", "tcpdump", "-r", str(PCAP), "-nn", "-tt", expr])
    return [ln for ln in out.stdout.splitlines() if ln and ln[0].isdigit()]


def count(expr: str) -> int:
    return len(read_pcap(expr))


def capture_alive(proc: subprocess.Popen[bytes]) -> bool:
    return proc.poll() is None


def canary_seen() -> bool:
    return PCAP.exists() and count(f"host {CANARY}") > 0


def box_ip(h: Harness) -> str:
    out = h.run(
        ["docker", "inspect", "-f",
         "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
         f"yoloai-cli-{BOX}"],
        check=False,
    ).stdout
    return str(out).strip()


def main() -> int:
    h = Harness("P1", "What does a real agent session actually send, by protocol and port?")
    proc = None
    try:
        cleanup()
        proc = start_capture()
        time.sleep(2)
        h.control("the capture process is running", capture_alive(proc),
                  "a capture that never started reports silence, and silence reads as coverage")

        canary = h.probe("the canary destination appears in the capture", canary_seen)
        canary.baseline(want=False,
                        detail="capture running, no sandbox yet — nothing on this host "
                               "contacts TEST-NET-1")

        # -- the product's own launch path, not a hand-built container (A37) -----
        t0 = time.time()
        started = h.run([str(YOLOAI), "run", BOX, WORKDIR, "-p", TASK], check=False)
        h.control("the sandbox launched via the product's own `yoloai run`",
                  started.returncode == 0, f"rc={started.returncode}")

        ip = box_ip(h)
        h.control("the sandbox has an address on the bridge", bool(ip), f"ip={ip!r}")

        canary_run = h.run(
            [str(YOLOAI), "exec", BOX, "--",
             "curl", "-s", "-m", "4", f"http://{CANARY}:{CANARY_PORT}/"], check=False)
        h.control("the canary command itself ran inside the sandbox",
                  canary_run.returncode in (0, 7, 28),
                  f"rc={canary_run.returncode} — 0/7/28 all emit packets. The `--` matters: "
                  "without it the CLI eats curl's own short flags and the probe never runs")
        time.sleep(2)
        h.expect("the capture sees traffic the sandbox emits, so a quiet census is a fact "
                 "rather than a broken instrument",
                 canary.sample("after a canary from inside the sandbox"), want=True)

        waited = h.run([str(YOLOAI), "wait", BOX], check=False)
        h.measure("the agent's own exit outcome", waited.returncode,
                  "recorded, not asserted — but see the next control")
        time.sleep(2)

        diff = h.run([str(YOLOAI), "diff", BOX], check=False).stdout
        h.control("the agent actually did the task, so this is a session and not a crash",
                  "package.json" in diff,
                  "run 1's agent died at startup in 2 s and its census was a docker build")

        # -- buckets are kernel filters, and they must account for every frame ---
        mine = f"host {ip}"
        attributed = count(mine)
        fresh = [ln for ln in read_pcap(mine) if float(ln.split(" ", 1)[0]) >= t0]
        stale = attributed - len(fresh)
        h.measure("frames discarded as pre-dating the sandbox", stale,
                  "an earlier container can hold the same bridge address; run 1 censused a "
                  "docker image build this way and read 100% HTTP from it")
        h.control("frames attributable to the sandbox were captured", len(fresh) > 0,
                  f"{len(fresh)} frames")

        scaffold = count(f"{mine} and host {CANARY}")
        http = count(f"{mine} and tcp and {HTTP_PORTS}")
        dns = count(f"{mine} and port 53")
        quic = count(f"{mine} and udp and port 443")
        rest = count(f"{mine} and not (tcp and {HTTP_PORTS}) and not port 53 "
                     f"and not host {CANARY}")

        h.control("every attributable frame lands in exactly one bucket",
                  http + dns + rest + scaffold == attributed,
                  f"{http}+{dns}+{rest}+{scaffold} vs {attributed} — a gap means a protocol "
                  "no filter names, an overlap means one counted twice")
        h.control("no other container's traffic is on this bridge",
                  count(f"net 172.17.0.0/16 and not {mine} and not arp") == 0,
                  "attribution by address is only sound if nothing else is speaking")

        for name, n in (("proxyable-http", http), ("dns-served-by-proxy", dns),
                        ("remainder", rest)):
            h.measure(f"frames classified {name}", n,
                      f"{100 * n / max(1, attributed):.1f}% of attributed frames")
        h.measure("QUIC (UDP/443) frames", quic,
                  "counted separately because it LOOKS like HTTPS and is not served by an "
                  "HTTP CONNECT proxy — the finding that would falsify the direction, and the "
                  "one run 2's text-parsing predicate could not have expressed")

        ports: collections.Counter[str] = collections.Counter()
        for ln in fresh:
            m = PORTLINE.match(ln)
            if m:
                sport, dst, dport = m.groups()
                ports[dport if dst != ip else sport] += 1
        h.measure("destination ports seen, most frequent first",
                  ", ".join(f"{p}={n}" for p, n in ports.most_common(12)),
                  "the raw distribution, because a bucket is an interpretation and this is not")
        h.measure("the non-HTTP, non-DNS remainder, as tcpdump decodes it",
                  "; ".join(sorted({ln.split(": ", 1)[-1][:40] for ln in read_pcap(
                      f"{mine} and not (tcp and {HTTP_PORTS}) and not port 53 "
                      f"and not host {CANARY}")})[:6]) or "none",
                  "named rather than counted, because what it IS decides the work")

        share = (http + dns) / max(1, attributed)
        covered = h.measure(
            "HTTP plus DNS accounts for at least 95% of what the agent emitted",
            share >= 0.95,
            f"{100 * share:.1f}% — the threshold is stated in advance so the number is not "
            "read as whatever it turns out to be",
        )
        h.expect(
            "a host-side proxy can serve substantially all of a real agent session, which is "
            "what D137's trade assumes and no run in this corpus had checked",
            covered, want=True,
            unbaselined="a census has no mechanism-absent state; the instrument is baselined "
                        "instead, via the canary above, which is the discriminating control "
                        "this claim actually rests on",
        )

        h.not_tried(
            "HOSTNAMES. Ports and protocols only — extracting SNI needs a TLS parser this host "
            "has no tooling for. The closure question D137 §2 poses needs them and is NOT "
            "answered here",
            "other agents. One Claude session; P8 asks whether Codex and Aider work at all "
            "under a chokepoint, and prior art says their proxy handling is unreliable",
            "other tasks. One npm-flavoured task; a Go build, a docker-in-docker workload or a "
            "task with MCP servers would emit different things",
            "the setup phase. Profile setup commands run before the agent and are deliberately "
            "outside isolation today; frames from that window are inside this region and are "
            "not separated out",
            "IPv6 egress. This network gives the guest no routable v6, so absence is a "
            "property of the network and not a finding",
            "WHY each flow happened. The capture says a destination was contacted, not which "
            "tool contacted it or whether the session would have failed without it",
            "repeat runs. n=1 session; an agent's traffic is not deterministic and proportions "
            "should be read as an order of magnitude",
        )
        return 0 if h.report() else 1
    finally:
        if proc is not None:
            _quiet(["sudo", "pkill", "-f", f"tcpdump -i {BRIDGE}"])
        cleanup()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
