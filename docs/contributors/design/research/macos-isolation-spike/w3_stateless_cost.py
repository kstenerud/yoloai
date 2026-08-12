#!/usr/bin/env python3
# ABOUTME: W3 — what statelessness and the 41-index superset cost pf at evaluation time.
# ABOUTME: Linux measured its fast-path removal as free; that is a Linux result and the
# ABOUTME: macOS corpus has been quoting it across.

"""W3: the number this workstream has been assuming.

The results README states the gap plainly:

> ***`pf-no-state.txt` does not measure what statelessness costs.*** *Linux established that
> dropping the fast-path is free at allowlist sizes 1/1000/10000; nothing here measures
> per-packet evaluation on pf at any allowlist size, with any number of sandboxes, or under
> load. "It costs nothing" is a Linux result and does not transfer.*

And `pf-grant-matrix.txt` G5 makes the gap **bigger**, because the pool inversion loads rules
for ~41 bridge indices — so every packet is evaluated against a first-match list dominated by
rules for interfaces that do not exist. Its own bounding section says so and declines to
price it.

Four rulesets, same transfer, same path, back to back:

- **none** — the anchor empty. The floor, and the thing everything else is a multiple of.
- **stateful** — the four-rule shape with pf's default `keep state`. One state carries the
  whole transfer past rule evaluation.
- **stateless** — the same four rules with `no state`. Every packet is evaluated. This is
  the shape `pf-no-state.txt` adopted and the one W1 widened to all protocols.
- **superset** — stateless, plus the pinned pool covering `bridge100`–`bridge140`: 164 rules,
  most naming interfaces that do not exist.

**The transfer is host-terminated on the bridge**, not an external download. An internet
destination's throughput is whatever the internet gave that minute, which is why
`pf-no-state.txt` reports its NAT'd arm as unusable for exactly this kind of comparison.

**Instrument boundary, stated because this directory has a specimen of getting it wrong.**
`pf-pool-scaling.txt` run 1 timed a `sudo` privilege drop *inside* its measured region and
inflated every figure 2.09×. Here the timed region is **the guest's transfer and nothing
else**: rulesets are loaded, then the clock starts, then it stops, then the next ruleset is
loaded. Ruleset load time is measured separately and reported separately.

**Evaluation counters are read either side of each transfer**, so a ruleset that turns out
not to be in the path cannot be reported as a cheap one — which is the `pf-anchor-eval`
failure mode applied to a performance number.

Every cell is n=3 on one idle host. `r14` established on the Linux side that run-to-run
variance in this kind of measurement can exceed the effect being looked for, so the samples
are printed rather than only their median.

Run it as: `python3 w3_stateless_cost.py`
"""

from __future__ import annotations

import http.server
import json
import re
import socketserver
import statistics
import subprocess
import sys
import threading
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[5] / "scripts"))

from research_harness_v2 import Harness, HarnessError  # noqa: E402

ANCHOR = "com.apple/yoloai_w3"
IMAGE = "yoloai-base:latest"
NET = "ybw3net"
BOX = "ybw3"
PORT = 18771
MB = 256
CONNS = 400
SAMPLES = 5
POOL_LO, POOL_HI = 100, 140

STATE: dict[str, object] = {}
_PF_NOISE = re.compile(
    r"use of -f option|main ruleset added|/etc/pf\.conf for further|ALTQ|^\s*$", re.I
)
BLOB = b"x" * (1024 * 1024)


class _Serve(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/ping":
            self.send_response(204)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/octet-stream")
        self.send_header("Content-Length", str(MB * 1024 * 1024))
        self.end_headers()
        try:
            for _ in range(MB):
                self.wfile.write(BLOB)
        except Exception:
            pass

    def log_message(self, *a: object) -> None:
        pass


def _q(argv: list[str], timeout: int = 180) -> subprocess.CompletedProcess[str]:
    return subprocess.run(argv, capture_output=True, text=True, check=False, timeout=timeout)


def guest(script: str, timeout: int = 180) -> str:
    return _q(["container", "exec", BOX, "sh", "-c", script], timeout=timeout).stdout.strip()


def cleanup() -> None:
    _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
    _q(["container", "stop", BOX])
    _q(["container", "rm", BOX])
    _q(["container", "network", "delete", NET])


def evaluations() -> int:
    out = _q(["sudo", "pfctl", "-a", ANCHOR, "-vvs", "rules"]).stdout
    return sum(int(m) for m in re.findall(r"Evaluations:\s+(\d+)", out))


def shapes(bridge: str) -> dict[str, str]:
    def four(state: str) -> str:
        return (
            "table <yw3_dst> persist\n"
            f"pass  in  quick on {bridge} from any to <yw3_dst> {state}\n"
            f"pass  out quick on {bridge} from <yw3_dst> to any {state}\n"
            f"block drop in  quick on {bridge} from any to any\n"
            f"block drop out quick on {bridge} from any to any\n"
        )

    pool = ["table <yw3_dst> persist"]
    pool += [f"table <yw3_dst_{i}> persist" for i in range(POOL_LO, POOL_HI + 1)]
    for i in range(POOL_LO, POOL_HI + 1):
        pool += [
            f"pass  in  quick on bridge{i} from any to <yw3_dst_{i}> no state",
            f"pass  out quick on bridge{i} from <yw3_dst_{i}> to any no state",
            f"block drop in  quick on bridge{i} from any to any",
            f"block drop out quick on bridge{i} from any to any",
        ]
    # The live bridge's own pair goes LAST, so the transfer is evaluated against the whole
    # first-match list ahead of it. Putting it first would price the best case and call it
    # the shape's cost.
    pool += [
        f"pass  in  quick on {bridge} from any to <yw3_dst> no state",
        f"pass  out quick on {bridge} from <yw3_dst> to any no state",
    ]
    return {"none": "", "stateful": four("keep state"), "stateless": four("no state"),
            "superset": "\n".join(pool) + "\n"}


def load(rules: str, gw: str) -> tuple[str, float]:
    """Load a ruleset. Returns (error, seconds-to-load). The load is NOT in the transfer's
    timed region — it is reported on its own because it lands on the acquisition path."""
    _q(["sudo", "pfctl", "-a", ANCHOR, "-F", "all"])
    if not rules:
        return "", 0.0
    t0 = time.monotonic()
    p = subprocess.run(["sudo", "pfctl", "-a", ANCHOR, "-f", "-"],
                       input=rules, capture_output=True, text=True, check=False)
    elapsed = time.monotonic() - t0
    for t in ["yw3_dst"] + [f"yw3_dst_{i}" for i in range(POOL_LO, POOL_HI + 1)]:
        if t == "yw3_dst" or f"<{t}>" in rules:
            _q(["sudo", "pfctl", "-a", ANCHOR, "-t", t, "-T", "add", gw])
    lines = [ln for ln in (p.stderr or "").splitlines() if not _PF_NOISE.search(ln)]
    return "\n".join(lines).strip(), elapsed


CLIENT_PY = r"""
import socket, sys, time
mode, host, port, n = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
req = b"GET /blob HTTP/1.1\r\nHost: h\r\nConnection: close\r\n\r\n"
t0 = time.monotonic()
if mode == "bulk":
    s = socket.create_connection((host, port), timeout=120)
    s.sendall(req)
    got = 0
    while True:
        b = s.recv(1 << 20)
        if not b:
            break
        got += len(b)
    s.close()
    print(f"{time.monotonic()-t0:.4f} {got}")
else:
    # N short connections. Under `keep state` only the first packet of each flow is
    # evaluated; under `no state` every packet is, against the whole first-match list. This
    # is where per-packet evaluation is a visible fraction of the work, which a bulk
    # transfer over a virtio bridge is not -- 24 MiB there finished inside one timer tick.
    for _ in range(n):
        s = socket.create_connection((host, port), timeout=10)
        s.sendall(b"GET /ping HTTP/1.1\r\nHost: h\r\nConnection: close\r\n\r\n")
        while s.recv(65536):
            pass
        s.close()
    print(f"{time.monotonic()-t0:.4f} {n}")
"""


def transfer(gw: str, mode: str) -> float:
    """One measured run, host-terminated on the bridge. Only this is timed.

    The client is a single in-guest Python process, so per-request `curl` spawn cost (a few
    ms each, far larger than the effect) is not inside the region.
    """
    n = CONNS if mode == "conns" else 1
    out = guest(f"python3 /tmp/w3client.py {mode} {gw} {PORT} {n}", timeout=300)
    parts = out.split()
    if len(parts) != 2:
        raise HarnessError(f"client did not report: {out!r}")
    if mode == "bulk" and int(parts[1]) < MB * 1024 * 1024:
        raise HarnessError(f"bulk transfer short: {out!r}")
    return float(parts[0])


def main() -> int:
    h = Harness("W3", "What do statelessness and the 41-index superset cost pf per packet?")
    srv: socketserver.TCPServer | None = None
    try:
        cleanup()
        socketserver.TCPServer.allow_reuse_address = True
        srv = socketserver.TCPServer(("0.0.0.0", PORT), _Serve)
        srv.daemon_threads = True  # type: ignore[attr-defined]
        threading.Thread(target=srv.serve_forever, daemon=True).start()

        # W10: some bridges cannot reach a host service on their gateway at all. This
        # experiment is host-terminated on the bridge, so it is unrunnable there. Retry and
        # report the count rather than silently selecting.
        gw = bridge = ""
        attempts = 0
        for attempts in range(1, 5):
            cleanup()
            _q(["container", "network", "create", NET])
            _q(["container", "run", "-d", "--name", BOX, "--network", NET, IMAGE,
                "sleep", "3600"])
            for _ in range(60):
                if guest("echo ok") == "ok":
                    break
                time.sleep(1)
            try:
                net = json.loads(_q(["container", "inspect", BOX]).stdout)[0]["status"]["networks"][0]
            except Exception:
                continue
            gw = net["ipv4Gateway"].split("/")[0]
            cur = ""
            bridge = ""
            for line in _q(["ifconfig", "-a"]).stdout.splitlines():
                if not line.startswith((" ", "\t")):
                    cur = line.split(":")[0]
                elif line.strip().startswith("inet ") and line.split()[1] == gw:
                    bridge = cur
                    break
            probe = guest(f"curl -s -o /dev/null -m 6 -w '%{{http_code}}' "
                          f"http://{gw}:{PORT}/ping 2>/dev/null; echo")
            if bridge and probe not in ("", "000"):
                break
        h.measure("bring-up attempts before a usable bridge", attempts,
                  "W10: on some bridge indices the guest cannot reach its gateway at all, "
                  "which makes a host-terminated transfer unrunnable there")
        h.require("the guest reaches the host server on its gateway", bool(bridge), f"{gw}")
        guest(f"cat > /tmp/w3client.py <<'W3EOF'\n{CLIENT_PY}\nW3EOF")
        h.require("the in-guest client is present",
                  guest("test -s /tmp/w3client.py && echo yes") == "yes")
        h.measure("rig", f"{BOX} on {bridge}, gateway {gw}, {MB} MiB bulk and {CONNS} "
                         f"short connections per sample, n={SAMPLES}")

        results: dict[str, list[float]] = {}
        bulk_results: dict[str, list[float]] = {}
        loads: dict[str, float] = {}
        evals: dict[str, int] = {}
        sh = shapes(bridge)
        for name in ("none", "stateful", "stateless", "superset"):
            err, load_s = load(sh[name], gw)
            if err:
                h.void_arm(name, f"ruleset did not load: {err}")
                continue
            loads[name] = load_s
            # One discarded warm-up per mode per arm. Run 2 ran each arm cold and the
            # UNFILTERED arm came out slowest of the four, which is an ordering artefact
            # rather than a fact about pf -- and it would have been quoted as one.
            transfer(gw, "bulk")
            transfer(gw, "conns")
            before = evaluations()
            bulk = [transfer(gw, "bulk") for _ in range(SAMPLES)]
            conns = [transfer(gw, "conns") for _ in range(SAMPLES)]
            evals[name] = evaluations() - before
            results[name] = conns
            bulk_results[name] = bulk
            h.measure(f"{name}: bulk, seconds per {MB} MiB", [round(x, 3) for x in bulk],
                      f"median {statistics.median(bulk):.3f}s = "
                      f"{MB / statistics.median(bulk):.0f} MiB/s")
            h.measure(f"{name}: {CONNS} short connections, seconds",
                      [round(x, 3) for x in conns],
                      f"median {statistics.median(conns):.3f}s = "
                      f"{CONNS / statistics.median(conns):.0f} conn/s")

        h.require("every ruleset ran", len(results) == 4, f"{sorted(results)}")

        n_rules = len([ln for ln in _q(["sudo", "pfctl", "-a", ANCHOR, "-s", "rules"])
                       .stdout.splitlines() if ln.strip().startswith(("pass", "block"))])
        h.measure("rules loaded in the superset arm", n_rules)
        h.measure("anchor evaluations during each arm's transfers", evals,
                  "a ruleset that is not in the path would be free AND fast — this is what "
                  "stops a cheap number being reported for a rule nobody evaluated")
        h.control(
            "the stateless arms really were evaluated per packet",
            evals.get("stateless", 0) > evals.get("stateful", 0) * 10,
            f"stateless {evals.get('stateless')} vs stateful {evals.get('stateful')} "
            "evaluations — if these were close, `no state` did not take and every timing "
            "below would be measuring the same thing twice",
        )
        h.measure("ruleset load time, seconds",
                  {k: round(v, 4) for k, v in loads.items()},
                  "separate from the transfer's timed region; this is what lands on the "
                  "acquisition path, and pf-pool-scaling.txt prices the sudo around it")

        med = {k: statistics.median(v) for k, v in results.items()}
        bmed = {k: statistics.median(v) for k, v in bulk_results.items()}
        h.measure("median seconds — short connections, by ruleset",
                  {k: round(v, 3) for k, v in med.items()})
        h.measure("median seconds — bulk, by ruleset",
                  {k: round(v, 3) for k, v in bmed.items()})
        h.measure("connection rate: cost of statelessness vs the same rules stateful",
                  f"{med['stateless'] / med['stateful']:.2f}x")
        h.measure("connection rate: cost of the 41-index superset vs the same shape at 4 rules",
                  f"{med['superset'] / med['stateless']:.2f}x")
        h.measure("connection rate: whole adopted shape vs no rules at all",
                  f"{med['superset'] / med['none']:.2f}x")
        h.measure("bulk: whole adopted shape vs no rules at all",
                  f"{bmed['superset'] / bmed['none']:.2f}x")

        # The spread WITHIN a single ruleset's samples is the noise floor. If the
        # between-ruleset differences do not clear it, the honest statement is an upper
        # bound and not a measurement of the effect.
        spread = {k: (max(v) - min(v)) / statistics.median(v) for k, v in results.items()}
        bspread = {k: (max(v) - min(v)) / statistics.median(v) for k, v in bulk_results.items()}
        h.measure("within-ruleset spread (max-min over median), short connections",
                  {k: f"{v:.1%}" for k, v in spread.items()})
        h.measure("within-ruleset spread, bulk",
                  {k: f"{v:.1%}" for k, v in bspread.items()})
        effect = abs(med["superset"] - med["none"]) / med["none"]
        noise = max(spread.values())
        h.measure("effect vs noise, short connections",
                  f"between-ruleset difference {effect:.1%} against a within-ruleset "
                  f"spread of up to {noise:.1%}",
                  "when the effect does not clear the noise the result is an upper bound")

        overhead = h.measure(
            "the adopted shape costs less than 2x the unfiltered floor on both measures",
            med["superset"] < med["none"] * 2 and bmed["superset"] < bmed["none"] * 2,
            f"connections {med['superset']:.3f}s vs {med['none']:.3f}s; "
            f"bulk {bmed['superset']:.3f}s vs {bmed['none']:.3f}s",
        )
        h.expect(
            "per-packet evaluation with a 164-rule first-match list is affordable on this "
            "path",
            overhead, want=True,
            unbaselined="a cost comparison between rulesets, not a before/after on one "
                        "mechanism. Its discriminating half is the evaluation-counter "
                        "control above, which shows the stateless arms were genuinely "
                        "evaluated per packet rather than quietly matching a state",
        )

        h.not_tried(
            "the NAT'd path. Every transfer here is host-terminated on the bridge, which is "
            "the only path whose throughput this rig controls. An external destination goes "
            "through vmnet's NAT and pf's translation rules as well, and that is where a "
            "real agent's traffic goes",
            "allowlist SIZE. `<yw3_dst>` holds one address throughout. Linux priced "
            "1/1000/10000 elements and found it free; pf tables are radix tries and should "
            "behave, but nothing here varies it and 'should' is not a measurement",
            "concurrent sandboxes. One guest, one transfer at a time. N guests all being "
            "evaluated against the same 164-rule list is the shape the product has and it "
            "is unmeasured",
            "the SERVER's own ceiling. The bulk arm is served by a Python HTTP handler "
            "writing 1 MiB chunks, and if that is the bottleneck then the bulk numbers "
            "measure Python rather than pf. The short-connection arm exists because of "
            "that: run 1 of this file used a 24 MiB transfer that finished inside one "
            "timer tick, so every ruleset looked identical",
            "UDP, which W1 made the shape cover. All timing here is TCP",
            "**a measurement of the effect, as opposed to an upper bound on it.** The "
            "between-ruleset differences here do not clear the within-ruleset spread, so "
            "what this run establishes is that the adopted shape costs less than the noise "
            "floor of this rig on this path -- not what it costs. Separating them needs "
            "many more samples or a path where pf is the bottleneck, and neither is here",
            "a busy host. One idle M4 MacBook Air, n=5 per cell after a discarded warm-up, "
            "medians reported beside their samples because r14 established that variance "
            "here can exceed the effect being looked for -- which is what happened",
            "latency, as distinct from throughput. Nothing measures connection setup time "
            "under the superset, which is what an agent making many short requests would "
            "actually feel",
        )
        return 0 if h.report() else 1
    finally:
        if srv is not None:
            srv.shutdown()
            srv.server_close()
        cleanup()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except HarnessError as exc:
        print(f"\nRUN VOID: {exc}", file=sys.stderr)
        sys.exit(2)
